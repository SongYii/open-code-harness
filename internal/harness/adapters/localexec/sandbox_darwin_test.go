//go:build darwin

package localexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
	"golang.org/x/sys/unix"
)

func newInternalTestRunner(t *testing.T, root string) *Runner {
	t.Helper()
	runner, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close() })
	return runner
}

func TestSeatbeltArgvBindsWorkspaceRootAndAppendsTarget(t *testing.T) {
	got := seatbeltArgv("/Users/agent/ws", []string{"echo", "hi"})
	if len(got) < 5 {
		t.Fatalf("seatbeltArgv() too short: %#v", got)
	}
	if got[0] != "-p" {
		t.Fatalf("seatbeltArgv()[0] = %q, want -p", got[0])
	}
	if got[2] != "-DWORKSPACE_ROOT=/Users/agent/ws" {
		t.Fatalf("seatbeltArgv()[2] = %q, want -DWORKSPACE_ROOT=/Users/agent/ws", got[2])
	}
	tail := got[len(got)-2:]
	if tail[0] != "echo" || tail[1] != "hi" {
		t.Fatalf("seatbeltArgv() tail = %#v, want target appended", tail)
	}
}

func TestSeatbeltPolicyDenyByDefaultWithWorkspaceWriteException(t *testing.T) {
	policy := seatbeltArgv("/ws", nil)[1]
	must := []string{
		"(deny default)",
		"(deny file-write*)",
		`(allow file-write* (subpath (param "WORKSPACE_ROOT")))`,
		"(allow file-read*)",
		"(deny network*)",
	}
	for _, clause := range must {
		if !strings.Contains(policy, clause) {
			t.Fatalf("policy missing clause %q\npolicy:\n%s", clause, policy)
		}
	}
}

func TestSeatbeltCommandArgvUsesHardcodedExecutable(t *testing.T) {
	name, argv := seatbeltCommandArgv("/ws", []string{"echo", "hi"})
	if name != seatbeltExecutable {
		t.Fatalf("name = %q, want %q", name, seatbeltExecutable)
	}
	if len(argv) == 0 {
		t.Fatal("argv is empty")
	}
}

func TestRlimitEnforcementLevelIsPartialOnDarwin(t *testing.T) {
	if got := rlimitEnforcementLevel(); got != EnforcementPartial {
		t.Fatalf("rlimitEnforcementLevel() = %q, want partial", got)
	}
}

func TestCPURlimitEnforcementLevelIsFullOnDarwin(t *testing.T) {
	if got := cpuRlimitEnforcementLevel(); got != EnforcementFull {
		t.Fatalf("cpuRlimitEnforcementLevel() = %q, want full", got)
	}
}

// TestRlimitBracketSetsAndRestoresRLIMIT_CPU proves the bracket itself,
// independent of spawning any child: RLIMIT_CPU is lowered to the
// soft/hard pair for the duration of the returned closure's lifetime and
// restored exactly afterward, the same guarantee this package's own
// RLIMIT_AS bracket already has, verified again here since RLIMIT_CPU is
// a second, independent rlimit sharing the same bracket function.
func TestRlimitBracketSetsAndRestoresRLIMIT_CPU(t *testing.T) {
	var before unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_CPU, &before); err != nil {
		t.Fatalf("Getrlimit() err = %v", err)
	}

	var mu sync.Mutex
	restore := beginRlimitBracket(&mu, DefaultMemoryHighBytes)

	var during unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_CPU, &during); err != nil {
		t.Fatalf("Getrlimit() during bracket err = %v", err)
	}
	if during.Cur != DefaultCPUSoftSeconds {
		t.Fatalf("RLIMIT_CPU.Cur during bracket = %d, want %d", during.Cur, DefaultCPUSoftSeconds)
	}

	restore()

	var after unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_CPU, &after); err != nil {
		t.Fatalf("Getrlimit() after restore err = %v", err)
	}
	if after != before {
		t.Fatalf("RLIMIT_CPU after restore = %+v, want %+v", after, before)
	}
}

