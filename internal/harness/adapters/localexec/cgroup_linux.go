//go:build linux

package localexec

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// memoryHighFraction is the fraction of memory.high current usage must
// still exceed, after the kernel's high counter increments, before this
// project kills the process group ahead of the kernel's own hard OOM kill
// at memory.max — Grok Build's own default.
const memoryHighFraction = 0.90

var cgroupSeq uint64

// cgroupQuota owns one cgroup v2 child directory for a Runner's lifetime,
// reused as a per-invocation memory boundary (Grok Build's model): each
// Run call's process is moved into it and the cgroup is naturally empty
// again once that process exits. All methods are nil-receiver safe so
// Runner.Run can call them unconditionally regardless of whether a quota
// is actually active.
type cgroupQuota struct {
	fsPath     string
	memoryHigh uint64
	inotifyFD  int

	mu     sync.Mutex
	notify map[int]chan struct{}
}

// cgroupV2SelfPath extracts this process's cgroup v2 (unified hierarchy)
// path from the contents of /proc/self/cgroup, the same "0::" line the
// architecture gate's Grok Build reading uses to detect cgroup v2.
func cgroupV2SelfPath(procSelfCgroup string) (path string, ok bool) {
	for _, line := range strings.Split(procSelfCgroup, "\n") {
		if rest, found := strings.CutPrefix(line, "0::"); found {
			return strings.TrimSpace(rest), true
		}
	}
	return "", false
}

func probeCgroupV2() (selfPath string, ok bool) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", false
	}
	return cgroupV2SelfPath(string(data))
}

// newCgroupQuota creates one child cgroup under this process's own cgroup
// and configures memory.high/memory.max, matching Grok Build's own
// approach: best-effort enabling of the memory controller on the parent
// (may already be enabled, or may not be permitted — for example this
// process's own cgroup still has a resident process, which cgroup v2's "no
// internal process" constraint forbids delegating further from), then
// writing the limits on the child itself. Any failure to get a working
// memory-limited child is reported as unavailable so the caller can fall
// back to no quota; this never fails Runner construction.
//
// cpu.max is written afterward, independently: a failure there (memReason
// empty, cpuReason not) never undoes the memory quota that already
// succeeded (CPU quota design §3) — the cpu controller may not be
// delegated on a host where memory is, and that must not cost this
// project the memory quota it already has.
func newCgroupQuota(memoryHighBytes, memoryMaxBytes, cpuPeriodMicros, cpuQuotaMicros uint64) (quota *cgroupQuota, memReason string, cpuReason string) {
	selfPath, ok := probeCgroupV2()
	if !ok {
		return nil, "cgroup v2 (unified hierarchy) not detected", ""
	}
	name := fmt.Sprintf("och-exec-%d-%d", os.Getpid(), atomic.AddUint64(&cgroupSeq, 1))
	fsPath := filepath.Join("/sys/fs/cgroup", selfPath, name)
	if err := os.MkdirAll(fsPath, 0o755); err != nil {
		return nil, "creating cgroup: " + err.Error(), ""
	}
	parent := filepath.Dir(fsPath)
	// Best-effort: this may already be enabled by an ancestor, or writing
	// it here may be refused entirely; either way we still try to use the
	// child below and let that attempt be the real availability signal.
	_ = os.WriteFile(filepath.Join(parent, "cgroup.subtree_control"), []byte("+memory +cpu"), 0o644)
	if err := os.WriteFile(filepath.Join(fsPath, "memory.high"), []byte(strconv.FormatUint(memoryHighBytes, 10)), 0o644); err != nil {
		_ = os.Remove(fsPath)
		return nil, "memory controller not delegated to this cgroup: " + err.Error(), ""
	}
	if err := os.WriteFile(filepath.Join(fsPath, "memory.max"), []byte(strconv.FormatUint(memoryMaxBytes, 10)), 0o644); err != nil {
		_ = os.Remove(fsPath)
		return nil, "writing memory.max: " + err.Error(), ""
	}
	quota = &cgroupQuota{
		fsPath:     fsPath,
		memoryHigh: memoryHighBytes,
		inotifyFD:  -1,
		notify:     make(map[int]chan struct{}),
	}
	if err := quota.startMonitor(); err != nil {
		_ = os.Remove(fsPath)
		return nil, "starting memory monitor: " + err.Error(), ""
	}
	cpuMax := fmt.Sprintf("%d %d", cpuQuotaMicros, cpuPeriodMicros)
	if err := os.WriteFile(filepath.Join(fsPath, "cpu.max"), []byte(cpuMax), 0o644); err != nil {
		cpuReason = "cpu controller not delegated to this cgroup: " + err.Error()
	}
	return quota, "", cpuReason
}

