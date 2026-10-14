package localexec

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"syscall"
	"time"
)

// Options augments New without changing its platform-specific defaults.
type Options struct {
	// RequiredEnforcement refuses construction unless every requested effect
	// has at least this level. In particular full CPU/memory requests on
	// Darwin fail: sampling and killing is not a kernel hard boundary.
	RequiredEnforcement Enforcement
	// CPUTimeLimit is an aggregate sampled CPU-time budget, not cpu.max
	// bandwidth. MemoryLimitBytes bounds sampled aggregate resident bytes.
	// Overrides currently require Darwin's monitor; other platforms refuse
	// them rather than substitute a different resource contract.
	CPUTimeLimit     time.Duration
	MemoryLimitBytes uint64
}

var (
	ErrEnforcementUnavailable = errors.New("localexec: required enforcement unavailable")
	ErrResourceMonitoring     = errors.New("localexec: resource monitoring failed")
	ErrResourceOwnership      = errors.New("localexec: original process-group identity unavailable")
)

const resourceSampleInterval = 25 * time.Millisecond
const maxObservedProcesses = 16384

type processIdentity struct {
	pid   int
	start uint64
}

type processUsage struct {
	identity processIdentity
	cpu      uint64 // nanoseconds of user + system time
	resident uint64
	alive    bool
}

type groupSampler func(int) ([]processUsage, error)

type resourceBudget struct {
	cpu      uint64
	resident uint64
}

// usageLedger retains each observed process's CPU high-water mark after it
// exits. A workload cannot reset the sampled budget by replacing a worker.
// PID + kernel start time distinguishes reuse; exec preserves that identity.
// Unobserved short-lived workers and escaped process groups remain a disclosed
// sampling gap, so this mechanism is always EnforcementPartial.
type usageLedger map[processIdentity]uint64

func (ledger usageLedger) sample(usages []processUsage) (cpu, resident uint64, err error) {
	for _, usage := range usages {
		ledger[usage.identity] = max(ledger[usage.identity], usage.cpu)
		if usage.alive {
			resident = saturatingAdd(resident, usage.resident)
		}
	}
	if len(ledger) > maxObservedProcesses {
		return 0, 0, fmt.Errorf("%w: process accounting capacity exceeded", ErrResourceMonitoring)
	}
	for _, value := range ledger {
		cpu = saturatingAdd(cpu, value)
	}
	return cpu, resident, nil
}

func saturatingAdd(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}

type resourceMonitor struct {
	trigger      chan struct{}
	stop         chan struct{}
	done         chan struct{}
	once         sync.Once
	mu           sync.Mutex
	limited      bool
	err          error
	pid          int
	leaderStart  uint64
	known        map[processIdentity]bool
	retired      bool
	signaler     func(int, syscall.Signal) error
	process      *os.Process
	leaderKiller func(*os.Process) error
}

