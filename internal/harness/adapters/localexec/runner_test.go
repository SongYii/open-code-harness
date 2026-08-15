//go:build unix

package localexec_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/localexec"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestEnforcementPartial(t *testing.T) {
	if localexec.Enforcement != "partial" {
		t.Fatalf("Enforcement = %q, want partial", localexec.Enforcement)
	}
}

func TestRunEcho(t *testing.T) {
	runner, root := newTestRunner(t)
	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"echo", "ok"},
		Cwd:      root,
		MaxBytes: localexec.DefaultMaxBytes,
	})
	if err != nil || got.ExitCode != 0 || got.TimedOut || got.Truncated {
		t.Fatalf("Run() = %#v err=%v", got, err)
	}
	if strings.TrimSpace(got.Output) != "ok" {
		t.Fatalf("output = %q", got.Output)
	}
}

func TestArgvOnlyNoShellExpansion(t *testing.T) {
	runner, root := newTestRunner(t)
	pwned := filepath.Join(root, "pwned")
	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"echo", "hello; touch pwned"},
		Cwd:      root,
		MaxBytes: localexec.DefaultMaxBytes,
	})
	if err != nil || got.ExitCode != 0 {
		t.Fatalf("Run() = %#v err=%v", got, err)
	}
	if !strings.Contains(got.Output, "hello; touch pwned") {
		t.Fatalf("output = %q, want literal argv", got.Output)
	}
	if _, err := os.Lstat(pwned); !os.IsNotExist(err) {
		t.Fatal("shell metacharacters were expanded")
	}
}

func TestScrubbedEnv(t *testing.T) {
	runner, root := newTestRunner(t)
	t.Setenv("AWS_SECRET_ACCESS_KEY", "should-not-leak")
	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"sh", "-c", `printf '%s\n' "PATH=$PATH" "HOME=$HOME" "TMPDIR=$TMPDIR" "AWS=$AWS_SECRET_ACCESS_KEY"`},
		Cwd:      root,
		MaxBytes: localexec.DefaultMaxBytes,
	})
	if err != nil || got.ExitCode != 0 {
		t.Fatalf("Run() = %#v err=%v", got, err)
	}
	lines := strings.Split(strings.TrimRight(got.Output, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("env lines = %#v", lines)
	}
	if !strings.HasPrefix(lines[0], "PATH=") || strings.TrimPrefix(lines[0], "PATH=") == "" {
		t.Fatalf("PATH missing: %q", lines[0])
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimPrefix(lines[1], "HOME=") != realRoot {
		t.Fatalf("HOME = %q, want %q", lines[1], realRoot)
	}
	tmp := strings.TrimPrefix(lines[2], "TMPDIR=")
	if !strings.HasPrefix(tmp, realRoot+string(filepath.Separator)) {
		t.Fatalf("TMPDIR = %q, want under %q", tmp, realRoot)
	}
	if strings.TrimPrefix(lines[3], "AWS=") != "" {
		t.Fatalf("AWS_SECRET_ACCESS_KEY leaked: %q", lines[3])
	}
	if _, err := os.Lstat(tmp); !os.IsNotExist(err) {
		t.Fatalf("TMPDIR left behind: %v", err)
	}
}

func TestTimeoutKillsProcessGroup(t *testing.T) {
	runner, root := newTestRunner(t)
	childPath := filepath.Join(root, "child.pid")
	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"sh", "-c", `sleep 120 & echo $! > "$1"; wait`, "sh", childPath},
		Cwd:      root,
		Timeout:  time.Second,
		MaxBytes: localexec.DefaultMaxBytes,
	})
	if err != nil || !got.TimedOut {
		t.Fatalf("Run() = %#v err=%v, want TimedOut", got, err)
	}
	assertKilledPIDFile(t, childPath)
}