// TestIsCPUResourceLimitExitDetectsSIGXCPUOnly proves the signal-inspection
// function's own discrimination: a real child killed by SIGXCPU is
// attributable, one killed by an unrelated signal (SIGTERM, standing in
// for "some other reason entirely") is not, and one that exits normally
// is not — using real subprocesses and real *exec.ExitError values, not a
// hand-constructed fake, since exec.ExitError's own Sys() shape is what
// this function actually depends on.
func TestIsCPUResourceLimitExitDetectsSIGXCPUOnly(t *testing.T) {
	run := func(sig syscall.Signal) error {
		cmd := exec.Command("sleep", "5")
		if err := cmd.Start(); err != nil {
			t.Fatalf("Start() err = %v", err)
		}
		if err := cmd.Process.Signal(sig); err != nil {
			t.Fatalf("Signal(%v) err = %v", sig, err)
		}
		return cmd.Wait()
	}

	if got := isCPUResourceLimitExit(run(syscall.SIGXCPU)); !got {
		t.Fatal("isCPUResourceLimitExit() = false for a real SIGXCPU exit, want true")
	}
	if got := isCPUResourceLimitExit(run(syscall.SIGTERM)); got {
		t.Fatal("isCPUResourceLimitExit() = true for an unrelated SIGTERM exit, want false")
	}

	normal := exec.Command("true")
	normalErr := normal.Run()
	if got := isCPUResourceLimitExit(normalErr); got {
		t.Fatalf("isCPUResourceLimitExit(%v) = true for a normal exit, want false", normalErr)
	}
}

// TestCPUQuotaKillsARunawayCPUCommand is the real, gated integration test:
// with the soft/hard limits lowered to make the test fast (the same
// technique TestRunWrapsArgvInSeatbeltWhenAvailable's package-level var
// substitution already uses for seatbeltExecutable), a CPU-spinning
// command exceeds RLIMIT_CPU's soft limit, is terminated by the kernel's
// own SIGXCPU, and Run() reports ResourceLimited: true via the
// signal-inspection path this task adds — proving the full chain, not
// merely the bracket or the signal-detector in isolation.
func TestCPUQuotaKillsARunawayCPUCommand(t *testing.T) {
	originalSoft, originalHard := DefaultCPUSoftSeconds, DefaultCPUHardSeconds
	DefaultCPUSoftSeconds, DefaultCPUHardSeconds = 1, 2
	t.Cleanup(func() { DefaultCPUSoftSeconds, DefaultCPUHardSeconds = originalSoft, originalHard })

	root := t.TempDir()
	runner := newInternalTestRunner(t, root)

	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"sh", "-c", `s=""; while :; do s="$s.x"; done`},
		Cwd:      root,
		Timeout:  10 * time.Second,
		MaxBytes: DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if got.TimedOut {
		t.Fatalf("Run() = %#v, want the CPU quota to fire before the 10s timeout", got)
	}
	if !got.ResourceLimited {
		t.Fatalf("Run() = %#v, want ResourceLimited from RLIMIT_CPU", got)
	}
}

// requireFunctionalSeatbelt skips the calling test unless this file's own
// GOOS build (Darwin only, per the filename) also has a functionally
// working sandbox-exec: the binary present and a probe invocation that
// succeeds.
func requireFunctionalSeatbelt(t *testing.T, runner *Runner) {
	t.Helper()
	if runner.Enforcement().Filesystem != EnforcementFull {
		t.Skip("sandbox-exec is not functionally available in this environment")
	}
}

func TestSeatbeltConfinementDeniesWritesOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	runner := newInternalTestRunner(t, root)
	requireFunctionalSeatbelt(t, runner)

	outsideMarker := filepath.Join(t.TempDir(), "och-seatbelt-integration-test-should-not-exist")
	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"sh", "-c", "echo x > " + outsideMarker},
		Cwd:      root,
		Timeout:  5 * time.Second,
		MaxBytes: DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if got.ExitCode == 0 {
		t.Fatalf("write outside workspace succeeded: %#v", got)
	}
	if _, statErr := os.Stat(outsideMarker); !os.IsNotExist(statErr) {
		t.Fatalf("write outside workspace left a file behind: %v", statErr)
	}
}

func TestSeatbeltConfinementDeniesNetwork(t *testing.T) {
	root := t.TempDir()
	runner := newInternalTestRunner(t, root)
	requireFunctionalSeatbelt(t, runner)

	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"sh", "-c", networkProbeScript},
		Cwd:      root,
		Timeout:  8 * time.Second,
		MaxBytes: DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	assertNetworkDenied(t, got.Output)
}

// networkProbeScript is /bin/sh-portable (no bash-only /dev/tcp/ pseudo-
// device, which some restricted shells don't implement: it fails to even
// open the path, which would make a naive "did it fail" check pass
// regardless of whether the network is actually reachable). It connects
// to a raw IP, skipping DNS, so the result reflects socket-connect denial
// specifically.
const networkProbeScript = `
curl -sS --max-time 3 https://1.1.1.1/ >/dev/null 2>&1
code=$?
if [ "$code" -eq 127 ]; then echo notfound; exit 0; fi
if [ "$code" -eq 0 ]; then echo connected; exit 0; fi
echo denied
`

func assertNetworkDenied(t *testing.T, output string) {
	t.Helper()
	switch strings.TrimSpace(output) {
	case "notfound":
		t.Skip("curl is not available in this environment")
	case "connected":
		t.Fatal("network connection succeeded, want denied")
	case "denied":
	default:
		t.Fatalf("unexpected probe output: %q", output)
	}
}

// TestRunWrapsArgvInSeatbeltWhenAvailable exercises the Run() wiring
// itself, independent of whether this environment has a functional
// sandbox-exec, by forcing seatbeltAvailable and substituting a fake
// executable for the package-level seatbeltExecutable var (production
// code never overrides it; this is exactly why it's a var, not a const).
func TestRunWrapsArgvInSeatbeltWhenAvailable(t *testing.T) {
	root := t.TempDir()
	runner := newInternalTestRunner(t, root)
	runner.seatbeltAvailable = true

	original := seatbeltExecutable
	fakeBinDir := t.TempDir()
	fakeSandboxExec := filepath.Join(fakeBinDir, "fake-sandbox-exec")
	// NUL-delimited, not newline-delimited: the SBPL policy is itself one
	// argv element and contains many embedded newlines, which would
	// otherwise be indistinguishable from argv-element boundaries.
	script := "#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$HOME/captured-args.txt\"\nexit 0\n"
	if err := os.WriteFile(fakeSandboxExec, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	seatbeltExecutable = fakeSandboxExec
	t.Cleanup(func() { seatbeltExecutable = original })

	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"echo", "hi"},
		Cwd:      root,
		MaxBytes: DefaultMaxBytes,
	})
	if err != nil || got.ExitCode != 0 {
		t.Fatalf("Run() = %#v err=%v", got, err)
	}

	capturedRaw, err := os.ReadFile(filepath.Join(root, "captured-args.txt"))
	if err != nil {
		t.Fatalf("fake sandbox-exec was not invoked: %v", err)
	}
	captured := strings.Split(strings.TrimRight(string(capturedRaw), "\x00"), "\x00")
	wantArgv := seatbeltArgv(root, []string{"echo", "hi"})
	if len(captured) != len(wantArgv) {
		t.Fatalf("captured argv = %#v, want %#v", captured, wantArgv)
	}
	for i := range wantArgv {
		if captured[i] != wantArgv[i] {
			t.Fatalf("captured argv[%d] = %q, want %q (full: %#v)", i, captured[i], wantArgv[i], captured)
		}
	}
}