func (runner *Runner) startResourceMonitor(process *os.Process) (*resourceMonitor, error) {
	pid := process.Pid
	if runner.resourceSampler == nil {
		return nil, nil
	}
	m := &resourceMonitor{trigger: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{}), pid: pid, known: make(map[processIdentity]bool), signaler: runner.groupSignal, process: process, leaderKiller: runner.leaderKill}
	initial, err := runner.resourceSampler(pid)
	if err != nil {
		// Retain ownership state even on initial sampling failure. In particular
		// prepared Close must never fall back to an unchecked Darwin group PID.
		if leader, readErr := platformProcessUsage(pid); readErr == nil {
			m.leaderStart = leader.identity.start
			m.known[leader.identity] = true
		}
		m.err = fmt.Errorf("%w: initial process-group sample: %w", ErrResourceMonitoring, err)
		close(m.trigger)
		close(m.done)
		return m, m.err
	}
	if len(initial) == 0 {
		// A dead leader is insufficient: another member may still own the
		// original group. Confirm the whole group through the native control
		// reader before permanently retiring its identity.
		native, readErr := platformGroupUsage(pid)
		if readErr == nil {
			live := false
			for _, u := range native {
				m.known[u.identity] = true
				if u.identity.pid == pid {
					m.leaderStart = u.identity.start
				}
				live = live || u.alive
			}
			if !live {
				m.retired = true
				close(m.done)
				return m, nil
			}
		}
		m.err = fmt.Errorf("%w: initial sample omitted a live or unverified group: %v", ErrResourceMonitoring, readErr)
		close(m.trigger)
		close(m.done)
		return m, m.err
	}
	for _, usage := range initial {
		m.known[usage.identity] = true
		if usage.identity.pid == pid {
			m.leaderStart = usage.identity.start
		}
	}
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(resourceSampleInterval)
		defer ticker.Stop()
		ledger := make(usageLedger)
		usages := initial
		for {
			live := false
			for _, usage := range usages {
				live = live || usage.alive
			}
			if !live {
				quiet, controlErr := m.groupQuiescent()
				if controlErr != nil {
					m.mu.Lock()
					m.err = errors.Join(ErrResourceMonitoring, controlErr)
					m.mu.Unlock()
					_ = m.killOwnedGroup()
					close(m.trigger)
					return
				}
				if quiet {
					return
				}
				// A fork/exit can straddle the sample. Refresh from the control reader
				// instead of claiming a live descendant disappeared with its leader.
				usages, controlErr = platformGroupUsage(pid)
				if controlErr != nil {
					m.mu.Lock()
					m.err = errors.Join(ErrResourceMonitoring, controlErr)
					m.mu.Unlock()
					_ = m.killOwnedGroup()
					close(m.trigger)
					return
				}
				continue
			}
			m.mu.Lock()
			owned := groupIdentityMatches(pid, m.leaderStart, m.known, usages)
			if owned {
				for _, usage := range usages {
					m.known[usage.identity] = true
				}
			}
			m.mu.Unlock()
			if !owned {
				m.mu.Lock()
				m.err = errors.Join(ErrResourceMonitoring, ErrResourceOwnership)
				m.mu.Unlock()
				_ = m.killOwnedGroup()
				close(m.trigger)
				return
			}
			cpu, resident, sampleErr := ledger.sample(usages)
			limited := cpu >= runner.resourceBudget.cpu || resident >= runner.resourceBudget.resident
			if sampleErr != nil || limited {
				m.mu.Lock()
				m.limited, m.err = limited, sampleErr
				m.mu.Unlock()
				// The monitor also supervises prepared/stdio commands whose
				// owner does not select on trigger. SIGKILL cannot be ignored.
				if killErr := m.killOwnedGroup(); killErr != nil && !processGoneError(killErr) {
					m.mu.Lock()
					m.err = errors.Join(m.err, fmt.Errorf("%w: kill group: %w", ErrResourceMonitoring, killErr))
					m.mu.Unlock()
				}
				close(m.trigger)
				return
			}
			select {
			case <-m.stop:
				return
			case <-ticker.C:
			}
			usages, sampleErr = runner.resourceSampler(pid)
			if sampleErr != nil {
				m.mu.Lock()
				m.err = fmt.Errorf("%w: process-group sample: %w", ErrResourceMonitoring, sampleErr)
				m.mu.Unlock()
				if killErr := m.killOwnedGroup(); killErr != nil && !processGoneError(killErr) {
					m.mu.Lock()
					m.err = errors.Join(m.err, killErr)
					m.mu.Unlock()
				}
				close(m.trigger)
				return
			}

		}
	}()
	return m, nil
}

func (m *resourceMonitor) notification() <-chan struct{} {
	if m == nil {
		return nil
	}
	return m.trigger
}

