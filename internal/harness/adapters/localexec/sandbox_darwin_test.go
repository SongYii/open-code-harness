//go:build darwin

package localexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
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
