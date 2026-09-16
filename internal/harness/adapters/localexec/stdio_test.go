//go:build unix

package localexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// An actual child executable, not a fake Wait or signal implementation. The
// tree mode keeps the parent alive to reap its child after a GROUP signal;
// killing only the parent or merely closing stdin cannot make the test pass.
func TestStdioProcessHelper(t *testing.T) {
	mode := ""
	for _, arg := range os.Args {
		if value, ok := strings.CutPrefix(arg, "--och-stdio-helper="); ok {
			mode = value
		}
	}
	if mode == "" {
		return
	}
	switch mode {
	case "child":
		signal.Reset(syscall.SIGTERM)
		time.Sleep(time.Hour)
	case "tree":
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
		binary, _ := os.Executable()
		child := exec.Command(binary, "-test.run=^TestStdioProcessHelper$", "--", "--och-stdio-helper=child")
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		fmt.Printf("ready %d\n", child.Process.Pid)
		_ = child.Wait()
	case "stubborn":
		signal.Ignore(syscall.SIGTERM)
		fmt.Println("ready")
		time.Sleep(time.Hour)
	case "echo":
		fmt.Println("ready")
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "tail":
		fmt.Print(strings.Repeat("x", 128<<10))
	}
	os.Exit(0)
}

func stdioFixture(t *testing.T, mode string) *StdioProcess {
	t.Helper()
	runner, workspace := confinedRunner(t)
	if mode == "tree" {
		// Isolate process-group supervision from bwrap's PID namespace and
		// die-with-parent behavior, which could mask a missing group signal.
		// Confinement is separately exercised by the other cases and MCP.
		runner.bwrapAvailable, runner.seatbeltAvailable = false, false
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "stdio-helper")
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := runner.NewStdioProcess(tools.CommandSpec{
		Argv: []string{path, "-test.run=^TestStdioProcessHelper$", "--", "--och-stdio-helper=" + mode}, Cwd: workspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func stdioReady(t *testing.T, p *StdioProcess) string {
	t.Helper()
	result := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(p).ReadString('\n'); result <- line }()
	select {
	case line := <-result:
		if !strings.HasPrefix(line, "ready") {
			t.Fatalf("helper did not start: %q", line)
		}
		return line
	case <-time.After(10 * time.Second):
		t.Fatal("helper readiness timed out")
	}
	return ""
}

func TestStdioProcessEOFReapsAndReleasesOnce(t *testing.T) {
	p := stdioFixture(t, "echo")
	ctx, cancel := context.WithCancel(t.Context())
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	stdioReady(t, p)
	if p.command.registered != p.command.cmd.Process.Pid {
		t.Fatal("quota enrollment was skipped")
	}
	if _, err := p.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 4)
	if _, err := io.ReadFull(p, data); err != nil || string(data) != "ping" {
		t.Fatalf("channel after startup cancellation: %q %v", data, err)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := p.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
	}
	group.Wait()
	assertStdioReleased(t, p)
	if err := p.Start(t.Context()); err == nil {
		t.Fatal("closed process restarted")
	}
}

func assertStdioReleased(t *testing.T, p *StdioProcess) {
	t.Helper()
	if p.waited != nil {
		select {
		case <-p.waited:
		default:
			t.Fatal("Close returned before Wait")
		}
		if !stdioGroupGone(p.command.cmd.Process.Pid, p.waited, 0) {
			t.Fatal("Close left process/group alive")
		}
	}
	if p.command.registered != 0 {
		t.Fatal("quota membership survived close")
	}
	if _, err := os.Stat(p.command.tempDir); !os.IsNotExist(err) {
		t.Fatalf("temporary directory survived close: %v", err)
	}
}

func TestStdioProcessClosesWholeTree(t *testing.T) {
	p := stdioFixture(t, "tree")
	if err := p.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	line := stdioReady(t, p)
	pid, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "ready ")))
	if err != nil {
		t.Fatal(err)
	}
	// Failure-only safety net (including source-overlay negative controls).
	// This runs AFTER the assertions: terminate the child so its parent can
	// reap it; never leave a deliberate mutation's live processes behind.
	t.Cleanup(func() {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
		_ = syscall.Kill(pid, syscall.SIGTERM)
		select {
		case <-p.waited:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-p.command.cmd.Process.Pid, syscall.SIGKILL)
		}
	})
	// The child is observed alive before shutdown; a missing fixture is a
	// failure, not a skipped assertion about an empty process group.
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("child was not alive: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		t.Fatal("grandchild survived process close")
	}
	assertStdioReleased(t, p)
}

func TestStdioProcessKillsStubbornLeader(t *testing.T) {
	p := stdioFixture(t, "stubborn")
	if err := p.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	stdioReady(t, p)
	start := time.Now()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > stdioEOFGrace+stdioTermGrace+stdioKillGrace+2*time.Second {
		t.Fatal("shutdown exceeded ladder budget")
	}
	assertStdioReleased(t, p)
}

func TestStdioProcessStartupFailuresReleaseResources(t *testing.T) {
	for _, mode := range []string{"cancelled", "missing", "closed"} {
		t.Run(mode, func(t *testing.T) {
			p := stdioFixture(t, "echo")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch mode {
			case "cancelled":
				cancel()
			case "missing":
				p.command.cmd.Path = filepath.Join(p.command.runner.workspace, "missing")
			case "closed":
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.Start(ctx); err == nil {
				t.Fatal("invalid startup succeeded")
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			assertStdioReleased(t, p)
		})
	}
}

func TestStdioProcessWaitDoesNotTruncateUnreadOutput(t *testing.T) {
	p := stdioFixture(t, "tail")
	if err := p.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(p)
	if err != nil || string(data) != strings.Repeat("x", 128<<10) {
		t.Fatalf("tail lost: %d bytes, %v", len(data), err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStdioReapProofRequiresWaitAndLeader(t *testing.T) {
	p := stdioFixture(t, "echo")
	if err := p.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	stdioReady(t, p)
	pid := p.command.cmd.Process.Pid
	claimedWait := make(chan struct{})
	close(claimedWait)
	if stdioGroupGone(pid, claimedWait, 0) {
		t.Fatal("live leader reported gone")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if stdioGroupGone(pid, make(chan struct{}), 0) {
		t.Fatal("missing Wait reported reaped")
	}
}

func TestStdioProcessCachesCleanupFailure(t *testing.T) {
	p := stdioFixture(t, "echo")
	blocker := filepath.Join(p.command.runner.workspace, "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	p.command.tempDir = filepath.Join(blocker, "child")
	first := p.Close()
	if first == nil {
		t.Fatal("invalid cleanup path reported success")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blocker, 0700); err != nil {
		t.Fatal(err)
	}
	if again := p.Close(); again != first {
		t.Fatal("repeat close erased earlier cleanup failure")
	}
}

func TestStdioReapProofRejectsMissingGroupWithLiveLeader(t *testing.T) {
	// Deliberately NOT a group leader. A missing -pid group must not be
	// mistaken for proof that this still-live process disappeared.
	cmd := exec.Command("/bin/sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	if !errors.Is(syscall.Kill(-cmd.Process.Pid, 0), syscall.ESRCH) {
		t.Fatal("fixture unexpectedly leads a group")
	}
	claimedWait := make(chan struct{})
	close(claimedWait)
	if stdioGroupGone(cmd.Process.Pid, claimedWait, 0) {
		t.Fatal("missing group hid a live leader")
	}
}
