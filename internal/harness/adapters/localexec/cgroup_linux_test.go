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
