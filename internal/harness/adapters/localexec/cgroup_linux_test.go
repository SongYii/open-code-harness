//go:build linux

package localexec

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestCgroupV2SelfPath(t *testing.T) {
	cases := []struct {
		name     string
		contents string
		wantPath string
		wantOK   bool
	}{
		{"unified v2", "0::/user.slice/user-1000.slice/session.scope\n", "/user.slice/user-1000.slice/session.scope", true},
		{"v1 hybrid with v2 line", "1:memory:/foo\n0::/bar\n", "/bar", true},
		{"v1 only, no v2 line", "1:memory:/foo\n7:pids:/foo\n", "", false},
		{"empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPath, gotOK := cgroupV2SelfPath(tc.contents)
			if gotOK != tc.wantOK || gotPath != tc.wantPath {
				t.Fatalf("cgroupV2SelfPath(%q) = (%q, %v), want (%q, %v)", tc.contents, gotPath, gotOK, tc.wantPath, tc.wantOK)
			}
		})
	}
}

func TestCgroupQuotaParsesMemoryEventsAndProcs(t *testing.T) {
	dir := t.TempDir()
	writeFile := func(name, contents string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("memory.events", "low 0\nhigh 3\nmax 0\noom 0\noom_kill 0\n")
	writeFile("memory.current", "104857600\n")
	writeFile("cgroup.procs", "123\n456\n\n789")

	q := &cgroupQuota{fsPath: dir, memoryHigh: DefaultMemoryHighBytes}
	high, err := q.readHighCounter()
	if err != nil || high != 3 {
		t.Fatalf("readHighCounter() = %d, %v, want 3, nil", high, err)
	}
	current, err := q.readMemoryCurrent()
	if err != nil || current != 104857600 {
		t.Fatalf("readMemoryCurrent() = %d, %v, want 104857600, nil", current, err)
	}
	pids, err := q.residentPIDs()
	if err != nil {
		t.Fatalf("residentPIDs() err = %v", err)
	}
	want := []int{123, 456, 789}
	if len(pids) != len(want) {
		t.Fatalf("residentPIDs() = %v, want %v", pids, want)
	}
	for i := range want {
		if pids[i] != want[i] {
			t.Fatalf("residentPIDs() = %v, want %v", pids, want)
		}
	}
}

func TestCgroupQuotaParsesCPUStatThrottledCount(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cpu.stat"), []byte("usage_usec 500000\nuser_usec 400000\nsystem_usec 100000\nnr_periods 10\nnr_throttled 4\nthrottled_usec 250000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	q := &cgroupQuota{fsPath: dir}
	count, err := q.readThrottledCount()
	if err != nil || count != 4 {
		t.Fatalf("readThrottledCount() = %d, %v, want 4, nil", count, err)
	}
}

func TestCgroupQuotaReadThrottledCountMissingFileIsNotAHardFailure(t *testing.T) {
	q := &cgroupQuota{fsPath: t.TempDir()} // no cpu.stat written: cpu controller never delegated
	count, err := q.readThrottledCount()
	if err == nil {
		t.Fatalf("readThrottledCount() with no cpu.stat = (%d, nil), want a read error", count)
	}
	if count != 0 {
		t.Fatalf("readThrottledCount() = %d on error, want 0", count)
	}
}

func TestCgroupQuotaNilReceiverMethodsAreNoOps(t *testing.T) {
	var q *cgroupQuota
	if err := q.addProcess(1); err != nil {
		t.Fatalf("addProcess on nil quota = %v, want nil", err)
	}
	if ch := q.register(1); ch != nil {
		t.Fatalf("register on nil quota = %v, want nil channel", ch)
	}
	q.unregister(1) // must not panic
	q.close()       // must not panic
	if count, err := q.readThrottledCount(); count != 0 || err != nil {
		t.Fatalf("readThrottledCount() on nil quota = (%d, %v), want (0, nil)", count, err)
	}
}

// requireFunctionalCgroup skips the calling test when this environment's
// cgroup v2 memory controller cannot actually be delegated to a child
// cgroup (for example the calling process's own cgroup still holds a
// resident process, which cgroup v2's "no internal process" constraint
// forbids delegating subtree_control from — a common shape for an
// interactive shell or session scope that was never set up for
// delegation, distinct from cgroup v2 simply being absent).
func requireFunctionalCgroup(t *testing.T, runner *Runner) {
	t.Helper()
	if runner.Enforcement().Memory != EnforcementFull {
		t.Skip("cgroup v2 memory quota is not functionally available in this environment")
	}
}

// requireFunctionalCPUQuota skips the calling test when this environment's
// cgroup v2 cpu controller cannot actually be delegated to a child cgroup,
// mirroring requireFunctionalCgroup's own reasoning for memory — the two
// controllers fail independently (CPU quota design §3), so a host where
// memory delegates but cpu does not (or vice versa) is a real, distinct
// case this helper checks for on its own.
func requireFunctionalCPUQuota(t *testing.T, runner *Runner) {
	t.Helper()
	if runner.Enforcement().CPU != EnforcementFull {
		t.Skip("cgroup v2 cpu quota is not functionally available in this environment")
	}
}

// TestCgroupCPUQuotaThrottlesParallelWork proves the real kernel behavior,
// not merely the Go-side wiring already covered by
// TestRunReportsThrottledFromHandWiredCPUStat: four CPU-bound loops
// spawned in parallel (inheriting cgroup membership from the exec'd shell
// through fork, exactly like the memory test's growing-string child does)
// demand more than the one-core cap this project's default cpu.max
// configures, so nr_throttled must be nonzero within the run's own
// timeout window. The loops never exit on their own; TimedOut is expected
// alongside Throttled, not a test failure — Throttled is additive (design
// doc §3), not a replacement outcome.
func TestCgroupCPUQuotaThrottlesParallelWork(t *testing.T) {
	root := t.TempDir()
	runner := newInternalTestRunner(t, root)
	requireFunctionalCPUQuota(t, runner)

	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv: []string{"sh", "-c", `
			for i in 1 2 3 4; do
				sh -c 'while :; do :; done' &
			done
			wait
		`},
		Cwd:      root,
		Timeout:  2 * time.Second,
		MaxBytes: DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if !got.TimedOut {
		t.Fatalf("Run() = %#v, want TimedOut (the parallel loops never exit on their own)", got)
	}
	if !got.Throttled {
		t.Fatalf("Run() = %#v, want Throttled true: four parallel CPU-bound loops under a one-core cgroup quota must be throttled", got)
	}
}

func TestCgroupMemoryQuotaKillsAMemoryGrowingCommand(t *testing.T) {
	root := t.TempDir()
	runner := newInternalTestRunner(t, root)
	requireFunctionalCgroup(t, runner)

	childPath := filepath.Join(root, "child.pid")
	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv: []string{"sh", "-c", `
			echo $$ > "$1"
			s=$(head -c 1000000 /dev/zero | tr '\0' 'x')
			while :; do s="$s$s"; done
		`, "sh", childPath},
		Cwd:      root,
		Timeout:  15 * time.Second,
		MaxBytes: DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if !got.ResourceLimited {
		t.Fatalf("Run() = %#v, want ResourceLimited", got)
	}
	if got.TimedOut {
		t.Fatalf("Run() = %#v, want the memory quota to fire before the 15s timeout", got)
	}
	raw, err := os.ReadFile(childPath)
	if err != nil {
		t.Fatalf("child never announced its pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		t.Fatalf("child pid file %q", raw)
	}
	assertProcessGroupDead(t, pid)
}

// TestRunKillsOnResourceLimitSignal exercises the Run() wiring itself —
// that a signal on the registered channel actually kills the process and
// sets ResourceLimited — independent of whether this environment's cgroup
// v2 memory controller is functionally delegated (blocked by the "no
// internal process" constraint on some hosts, this one included), by
// hand-wiring a bare cgroupQuota instead of going through newCgroupQuota.
func TestRunKillsOnResourceLimitSignal(t *testing.T) {
	root := t.TempDir()
	runner := newInternalTestRunner(t, root)
	runner.cgroup = &cgroupQuota{fsPath: t.TempDir(), inotifyFD: -1, notify: make(map[int]chan struct{})}

	childPath := filepath.Join(root, "child.pid")
	type outcome struct {
		result tools.CommandResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runner.Run(context.Background(), tools.CommandSpec{
			Argv:     []string{"sh", "-c", `echo $$ > "$1"; sleep 120`, "sh", childPath},
			Cwd:      root,
			Timeout:  10 * time.Second,
			MaxBytes: DefaultMaxBytes,
		})
		done <- outcome{result, err}
	}()

	pid := waitForPIDFile(t, childPath)
	ch := waitForRegisteredChannel(t, runner.cgroup, pid)
	ch <- struct{}{}

	got := <-done
	if got.err != nil {
		t.Fatalf("Run() err = %v", got.err)
	}
	if !got.result.ResourceLimited {
		t.Fatalf("Run() = %#v, want ResourceLimited", got.result)
	}
	assertProcessGroupDead(t, pid)
}

// TestRunReportsThrottledFromHandWiredCPUStat exercises Run()'s own
// wiring — that it reads cpu.stat after the process exits and sets
// CommandResult.Throttled — independent of whether this environment's
// cgroup v2 cpu controller is functionally delegated, by hand-wiring a
// bare cgroupQuota whose fsPath is a plain directory this test controls
// (mirroring TestRunKillsOnResourceLimitSignal's own hand-wiring
// technique for the memory-kill path).
func TestRunReportsThrottledFromHandWiredCPUStat(t *testing.T) {
	root := t.TempDir()
	runner := newInternalTestRunner(t, root)
	cgroupDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cgroupDir, "cpu.stat"), []byte("nr_throttled 1\nthrottled_usec 1000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner.cgroup = &cgroupQuota{fsPath: cgroupDir, inotifyFD: -1, notify: make(map[int]chan struct{})}

	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"echo", "ok"},
		Cwd:      root,
		Timeout:  5 * time.Second,
		MaxBytes: DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if !got.Throttled {
		t.Fatalf("Run() = %#v, want Throttled true from the hand-wired cpu.stat", got)
	}
}

// TestRunReportsNotThrottledWhenCPUStatShowsNoThrottling proves the
// converse of the test above with the same hand-wiring technique: a
// nr_throttled of zero reports Throttled: false, not merely "true by
// default whenever a cgroup is wired up at all".
func TestRunReportsNotThrottledWhenCPUStatShowsNoThrottling(t *testing.T) {
	root := t.TempDir()
	runner := newInternalTestRunner(t, root)
	cgroupDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cgroupDir, "cpu.stat"), []byte("nr_throttled 0\nthrottled_usec 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner.cgroup = &cgroupQuota{fsPath: cgroupDir, inotifyFD: -1, notify: make(map[int]chan struct{})}

	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"echo", "ok"},
		Cwd:      root,
		Timeout:  5 * time.Second,
		MaxBytes: DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if got.Throttled {
		t.Fatalf("Run() = %#v, want Throttled false when nr_throttled is 0", got)
	}
}

