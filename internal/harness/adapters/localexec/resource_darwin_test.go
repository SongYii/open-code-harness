//go:build darwin

package localexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
	"golang.org/x/sys/unix"
)

// Real executable workloads. No fake signal, Wait or CPU/memory usage supplies
// the integration evidence. Each helper has an independent finite deadline.
func TestDarwinResourceHelper(t *testing.T) {
	mode := ""
	for _, arg := range os.Args {
		if value, ok := strings.CutPrefix(arg, "--och-resource-helper="); ok {
			mode = value
		}
	}
	if mode == "" {
		return
	}
	switch mode {
	case "sleep":
		fmt.Println("ready")
		time.Sleep(12 * time.Second)
	case "cpu", "ignore-cpu":
		if mode == "ignore-cpu" {
			signal.Ignore(syscall.SIGXCPU)
			if err := unix.Setrlimit(unix.RLIMIT_CPU, &unix.Rlimit{Cur: 1, Max: 2}); err != nil {
				os.Exit(3)
			}
		}
		fmt.Println("ready")
		deadline := time.Now().Add(12 * time.Second)
		var value uint64
		for time.Now().Before(deadline) {
			for i := 0; i < 10000; i++ {
				value = value*1664525 + 1013904223
			}
		}
		fmt.Println(value)
		os.Exit(42)
	case "memory", "short-memory":
		count := 64
		if mode == "short-memory" {
			count = 24
		}
		fmt.Println("ready")
		blocks := make([][]byte, 0, 64)
		for range count {
			block := make([]byte, 4<<20)
			for i := 0; i < len(block); i += 4096 {
				block[i] = 1
			}
			blocks = append(blocks, block)
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(8 * time.Second)
		runtime.KeepAlive(blocks)
		os.Exit(42)
	case "short-cpu":
		// Each worker finishes below the group's budget. Only aggregation of
		// multiple workers can terminate this finite workload as resource-limited.
		var usage unix.Rusage
		for {
			if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
				os.Exit(3)
			}
			used := time.Duration(usage.Utime.Sec+usage.Stime.Sec)*time.Second + time.Duration(usage.Utime.Usec+usage.Stime.Usec)*time.Microsecond
			if used >= 350*time.Millisecond {
				break
			}
			for i := 0; i < 10000; i++ {
				runtime.KeepAlive(i)
			}
		}
	case "lifecycle-tree", "lifecycle-overflow", "lifecycle-resource":
		binary, _ := os.Executable()
		var children []*exec.Cmd
		for range 2 {
			child := exec.Command(binary, "-test.run=^TestDarwinResourceHelper$", "--", "--och-resource-helper=sleep")
			if err := child.Start(); err != nil {
				os.Exit(3)
			}
			children = append(children, child)
		}
		if err := os.WriteFile("lifecycle.pids", []byte(fmt.Sprintf("%d %d", children[0].Process.Pid, children[1].Process.Pid)), 0600); err != nil {
			os.Exit(3)
		}
		if mode == "lifecycle-overflow" {
			fmt.Print(strings.Repeat("x", 128<<10))
		}
		if mode == "lifecycle-resource" {
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) {
				for i := 0; i < 10000; i++ {
					runtime.KeepAlive(i)
				}
			}
		}
		for _, child := range children {
			_ = child.Wait()
		}
	case "tree-cpu", "tree-memory":
		worker := "short-" + strings.TrimPrefix(mode, "tree-")
		binary, _ := os.Executable()
		var children []*exec.Cmd
		for range 2 {
			cmd := exec.Command(binary, "-test.run=^TestDarwinResourceHelper$", "--", "--och-resource-helper="+worker)
			cmd.Stdout = os.Stdout
			if err := cmd.Start(); err != nil {
				os.Exit(3)
			}
			children = append(children, cmd)
		}
		fmt.Printf("workers %d %d\n", children[0].Process.Pid, children[1].Process.Pid)
		for _, child := range children {
			_ = child.Wait()
		}
	case "exit-child":
		binary, _ := os.Executable()
		child := exec.Command(binary, "-test.run=^TestDarwinResourceHelper$", "--", "--och-resource-helper=cpu")
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		if err := os.WriteFile("remaining.pid", []byte(fmt.Sprint(child.Process.Pid)), 0600); err != nil {
			os.Exit(3)
		}
		// Let monitoring observe the group while the leader still owns it.
		time.Sleep(200 * time.Millisecond)
	case "environment":
		fmt.Println(strings.Join(os.Environ(), "\n"))
	case "limits":
		var cpu, as unix.Rlimit
		if unix.Getrlimit(unix.RLIMIT_CPU, &cpu) != nil || unix.Getrlimit(unix.RLIMIT_AS, &as) != nil {
			os.Exit(3)
		}
		fmt.Printf("%d/%d %d/%d\n", cpu.Cur, cpu.Max, as.Cur, as.Max)
	case "stdio":
		fmt.Println("ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	os.Exit(0)
}

func darwinResourceFixture(t *testing.T, options Options, mode string) (*Runner, tools.CommandSpec) {
	t.Helper()
	root := t.TempDir()
	runner, err := NewWithOptions(root, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Close() })
	// Resource tests isolate the monitor from Seatbelt; confinement has its
	// own integration suite. Preserve the actual process-group and argv path.
	runner.seatbeltAvailable = false
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "resource-helper")
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	return runner, tools.CommandSpec{Argv: []string{path, "-test.run=^TestDarwinResourceHelper$", "--", "--och-resource-helper=" + mode}, Cwd: root, Timeout: 10 * time.Second}
}