func (q *cgroupQuota) startMonitor() error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		return err
	}
	if _, err := unix.InotifyAddWatch(fd, filepath.Join(q.fsPath, "memory.events"), unix.IN_MODIFY); err != nil {
		_ = unix.Close(fd)
		return err
	}
	q.inotifyFD = fd
	go q.monitorLoop()
	return nil
}

// monitorLoop watches memory.events for the kernel's "high" counter to
// increment and, when current usage is still above memoryHighFraction of
// memory.high, notifies every currently resident PID's registered channel
// so its own Run call can kill it and report ResourceLimited. It exits
// once the watched fd is closed (Runner teardown).
func (q *cgroupQuota) monitorLoop() {
	lastHigh, _ := q.readHighCounter()
	buf := make([]byte, 4096)
	for {
		n, err := unix.Read(q.inotifyFD, buf)
		if err != nil || n <= 0 {
			return
		}
		high, err := q.readHighCounter()
		if err != nil || high <= lastHigh {
			continue
		}
		lastHigh = high
		current, err := q.readMemoryCurrent()
		if err != nil {
			continue
		}
		if float64(current) < float64(q.memoryHigh)*memoryHighFraction {
			continue
		}
		q.notifyResidents()
	}
}

func (q *cgroupQuota) notifyResidents() {
	pids, err := q.residentPIDs()
	if err != nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, pid := range pids {
		ch, ok := q.notify[pid]
		if !ok {
			continue
		}
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (q *cgroupQuota) readHighCounter() (uint64, error) {
	data, err := os.ReadFile(filepath.Join(q.fsPath, "memory.events"))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "high "); ok {
			return strconv.ParseUint(strings.TrimSpace(rest), 10, 64)
		}
	}
	return 0, fmt.Errorf("localexec: memory.events missing high counter")
}

// readThrottledCount reads cpu.stat's nr_throttled counter — the number of
// periods in which this cgroup was throttled by cpu.max since the cgroup
// was created. A nil quota (no cgroup active at all) and any read error
// (the cpu controller was never delegated, so cpu.stat does not exist)
// both report zero, never a hard failure: the caller treats either as
// "not throttled".
func (q *cgroupQuota) readThrottledCount() (uint64, error) {
	if q == nil {
		return 0, nil
	}
	data, err := os.ReadFile(filepath.Join(q.fsPath, "cpu.stat"))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "nr_throttled "); ok {
			return strconv.ParseUint(strings.TrimSpace(rest), 10, 64)
		}
	}
	return 0, fmt.Errorf("localexec: cpu.stat missing nr_throttled counter")
}

func (q *cgroupQuota) readMemoryCurrent() (uint64, error) {
	data, err := os.ReadFile(filepath.Join(q.fsPath, "memory.current"))
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}

func (q *cgroupQuota) residentPIDs() ([]int, error) {
	data, err := os.ReadFile(filepath.Join(q.fsPath, "cgroup.procs"))
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, field := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

// addProcess moves pid (and, since cgroup membership is inherited by
// forked children, its whole process tree) into the quota's cgroup. A nil
// quota is a no-op: nothing to add pid to.
func (q *cgroupQuota) addProcess(pid int) error {
	if q == nil {
		return nil
	}
	return os.WriteFile(filepath.Join(q.fsPath, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0o644)
}

// register returns a channel that receives a value if pid is resident in
// the quota's cgroup when a memory.high breach fires. A nil quota returns
// a nil channel, on which a select case blocks forever — correctly
// disabling that case when no quota is active.
func (q *cgroupQuota) register(pid int) <-chan struct{} {
	if q == nil {
		return nil
	}
	ch := make(chan struct{}, 1)
	q.mu.Lock()
	q.notify[pid] = ch
	q.mu.Unlock()
	return ch
}

func (q *cgroupQuota) unregister(pid int) {
	if q == nil {
		return
	}
	q.mu.Lock()
	delete(q.notify, pid)
	q.mu.Unlock()
}

// close stops the monitor goroutine and removes the cgroup directory. A
// nil quota is a no-op.
func (q *cgroupQuota) close() {
	if q == nil {
		return
	}
	if q.inotifyFD >= 0 {
		_ = unix.Close(q.inotifyFD)
	}
	_ = os.Remove(q.fsPath)
}
