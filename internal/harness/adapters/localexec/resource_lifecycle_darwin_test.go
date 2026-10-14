//go:build darwin

package localexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestDarwinStdioRuntimeMonitorFailureStopsTransport(t *testing.T) {
	p := stdioFixture(t, "echo")
	runner := p.command.runner
	native := runner.resourceSampler
	release := make(chan struct{})
	var calls atomic.Int32
	runner.resourceSampler = func(pid int) ([]processUsage, error) {
		if calls.Add(1) == 1 {
			return native(pid)
		}
		<-release
		return nil, syscall.EPERM
	}
	if err := p.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	stdioReady(t, p)
	// Simulate an ownership verification failure as well as sample failure:
	// the group signal must be refused, leaving the transport to fail closed.
	m := p.command.monitor
	m.mu.Lock()
	m.leaderStart++
	m.mu.Unlock()
	close(release)
	select {
	case <-p.command.ResourceFailure():
	case <-time.After(time.Second):
		t.Fatal("monitor did not deliver failure")
	}
	<-m.done
	if err := p.command.ResourceError(); !errors.Is(err, ErrResourceMonitoring) {
		t.Fatalf("fault missing: %v", err)
	}
	n, err := p.Write([]byte("after-fault\n"))
	if err == nil {
		line, readErr := bufio.NewReader(p).ReadString('\n')
		t.Fatalf("stopped monitor still served transport: write=%d/%v read=%q/%v", n, err, line, readErr)
	}
	if !errors.Is(err, ErrResourceMonitoring) {
		t.Fatalf("runtime fault not surfaced to transport: %v", err)
	}
	if _, err := p.Read(make([]byte, 1)); !errors.Is(err, ErrResourceMonitoring) {
		t.Fatalf("Read did not preserve monitor failure: %v", err)
	}
}

func TestDarwinQuickExitDelayedCloseDoesNotSignalReusedGroup(t *testing.T) {
	runner, root := confinedRunner(t)
	runner.seatbeltAvailable = false
	runner.resourceSampler = func(pid int) ([]processUsage, error) {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			u, err := darwinProcessUsage(pid)
			if processGoneError(err) || (err == nil && !u.alive) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			time.Sleep(time.Millisecond)
		}
		return nil, fmt.Errorf("quick command did not exit")
	}
	p, err := runner.NewStdioProcess(tools.CommandSpec{Argv: []string{"true"}, Cwd: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-p.waited
	// An unrelated, self-owned live group stands in for a reused numeric
	// PID/PGID. Rebinding the stale handle makes any blind fallback observable.
	victim := exec.Command("/bin/sleep", "30")
	victim.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := victim.Start(); err != nil {
		t.Fatal(err)
	}
	victimDone := make(chan error, 1)
	go func() { victimDone <- victim.Wait() }()
	defer func() {
		_ = victim.Process.Kill()
		select {
		case <-victimDone:
		case <-time.After(time.Second):
		}
	}()
	p.command.cmd.Process = victim.Process
	closeDone := make(chan error, 1)
	go func() { closeDone <- p.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("retired group did not close cleanly: %v", err)
		}
		select {
		case err := <-victimDone:
			t.Fatalf("delayed Close killed reused group: %v", err)
		default:
		}
	case err := <-victimDone:
		t.Fatalf("delayed Close signalled unrelated reused group: %v", err)
	case <-time.After(7 * time.Second):
		t.Fatal("Close did not recognize the already terminated original group")
	}
}