func TestDarwinMonitorReportsPartialAndRejectsStrictRequests(t *testing.T) {
	runner, _ := darwinResourceFixture(t, Options{}, "environment")
	got := runner.Enforcement()
	if got.CPU != EnforcementPartial || got.Memory != EnforcementPartial {
		t.Fatalf("monitor overstated: %+v", got)
	}
	for _, requirement := range []Enforcement{{CPU: EnforcementFull}, {Memory: EnforcementFull}} {
		if r, err := NewWithOptions(t.TempDir(), Options{RequiredEnforcement: requirement}); !errors.Is(err, ErrEnforcementUnavailable) {
			if r != nil {
				_ = r.Close()
			}
			t.Fatalf("strict request accepted: %+v %v", requirement, err)
		}
	}
}

func TestDarwinMonitorKillsIgnoringSIGXCPUBeyondKernelHardValue(t *testing.T) {
	runner, spec := darwinResourceFixture(t, Options{CPUTimeLimit: 2500 * time.Millisecond}, "ignore-cpu")
	started := time.Now()
	got, err := runner.Run(t.Context(), spec)
	if err != nil || !got.ResourceLimited || got.TimedOut || got.ExitCode == 42 || !strings.Contains(got.Output, "ready") {
		t.Fatalf("ignored SIGXCPU escaped monitor: %#v, %v", got, err)
	}
	if time.Since(started) > 8*time.Second {
		t.Fatalf("monitor exceeded safety deadline: %v", time.Since(started))
	}
}

func TestDarwinMonitorKillsResidentMemoryGrowth(t *testing.T) {
	runner, spec := darwinResourceFixture(t, Options{MemoryLimitBytes: 128 << 20}, "memory")
	got, err := runner.Run(t.Context(), spec)
	if err != nil || !got.ResourceLimited || got.TimedOut || got.ExitCode == 42 || !strings.Contains(got.Output, "ready") {
		t.Fatalf("resident growth escaped monitor: %#v, %v", got, err)
	}
}

func TestDarwinMonitorAccountsCPUAndMemoryAcrossGroupWorkers(t *testing.T) {
	for _, test := range []struct {
		mode    string
		options Options
	}{
		{"tree-cpu", Options{CPUTimeLimit: 500 * time.Millisecond}},
		{"tree-memory", Options{MemoryLimitBytes: 180 << 20}},
	} {
		t.Run(test.mode, func(t *testing.T) {
			runner, spec := darwinResourceFixture(t, test.options, test.mode)
			native := runner.resourceSampler
			var maxCPU, maxRSS atomic.Uint64
			runner.resourceSampler = func(pid int) ([]processUsage, error) {
				usages, err := native(pid)
				for _, u := range usages {
					for old := maxCPU.Load(); u.cpu > old && !maxCPU.CompareAndSwap(old, u.cpu); old = maxCPU.Load() {
					}
					for old := maxRSS.Load(); u.resident > old && !maxRSS.CompareAndSwap(old, u.resident); old = maxRSS.Load() {
					}
				}
				return usages, err
			}
			got, err := runner.Run(t.Context(), spec)
			if err != nil || !got.ResourceLimited || got.TimedOut || !strings.Contains(got.Output, "workers ") {
				t.Fatalf("group workers escaped monitor: %#v %v", got, err)
			}
			if test.options.CPUTimeLimit > 0 && maxCPU.Load() >= uint64(test.options.CPUTimeLimit) {
				t.Fatalf("CPU test did not isolate aggregation: individual=%d budget=%d", maxCPU.Load(), test.options.CPUTimeLimit)
			}
			if test.options.MemoryLimitBytes > 0 && maxRSS.Load() >= test.options.MemoryLimitBytes {
				t.Fatalf("memory test did not isolate aggregation: individual=%d budget=%d", maxRSS.Load(), test.options.MemoryLimitBytes)
			}
			t.Logf("max individual CPU=%dns RSS=%dbytes; group exceeded configured budget", maxCPU.Load(), maxRSS.Load())
			var first, second int
			for _, line := range strings.Split(got.Output, "\n") {
				if strings.HasPrefix(line, "workers ") {
					_, _ = fmt.Sscanf(line, "workers %d %d", &first, &second)
				}
			}
			for _, pid := range []int{first, second} {
				if pid <= 0 {
					t.Fatalf("missing child PID: %q", got.Output)
				}
				assertDarwinProcessInactive(t, pid)
			}
		})
	}
}

