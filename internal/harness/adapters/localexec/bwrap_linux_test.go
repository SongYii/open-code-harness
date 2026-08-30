//go:build linux

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

func TestBwrapArgvWrapsTargetWithRequiredNamespaceIsolation(t *testing.T) {
	got := bwrapArgv("/ws", "/ws/sub", []string{"echo", "hi"})
	want := []string{
		"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts",
		"--unshare-cgroup", "--unshare-net",
		"--die-with-parent", "--new-session",
		"--cap-drop", "ALL",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--tmpfs", "/tmp",
		"--bind", "/ws", "/ws",
		"--chdir", "/ws/sub",
		"echo", "hi",
	}
	if len(got) != len(want) {
		t.Fatalf("bwrapArgv() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bwrapArgv()[%d] = %q, want %q (full: %#v)", i, got[i], want[i], got)
		}
	}
}

func TestBwrapArgvPlacesTargetAfterSandboxFlags(t *testing.T) {
	target := []string{"sh", "-c", "echo hi; echo --bind"}
	got := bwrapArgv("/ws", "/ws", target)
	if len(got) < len(target) {
		t.Fatalf("bwrapArgv() shorter than target: %#v", got)
	}
	gotTail := got[len(got)-len(target):]
	for i := range target {
		if gotTail[i] != target[i] {
			t.Fatalf("target tail = %#v, want %#v (full: %#v)", gotTail, target, got)
		}
	}
}

func TestIsWSL1Version(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    bool
	}{
		{"wsl1", "Linux version 4.4.0-19041-Microsoft (Microsoft@Microsoft.com)", true},
		{"wsl2", "Linux version 5.15.90.1-microsoft-standard-WSL2 (oe-user@oe-host)", false},
		{"ordinary linux", "Linux version 7.0.0-1010-aws (buildd@lcy02-amd64-063)", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWSL1Version(tc.version); got != tc.want {
				t.Fatalf("isWSL1Version(%q) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}

// TestRunWrapsArgvInBwrapWhenAvailable exercises the Run() wiring itself —
// that it actually shells out to "bwrap" with the argv bwrapArgv builds —
// independent of whether this environment can functionally run a
// namespace-confined process (unprivileged user namespaces are blocked by
// AppArmor on some hardened hosts even when the bwrap binary is present),
// by substituting a fake bwrap on PATH that records its own argv instead
// of actually sandboxing anything.
func TestRunWrapsArgvInBwrapWhenAvailable(t *testing.T) {
	root := t.TempDir()
	runner, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	runner.bwrapAvailable = true

	fakeBinDir := t.TempDir()
	fakeBwrap := filepath.Join(fakeBinDir, "bwrap")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HOME/captured-args.txt\"\nexit 0\n"
	if err := os.WriteFile(fakeBwrap, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))

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
		t.Fatalf("fake bwrap was not invoked: %v", err)
	}
	captured := strings.Split(strings.TrimRight(string(capturedRaw), "\n"), "\n")
	want := bwrapArgv(root, root, []string{"echo", "hi"})
	if len(captured) != len(want) {
		t.Fatalf("captured argv = %#v, want %#v", captured, want)
	}
	for i := range want {
		if captured[i] != want[i] {
			t.Fatalf("captured argv[%d] = %q, want %q (full: %#v)", i, captured[i], want[i], captured)
		}
	}
}

// requireFunctionalBwrap skips the calling test when this environment's
// bwrap cannot actually confine a process (missing binary, WSL1, or a
// probe that runs but fails — for example unprivileged user namespaces
// blocked by AppArmor, which is the case on some hardened hosts even when
// the bwrap binary itself is installed).
func requireFunctionalBwrap(t *testing.T, runner *Runner) {
	t.Helper()
	if runner.Enforcement().Filesystem != EnforcementFull {
		t.Skip("bwrap is not functionally available in this environment")
	}
}

func TestBwrapConfinementDeniesWritesOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	runner, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	requireFunctionalBwrap(t, runner)
	outsideMarker := "/etc/och-bwrap-integration-test-should-not-exist"
	_ = os.Remove(outsideMarker)
	defer os.Remove(outsideMarker)
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

func TestBwrapConfinementDeniesNetwork(t *testing.T) {
	root := t.TempDir()
	runner, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	requireFunctionalBwrap(t, runner)
	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv: []string{"sh", "-c", `
			exec 3<>/dev/tcp/1.1.1.1/80 2>/dev/null && echo connected || echo denied
		`},
		Cwd:      root,
		Timeout:  5 * time.Second,
		MaxBytes: DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	if strings.TrimSpace(got.Output) != "denied" {
		t.Fatalf("output = %q, want network to be denied", got.Output)
	}
}

func TestBwrapConfinementHidesHostProcesses(t *testing.T) {
	root := t.TempDir()
	runner, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	requireFunctionalBwrap(t, runner)
	hostPid1Comm, err := os.ReadFile("/proc/1/comm")
	if err != nil {
		t.Skip("cannot read host /proc/1/comm to compare against")
	}
	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"cat", "/proc/1/comm"},
		Cwd:      root,
		Timeout:  5 * time.Second,
		MaxBytes: DefaultMaxBytes,
	})
	if err != nil || got.ExitCode != 0 {
		t.Fatalf("Run() = %#v err=%v", got, err)
	}
	if strings.TrimSpace(got.Output) == strings.TrimSpace(string(hostPid1Comm)) {
		t.Fatalf("sandboxed pid 1 comm = %q, same as host's real init: PID namespace not isolated", got.Output)
	}
}