func TestDarwinStartupSamplingFailureHasBoundedWaitAndKillErrors(t *testing.T) {
	for _, mode := range []string{"run", "prepared", "stdio"} {
		t.Run(mode, func(t *testing.T) {
			runner, spec := darwinResourceFixture(t, Options{}, "cpu")
			runner.stopGrace = 80 * time.Millisecond
			runner.resourceSampler = func(int) ([]processUsage, error) { return nil, syscall.EPERM }
			var ownedPID atomic.Int64
			runner.groupSignal = func(pid int, _ syscall.Signal) error { ownedPID.Store(int64(pid)); return syscall.EPERM }
			runner.leaderKill = func(*os.Process) error { return syscall.EPERM }
			started := time.Now()
			var err error
			var waiter *commandWait
			switch mode {
			case "run":
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
				defer cancel()
				_, err = runner.Run(ctx, spec)
			case "prepared":
				c, makeErr := runner.NewConfinedCommand(spec)
				if makeErr != nil {
					t.Fatal(makeErr)
				}
				defer c.Close()
				err = c.Start()
				waiter = c.startupWait
			case "stdio":
				p, makeErr := runner.NewStdioProcess(spec)
				if makeErr != nil {
					t.Fatal(makeErr)
				}
				defer p.Close()
				err = p.Start(t.Context())
				waiter = p.command.startupWait
			}
			// Restore actual signal capability only for our exact fixture group.
			pid := int(ownedPID.Load())
			if pid > 0 {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
			if waiter != nil {
				select {
				case <-waiter.done:
				case <-time.After(time.Second):
					t.Fatal("fixture did not reap")
				}
			}
			if elapsed := time.Since(started); elapsed > 800*time.Millisecond {
				t.Fatalf("startup wait exceeded bounded grace: %v", elapsed)
			}
			if !errors.Is(err, ErrResourceMonitoring) || !errors.Is(err, ErrProcessTeardownUnproven) || !errors.Is(err, syscall.EPERM) {
				t.Fatalf("startup errors lost: %v", err)
			}
		})
	}
}

func TestDarwinTerminationCannotClaimSuccessWhileGroupDescendantsLive(t *testing.T) {
	for _, reason := range []string{"cancel", "timeout", "output", "resource"} {
		t.Run(reason, func(t *testing.T) {
			mode := "lifecycle-tree"
			options := Options{}
			if reason == "output" {
				mode = "lifecycle-overflow"
			}
			if reason == "resource" {
				mode = "lifecycle-resource"
				options.CPUTimeLimit = 350 * time.Millisecond
			}
			runner, spec := darwinResourceFixture(t, options, mode)
			runner.stopGrace = 80 * time.Millisecond
			var rootPID atomic.Int64
			// Deliberately emulate a faulty group operation that kills only the
			// leader. A successful signal return is not descendant teardown proof.
			runner.groupSignal = func(pid int, sig syscall.Signal) error { rootPID.Store(int64(pid)); return syscall.Kill(pid, sig) }
			if reason == "timeout" {
				spec.Timeout = 2 * time.Second
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := runner.Run(ctx, spec); result <- err }()
			path := filepath.Join(spec.Cwd, "lifecycle.pids")
			var data []byte
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				data, _ = os.ReadFile(path)
				if len(data) > 0 {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if len(data) == 0 {
				t.Fatal("tree did not become ready")
			}
			if reason == "cancel" {
				cancel()
			}
			var err error
			select {
			case err = <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("termination did not return within bound")
			}
			var first, second int
			if _, scanErr := fmt.Sscan(string(data), &first, &second); scanErr != nil {
				t.Fatal(scanErr)
			}
			live := 0
			for _, pid := range []int{first, second} {
				u, e := darwinProcessUsage(pid)
				if e == nil && u.alive {
					live++
				}
			}
			// Kill and verify only this test's known remaining workers.
			if pid := int(rootPID.Load()); pid > 0 {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
			for _, pid := range []int{first, second} {
				assertDarwinProcessInactive(t, pid)
			}
			if live == 0 {
				t.Fatal("negative control did not leave live descendants")
			}
			if !errors.Is(err, ErrProcessTeardownUnproven) {
				t.Fatalf("leader-only termination incorrectly succeeded for %s: %v", reason, err)
			}
			if reason == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation reason lost: %v", err)
			}
		})
	}
}

func TestDarwinEmptyInitialSampleCannotRetireLiveDescendants(t *testing.T) {
	runner, spec := darwinResourceFixture(t, Options{}, "exit-child")
	var leaderPID atomic.Int64
	runner.resourceSampler = func(pid int) ([]processUsage, error) {
		leaderPID.Store(int64(pid))
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			u, err := darwinProcessUsage(pid)
			if err == nil && !u.alive {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			time.Sleep(time.Millisecond)
		}
		return nil, fmt.Errorf("leader did not exit")
	}
	_, err := runner.Run(t.Context(), spec)
	data, readErr := os.ReadFile(filepath.Join(spec.Cwd, "remaining.pid"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	var worker int
	if _, scanErr := fmt.Sscan(string(data), &worker); scanErr != nil {
		t.Fatal(scanErr)
	}
	u, usageErr := darwinProcessUsage(worker)
	// The negative control's survivor remains self-owned and is reclaimed.
	if pid := int(leaderPID.Load()); pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	assertDarwinProcessInactive(t, worker)
	if err == nil {
		t.Fatalf("empty sample silently retired live descendants (alive=%v usage_error=%v)", u.alive, usageErr)
	}
	if !errors.Is(err, ErrResourceMonitoring) {
		t.Fatalf("omitted live group was not an explicit sampler error: %v", err)
	}
}