func assertDarwinProcessInactive(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		usage, err := darwinProcessUsage(pid)
		if processGoneError(err) || (err == nil && !usage.alive) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("worker %d remained alive after group termination", pid)
}

func TestDarwinMonitorSamplingFailureKillsAndReportsError(t *testing.T) {
	for _, failAt := range []int32{1, 2} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			runner, spec := darwinResourceFixture(t, Options{}, "cpu")
			native := runner.resourceSampler
			var calls atomic.Int32
			runner.resourceSampler = func(pid int) ([]processUsage, error) {
				if calls.Add(1) == failAt {
					return nil, syscall.EPERM
				}
				return native(pid)
			}
			got, err := runner.Run(t.Context(), spec)
			if !errors.Is(err, ErrResourceMonitoring) || got.TimedOut || got.ResourceLimited {
				t.Fatalf("failed sampling was silent or misattributed: %#v %v", got, err)
			}
		})
	}
}

func TestDarwinMonitorSharedStdioPathStopsRunaway(t *testing.T) {
	runner, spec := darwinResourceFixture(t, Options{CPUTimeLimit: 500 * time.Millisecond}, "cpu")
	p, err := runner.NewStdioProcess(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.waited:
	case <-time.After(8 * time.Second):
		t.Fatal("stdio process escaped resource monitor")
	}
	limited, err := p.command.monitor.status()
	if !limited || err != nil {
		t.Fatalf("stdio termination was not resource-attributed: %v %v", limited, err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDarwinMonitorConcurrentRunnersKeepParentAndChildLimits(t *testing.T) {
	var cpu, as unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_CPU, &cpu) != nil || unix.Getrlimit(unix.RLIMIT_AS, &as) != nil {
		t.Fatal("read parent limits")
	}
	want := fmt.Sprintf("%d/%d %d/%d", cpu.Cur, cpu.Max, as.Cur, as.Max)
	var group sync.WaitGroup
	for range 3 {
		runner, spec := darwinResourceFixture(t, Options{}, "limits")
		for range 3 {
			group.Add(1)
			go func() {
				defer group.Done()
				got, err := runner.Run(context.Background(), spec)
				if err != nil || got.ExitCode != 0 || strings.TrimSpace(got.Output) != want {
					t.Errorf("inherited limits changed: %#v %v want %s", got, err, want)
				}
			}()
		}
	}
	group.Wait()
	var afterCPU, afterAS unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_CPU, &afterCPU) != nil || unix.Getrlimit(unix.RLIMIT_AS, &afterAS) != nil {
		t.Fatal("read parent limits after concurrency")
	}
	if afterCPU != cpu || afterAS != as {
		t.Fatalf("concurrent Runners polluted parent: %+v/%+v want %+v/%+v", afterCPU, afterAS, cpu, as)
	}
}

func TestDarwinMonitorPreservesActualChildEnvironment(t *testing.T) {
	t.Setenv("OCH_RESOURCE_SECRET", "must-not-leak")
	runner, spec := darwinResourceFixture(t, Options{}, "environment")
	got, err := runner.Run(t.Context(), spec)
	if err != nil || got.ExitCode != 0 {
		t.Fatalf("environment probe: %#v %v", got, err)
	}
	lines := strings.Split(strings.TrimSpace(got.Output), "\n")
	if len(lines) != 3 {
		t.Fatalf("actual exec environment changed: %q", got.Output)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "PATH=") && !strings.HasPrefix(line, "HOME=") && !strings.HasPrefix(line, "TMPDIR=") {
			t.Fatalf("unexpected actual env: %q", line)
		}
	}
}