// TestCPUControllerFailureLeavesMemoryQuotaActive proves the two
// controllers fail independently: an invalid cpu.max period (rejected by
// the kernel regardless of host-specific delegation quirks) must not
// undo an already-successful memory.high/memory.max write. Skips, like
// TestCgroupMemoryQuotaKillsAMemoryGrowingCommand, on a host where even
// the memory controller cannot be delegated at all (this one included),
// since there is nothing to prove independence between two controllers
// when neither one works.
func TestCPUControllerFailureLeavesMemoryQuotaActive(t *testing.T) {
	quota, memReason, cpuReason := newCgroupQuota(DefaultMemoryHighBytes, DefaultMemoryHighBytes+DefaultMemoryHeadroomBytes, 0, 0)
	if quota == nil {
		t.Skip("cgroup v2 memory quota is not functionally available in this environment")
	}
	t.Cleanup(quota.close)
	if memReason != "" {
		t.Fatalf("memReason = %q, want empty (memory succeeded)", memReason)
	}
	if cpuReason == "" {
		t.Fatal("cpuReason is empty, want a failure reason for an invalid (zero) cpu.max period")
	}
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw))); convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for pid file %s", path)
	return 0
}

func waitForRegisteredChannel(t *testing.T, q *cgroupQuota, pid int) chan struct{} {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		ch, ok := q.notify[pid]
		q.mu.Unlock()
		if ok {
			return ch
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Run() never registered pid %d with the cgroup quota", pid)
	return nil
}

func assertProcessGroupDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d still alive", pid)
}
