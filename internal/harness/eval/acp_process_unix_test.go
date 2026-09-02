//go:build unix

package eval

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

var (
	acpChildBuildOnce sync.Once
	acpChildBuildPath string
	acpChildBuildErr  error
)

// buildACPChild builds testdata/acpchild once per test binary run and
// reuses the same path for every test that needs it.
func buildACPChild(t *testing.T) string {
	t.Helper()
	acpChildBuildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "acpchild-build")
		if err != nil {
			acpChildBuildErr = err
			return
		}
		path := filepath.Join(dir, "acpchild")
		build := exec.Command("go", "build", "-o", path, "./testdata/acpchild")
		if out, err := build.CombinedOutput(); err != nil {
			acpChildBuildErr = err
			t.Logf("go build ./testdata/acpchild: %v\n%s", err, out)
			return
		}
		acpChildBuildPath = path
	})
	if acpChildBuildErr != nil {
		t.Fatalf("build acpchild: %v", acpChildBuildErr)
	}
	return acpChildBuildPath
}

func TestStartACPProcessSpawnsInItsOwnProcessGroup(t *testing.T) {
	child := buildACPChild(t)
	process, err := startACPProcess(child, nil, os.Environ(), t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("startACPProcess: %v", err)
	}
	defer func() {
		_ = process.stdin.Close()
		_, _ = process.waitTimeout(2 * time.Second)
	}()

	pgid, err := syscall.Getpgid(process.pid())
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	if pgid != process.pid() {
		t.Fatalf("pgid = %d, want the child's own pid %d (new process group leader)", pgid, process.pid())
	}
}

func TestACPProcessNormalShutdownClosesStdinAndReaps(t *testing.T) {
	child := buildACPChild(t)
	process, err := startACPProcess(child, nil, os.Environ(), t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("startACPProcess: %v", err)
	}
	if process.isReaped() {
		t.Fatal("isReaped() = true before the process ever exited")
	}
	if err := process.stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	exited, waitErr := process.waitTimeout(5 * time.Second)
	if !exited {
		t.Fatal("waitTimeout() = false, want the child to exit promptly on stdin EOF")
	}
	if waitErr != nil {
		t.Fatalf("wait error = %v, want a clean exit", waitErr)
	}
	if !process.isReaped() {
		t.Fatal("isReaped() = false after wait already returned")
	}
}

func TestACPProcessWaitTimeoutReportsNotExitedForAHangingChild(t *testing.T) {
	child := buildACPChild(t)
	process, err := startACPProcess(child, []string{"-mode", "hang"}, os.Environ(), t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("startACPProcess: %v", err)
	}
	_ = process.stdin.Close() // a hanging child never even reads stdin; EOF has no effect

	exited, _ := process.waitTimeout(200 * time.Millisecond)
	if exited {
		t.Fatal("waitTimeout() = true for a child that never exits, want false")
	}
	if process.isReaped() {
		t.Fatal("isReaped() = true despite waitTimeout() reporting not-exited")
	}

	// Task 12 does not implement the SIGTERM/SIGKILL escalation ladder
	// (Task 13's own scope); this test still must not leak the child it
	// started, so it reaps directly here rather than leaving a hung
	// process behind for the test runner's own process tree.
	if killErr := syscall.Kill(process.pid(), syscall.SIGKILL); killErr != nil {
		t.Fatalf("cleanup kill: %v", killErr)
	}
	if exited, waitErr := process.waitTimeout(5 * time.Second); !exited {
		t.Fatalf("child did not exit after SIGKILL: %v", waitErr)
	}
}

func TestACPProcessNonZeroExitIsReportedNotSwallowed(t *testing.T) {
	child := buildACPChild(t)
	process, err := startACPProcess(child, []string{"-mode", "exit-nonzero"}, os.Environ(), t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("startACPProcess: %v", err)
	}
	// exit-nonzero waits for one real initialize frame, answers it, then
	// exits(1) right after -- it never reads further, so no stdin close
	// is needed once this one frame is sent.
	if _, err := process.stdin.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")); err != nil {
		t.Fatalf("write initialize frame: %v", err)
	}
	exited, waitErr := process.waitTimeout(5 * time.Second)
	if !exited {
		t.Fatal("waitTimeout() = false, want the child to have exited on its own")
	}
	if waitErr == nil {
		t.Fatal("wait error = nil, want a non-zero-exit error")
	}
}

func TestACPProcessBoundedStderrTruncatesRatherThanGrowingUnbounded(t *testing.T) {
	child := buildACPChild(t)
	const limit = 64 * 1024
	process, err := startACPProcess(child, []string{"-mode", "huge-stderr"}, os.Environ(), t.TempDir(), limit)
	if err != nil {
		t.Fatalf("startACPProcess: %v", err)
	}
	defer func() {
		_ = process.stdin.Close()
		_, _ = process.waitTimeout(5 * time.Second)
	}()

	// Give the child's stderr-writing goroutine time to exceed the bound;
	// polling on stderr.Truncated() rather than a fixed sleep so this
	// isn't flaky on a slow CI host.
	deadline := time.Now().Add(5 * time.Second)
	for !process.stderr.Truncated() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !process.stderr.Truncated() {
		t.Fatal("stderr was never marked truncated despite the child writing well past the limit")
	}
	if int64(len(process.stderr.Bytes())) > limit {
		t.Fatalf("captured stderr = %d bytes, want at most %d", len(process.stderr.Bytes()), limit)
	}
}