func TestDarwinTimebaseConversionUsesHostRatioAndSaturates(t *testing.T) {
	info, err := readDarwinTimebase()
	if err != nil || info.numer == 0 || info.denom == 0 {
		t.Fatalf("host timebase: %+v %v", info, err)
	}
	if got := ticksToNanos(24_000_000, 125, 3); got != uint64(time.Second) {
		t.Fatalf("Apple Silicon second = %d", got)
	}
	if got := ticksToNanos(math.MaxUint64, 125, 3); got != math.MaxUint64 {
		t.Fatalf("overflow = %d", got)
	}
	if got := ticksToNanos(17, 1, 1); got != 17 {
		t.Fatalf("Intel identity ratio = %d", got)
	}
}

func TestDarwinSignalGuardRefusesStaleLeaderIdentity(t *testing.T) {
	runner, spec := darwinResourceFixture(t, Options{}, "stdio")
	command, err := runner.NewConfinedCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer command.Close()
	cmd := command.Cmd()
	// Keep stdin open so this actual child survives the negative signal check.
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() }()
	usage, err := darwinProcessUsage(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	known := map[processIdentity]bool{usage.identity: true}
	if err := signalResourceGroup(cmd.Process.Pid, usage.identity.start+1, known, syscall.SIGKILL); !errors.Is(err, ErrResourceOwnership) {
		t.Fatalf("stale generation was signalled: %v", err)
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatalf("negative control killed the live command: %v", err)
	}
}

func TestDarwinPreparedStartEnrollsBeforeReturning(t *testing.T) {
	runner, spec := darwinResourceFixture(t, Options{CPUTimeLimit: 500 * time.Millisecond}, "cpu")
	command, err := runner.NewConfinedCommand(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer command.Close()
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if command.monitor == nil || command.registered != command.Cmd().Process.Pid {
		t.Fatal("prepared Start returned without monitoring")
	}
	done := make(chan error, 1)
	go func() { done <- command.Cmd().Wait() }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		_ = syscall.Kill(-command.Cmd().Process.Pid, syscall.SIGKILL)
		<-done
		t.Fatal("prepared command escaped monitor")
	}
	limited, err := command.monitor.status()
	if !limited || err != nil {
		t.Fatalf("prepared resource attribution: %v %v", limited, err)
	}
}

func TestDarwinStdioStartReportsRegistrationFailure(t *testing.T) {
	runner, spec := darwinResourceFixture(t, Options{}, "cpu")
	runner.resourceSampler = func(int) ([]processUsage, error) { return nil, syscall.EPERM }
	p, err := runner.NewStdioProcess(spec)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Start(t.Context()); !errors.Is(err, ErrResourceMonitoring) {
		t.Fatalf("stdio hid registration failure: %v", err)
	}
	if p.command.Cmd().ProcessState == nil {
		t.Fatal("failed stdio startup did not reap its process")
	}
	assertDarwinProcessInactive(t, p.command.Cmd().Process.Pid)
}

func TestDarwinRunCleansRemainingOwnedGroupAfterLeaderExit(t *testing.T) {
	runner, spec := darwinResourceFixture(t, Options{}, "exit-child")
	got, err := runner.Run(t.Context(), spec)
	if err != nil || got.ExitCode != 0 || got.TimedOut || got.ResourceLimited {
		t.Fatalf("normal leader exit changed or teardown unproven: %#v %v", got, err)
	}
	data, err := os.ReadFile(filepath.Join(spec.Cwd, "remaining.pid"))
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Sscan(string(data), &pid); err != nil || pid <= 0 {
		t.Fatalf("missing child identity: %q %v", data, err)
	}
	assertDarwinProcessInactive(t, pid)
}

func TestDarwinConstructorProbeDoesNotReadUnownedGroupPeers(t *testing.T) {
	const self, otherUID, group = 41, 42, 40
	var reads []int
	read := func(pid int) (processUsage, error) {
		reads = append(reads, pid)
		if pid == otherUID {
			return processUsage{}, syscall.EPERM
		}
		return processUsage{identity: processIdentity{pid: pid, start: 1}}, nil
	}
	list := func(gid int) ([]int, error) {
		if gid != group {
			t.Fatalf("wrong probe group %d", gid)
		}
		return []int{self, otherUID}, nil
	}
	if err := probeDarwinAccess(self, group, read, list); err != nil {
		t.Fatalf("unowned supervisor broke constructor: %v", err)
	}
	if len(reads) != 1 || reads[0] != self {
		t.Fatalf("constructor inspected unowned peers: %v", reads)
	}
	if err := probeDarwinAccess(self, group, read, func(int) ([]int, error) { return []int{otherUID}, nil }); err == nil {
		t.Fatal("incomplete group enumeration was accepted")
	}
	if err := probeDarwinAccess(self, group, func(int) (processUsage, error) { return processUsage{}, syscall.EPERM }, list); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("self access failure was ignored: %v", err)
	}
}
