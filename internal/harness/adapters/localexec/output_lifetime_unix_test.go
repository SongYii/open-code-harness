//go:build unix

package localexec

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestPipeDrainHelper(t *testing.T) {
	mode, marker := "", ""
	exit := 0
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "--och-pipe-exit=") {
			exit, _ = strconv.Atoi(strings.TrimPrefix(arg, "--och-pipe-exit="))
		}
		if strings.HasPrefix(arg, "--och-pipe-holder=") {
			mode, marker = "holder", strings.TrimPrefix(arg, "--och-pipe-holder=")
		}
		if strings.HasPrefix(arg, "--och-pipe-leader=") {
			mode, marker = "leader", strings.TrimPrefix(arg, "--och-pipe-leader=")
		}
	}
	if mode == "" {
		return
	}
	if mode == "holder" {
		time.Sleep(15 * time.Second)
		os.Exit(0)
	}
	if err := os.WriteFile(marker+".leader", []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(binary, "-test.run=^TestPipeDrainHelper$", "--", "--och-pipe-holder="+marker)
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker+".pid", []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			_, _ = os.Stdout.WriteString("partial")
			os.Exit(exit)
		}
		time.Sleep(time.Millisecond)
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	t.Fatal("sampler handshake never arrived")
}

// A successful leader exit does not close pipes held by its descendant. The
// native process/pipe fixture has an independent finite lifetime and cleanup.
func TestRunCannotReportOrdinaryResultAfterPipeDrainDeadline(t *testing.T) {
	for _, exit := range []int{0, 7} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) { testRunPipeDrainDeadline(t, exit, false) })
	}
}

func TestRunCancellationWhileOnlyDescendantHoldsOutput(t *testing.T) {
	testRunPipeDrainDeadline(t, 7, true)
}

func testRunPipeDrainDeadline(t *testing.T, exit int, cancelDuringDrain bool) {
	root := t.TempDir()
	runner, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close() })
	// Isolate lifetime semantics. The required backend lane covers confinement
	// separately, including its namespace PID semantics.
	runner.bwrapAvailable, runner.seatbeltAvailable = false, false
	marker := filepath.Join(root, "registered")
	if runner.resourceSampler != nil {
		sampler := runner.resourceSampler
		runner.resourceSampler = func(pid int) ([]processUsage, error) {
			usages, err := sampler(pid)
			// The initial sample must enroll the leader. Later samples wait for the
			// descendant before allowing the leader to exit.
			if _, pidErr := os.Stat(marker + ".pid"); err == nil && pidErr == nil {
				_ = os.WriteFile(marker, nil, 0600)
			}
			return usages, err
		}
	} else if err := os.WriteFile(marker, nil, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "pipe-fixture")
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.OpenFile(binary, os.O_CREATE|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(output, input)
	if err := errors.Join(copyErr, output.Close()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		data, _ := os.ReadFile(marker + ".pid")
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid <= 0 {
			return
		}
		// Check the unique helper identity before cleaning up its known PID.
		args, _ := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
		if strings.Contains(string(args), binary) && strings.Contains(string(args), "--och-pipe-holder="+marker) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	started := time.Now()
	if cancelDuringDrain {
		done := make(chan error, 1)
		go func() {
			_, err := runner.Run(ctx, tools.CommandSpec{Argv: []string{binary, "-test.run=^TestPipeDrainHelper$", "--", "--och-pipe-leader=" + marker, "--och-pipe-exit=7"}, Cwd: root})
			done <- err
		}()
		deadline := time.Now().Add(4 * time.Second)
		exited := false
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(marker + ".leader")
			pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			if pid > 0 && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				exited = true
				break
			}
			time.Sleep(time.Millisecond)
		}
		if !exited {
			t.Fatal("fixture leader was not reaped before cancellation")
		}
		cancel()
		err = <-done
		if !errors.Is(err, context.Canceled) || errors.Is(err, tools.ErrCommandOutputIncomplete) {
			t.Fatalf("cancel during drain lost its reason: %v", err)
		}
		return
	}
	result, err := runner.Run(ctx, tools.CommandSpec{Argv: []string{binary, "-test.run=^TestPipeDrainHelper$", "--", "--och-pipe-leader=" + marker, "--och-pipe-exit=" + strconv.Itoa(exit)}, Cwd: root})
	if _, statErr := os.Stat(marker + ".pid"); statErr != nil {
		t.Fatalf("fixture did not start: %v", statErr)
	}
	if !errors.Is(err, exec.ErrWaitDelay) || !errors.Is(err, tools.ErrCommandOutputIncomplete) {
		t.Fatalf("result=%+v error=%v; pipe-drain failure was presented as an ordinary command result", result, err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("unbounded output drain: %s", time.Since(started))
	}
}