func (m *resourceMonitor) status() (bool, error) {
	if m == nil {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.limited, m.err
}

func (m *resourceMonitor) close() {
	if m == nil {
		return
	}
	m.once.Do(func() { close(m.stop) })
	<-m.done
}

func requireEnforcement(actual, required Enforcement) error {
	for _, effect := range []struct {
		name             string
		actual, required EnforcementLevel
	}{
		{"filesystem", actual.Filesystem, required.Filesystem},
		{"network", actual.Network, required.Network},
		{"memory", actual.Memory, required.Memory},
		{"CPU", actual.CPU, required.CPU},
	} {
		rank := func(level EnforcementLevel) int {
			switch level {
			case "", EnforcementNone:
				return 0
			case EnforcementPartial:
				return 1
			case EnforcementFull:
				return 2
			default:
				return -1
			}
		}
		if rank(effect.required) < 0 {
			return fmt.Errorf("localexec: invalid required %s enforcement %q", effect.name, effect.required)
		}
		if rank(effect.actual) < rank(effect.required) {
			return fmt.Errorf("%w: %s requires %s, available %s", ErrEnforcementUnavailable, effect.name, effect.required, effect.actual)
		}
	}
	return nil
}

func processGoneError(err error) bool { return errors.Is(err, syscall.ESRCH) }

// A reused leader must never become a member of the original command merely
// because its numeric PID/PGID is the same. If the leader is gone, require
// continuity with at least one already observed member; unknown continuity
// fails explicitly, without signalling an unproven group.
func groupIdentityMatches(pid int, leaderStart uint64, known map[processIdentity]bool, usages []processUsage) bool {
	if len(usages) == 0 {
		return true
	}
	for _, usage := range usages {
		if usage.identity.pid == pid {
			return leaderStart != 0 && usage.identity.start == leaderStart
		}
	}
	for _, usage := range usages {
		if known[usage.identity] {
			return true
		}
	}
	return false
}

func (m *resourceMonitor) signal(sig syscall.Signal) error {
	m.mu.Lock()
	if m.retired {
		m.mu.Unlock()
		return syscall.ESRCH
	}
	signaler := m.signaler
	known := make(map[processIdentity]bool, len(m.known))
	for identity := range m.known {
		known[identity] = true
	}
	pid, start := m.pid, m.leaderStart
	m.mu.Unlock()
	if signaler != nil {
		return signaler(pid, sig)
	}
	return signalResourceGroup(pid, start, known, sig)
}

// Complete the invocation only after remaining owned group members have
// stopped. This also covers descendants that closed inherited stdio before
// their leader exited. A stale/unproven group is an error, never a blind kill.
func (m *resourceMonitor) cleanupGroup(_ groupSampler) error {
	if m == nil {
		return nil
	}
	if err := m.signal(syscall.SIGKILL); err != nil && !processGoneError(err) {
		return errors.Join(ErrProcessTeardownUnproven, err)
	}
	return waitForCommandGroup(m.pid, m, time.Second)
}

func (m *resourceMonitor) groupQuiescent() (bool, error) {
	m.mu.Lock()
	if m.retired {
		m.mu.Unlock()
		return true, nil
	}
	known := make(map[processIdentity]bool, len(m.known))
	for identity := range m.known {
		known[identity] = true
	}
	pid, start := m.pid, m.leaderStart
	m.mu.Unlock()
	usages, err := platformGroupUsage(pid)
	if err != nil {
		return false, err
	}
	if !groupIdentityMatches(pid, start, known, usages) {
		return false, ErrResourceOwnership
	}
	m.mu.Lock()
	for _, u := range usages {
		m.known[u.identity] = true
	}
	m.mu.Unlock()
	for _, u := range usages {
		if u.alive {
			return false, nil
		}
	}
	m.mu.Lock()
	m.retired = true
	m.mu.Unlock()
	return true, nil
}

func (m *resourceMonitor) killOwnedGroup() error {
	err := m.signal(syscall.SIGKILL)
	if err != nil && !processGoneError(err) {
		var leaderErr error
		if m.leaderKiller != nil {
			leaderErr = m.leaderKiller(m.process)
		} else {
			leaderErr = m.process.Kill()
		}
		if leaderErr != nil && !errors.Is(leaderErr, os.ErrProcessDone) {
			err = errors.Join(err, leaderErr)
		}
		m.mu.Lock()
		m.err = errors.Join(m.err, ErrResourceMonitoring, err)
		m.mu.Unlock()
	}
	return err
}