func TestCancelKillsProcessGroup(t *testing.T) {
	runner, root := newTestRunner(t)
	ready := filepath.Join(root, "ready")
	childPath := filepath.Join(root, "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct {
		result tools.CommandResult
		err    error
	}, 1)
	go func() {
		result, err := runner.Run(ctx, tools.CommandSpec{
			Argv:     []string{"sh", "-c", `sleep 120 & echo $! > "$1"; echo ready > "$2"; wait`, "sh", childPath, ready},
			Cwd:      root,
			Timeout:  10 * time.Second,
			MaxBytes: localexec.DefaultMaxBytes,
		})
		done <- struct {
			result tools.CommandResult
			err    error
		}{result, err}
	}()
	waitFile(t, ready)
	cancel()
	got := <-done
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("Run() = %#v err=%v, want canceled", got.result, got.err)
	}
	assertKilledPIDFile(t, childPath)
}

func TestOutputCapKillsAndTruncates(t *testing.T) {
	runner, root := newTestRunner(t)
	const maxBytes = localexec.DefaultMaxBytes
	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"sh", "-c", "while :; do printf %s xxxxxxxxx; done"},
		Cwd:      root,
		Timeout:  5 * time.Second,
		MaxBytes: maxBytes,
	})
	if err != nil || !got.Truncated || got.TimedOut {
		t.Fatalf("Run() = %#v err=%v, want truncated", got, err)
	}
	if len(got.Output) != maxBytes {
		t.Fatalf("len(output) = %d, want %d", len(got.Output), maxBytes)
	}
}

func TestRejectsEmptyArgvAndNonDirCwd(t *testing.T) {
	runner, root := newTestRunner(t)
	if _, err := runner.Run(context.Background(), tools.CommandSpec{Cwd: root, Argv: nil}); err == nil {
		t.Fatal("empty argv: expected error")
	}
	if _, err := runner.Run(context.Background(), tools.CommandSpec{Cwd: root, Argv: []string{""}}); err == nil {
		t.Fatal("empty argv0: expected error")
	}
	if _, err := runner.Run(context.Background(), tools.CommandSpec{Argv: []string{"echo", "x"}}); err == nil {
		t.Fatal("empty cwd: expected error")
	}
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), tools.CommandSpec{Argv: []string{"echo", "x"}, Cwd: file}); err == nil {
		t.Fatal("file cwd: expected error")
	}
}

func TestCwdAndArgvMustStayInWorkspace(t *testing.T) {
	runner, root := newTestRunner(t)
	outside := t.TempDir()
	if _, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv: []string{"echo", "x"},
		Cwd:  outside,
	}); !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatalf("outside cwd error = %v", err)
	}

	marker := filepath.Join(root, "ran")
	if _, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv: []string{"/bin/sh", "-c", "echo ran > " + marker},
		Cwd:  root,
	}); !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatalf("abs argv0 outside error = %v", err)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatal("outside argv0 was executed")
	}
}

func TestWorkspaceScriptArgv(t *testing.T) {
	runner, root := newTestRunner(t)
	script := filepath.Join(root, "bin", "hi")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := runner.Run(context.Background(), tools.CommandSpec{
		Argv:     []string{"bin/hi"},
		Cwd:      root,
		MaxBytes: localexec.DefaultMaxBytes,
	})
	if err != nil || got.ExitCode != 0 || strings.TrimSpace(got.Output) != "hi" {
		t.Fatalf("Run() = %#v err=%v", got, err)
	}
}

func TestNewRejectsFileWorkspace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := localexec.New(path); err == nil {
		t.Fatal("expected error for file workspace")
	}
	if _, err := localexec.New(""); err == nil {
		t.Fatal("expected error for empty workspace")
	}
}

func newTestRunner(t *testing.T) (*localexec.Runner, string) {
	t.Helper()
	root := t.TempDir()
	runner, err := localexec.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return runner, root
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func assertKilledPIDFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || pid <= 0 {
			t.Fatalf("child pid file %q", raw)
		}
		last = syscall.Kill(pid, 0)
		if last != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if last == nil {
		t.Fatal("child process still alive")
	}
}
