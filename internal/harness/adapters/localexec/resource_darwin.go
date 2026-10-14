//go:build darwin

package localexec

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// This is the 64-bit RUSAGE_INFO_V0 ABI from Apple's sys/resource.h.
// libproc's proc_pid_rusage calls SYS_PROC_INFO / PIDRUSAGE (9), while
// proc_listpgrppids calls LISTPIDS (1) / PROC_PGRP_ONLY (2). Using Go's
// Darwin syscall entry keeps CGO_ENABLED=0 builds usable and introduces no
// native helper, shell bootstrap or installation. A runtime probe gates use;
// a refused or incompatible ABI fails explicitly rather than disabling limits.
// Sources: apple-oss-distributions/xnu, bsd/sys/resource.h and
// libsyscall/wrappers/libproc/libproc.c.
type darwinRusage struct {
	uuid                                          [16]byte
	user, system, idle, interrupt, pageins, wired uint64
	resident, footprint, start, exit              uint64
}

func darwinProcessUsage(pid int) (processUsage, error) {
	timebase, err := readDarwinTimebase()
	if err != nil {
		return processUsage{}, err
	}
	var usage darwinRusage
	_, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, 9, uintptr(pid), 0, 0, uintptr(unsafe.Pointer(&usage)), 0)
	runtime.KeepAlive(&usage)
	if errno != 0 {
		return processUsage{}, errno
	}
	if usage.start == 0 {
		return processUsage{}, fmt.Errorf("invalid rusage start identity for pid %d", pid)
	}
	return processUsage{identity: processIdentity{pid: pid, start: usage.start}, cpu: ticksToNanos(saturatingAdd(usage.user, usage.system), timebase.numer, timebase.denom), resident: usage.resident, alive: usage.exit == 0}, nil
}

func darwinGroupPIDs(pgid int) ([]int, error) {
	for capacity := 32; capacity <= maxObservedProcesses; capacity *= 2 {
		pids := make([]int32, capacity)
		n, _, errno := syscall.Syscall6(syscall.SYS_PROC_INFO, 1, 2, uintptr(pgid), 0, uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)*4))
		runtime.KeepAlive(pids)
		if errno != 0 {
			return nil, errno
		}
		if n%4 != 0 || n > uintptr(len(pids)*4) {
			return nil, fmt.Errorf("invalid group sample length %d", n)
		}
		if n == uintptr(len(pids)*4) {
			continue
		}
		result := make([]int, 0, n/4)
		for _, raw := range pids[:n/4] {
			if raw > 0 {
				result = append(result, int(raw))
			}
		}
		return result, nil
	}
	return nil, fmt.Errorf("process group exceeds %d sampled members", maxObservedProcesses)
}

func darwinGroupUsage(pgid int) ([]processUsage, error) {
	pids, err := darwinGroupPIDs(pgid)
	if err != nil {
		return nil, err
	}
	usages := make([]processUsage, 0, len(pids))
	for _, pid := range pids {
		usage, err := darwinProcessUsage(pid)
		if processGoneError(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("pid %d rusage: %w", pid, err)
		}
		// Membership may change between list and rusage. Never charge a
		// reused/unrelated pid to this command's group.
		group, err := syscall.Getpgid(pid)
		if processGoneError(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if group == pgid {
			usages = append(usages, usage)
		}
	}
	return usages, nil
}

// The host's current group may include a CI supervisor with another UID.
// Construction verifies only our own statistics and that group enumeration
// works. It must not demand access to unowned peers. Started commands receive
// a separate Setpgid group; every member there is still sampled or refused.
func probeDarwinAccess(self, group int, read func(int) (processUsage, error), list func(int) ([]int, error)) error {
	if _, err := read(self); err != nil {
		return fmt.Errorf("self rusage: %w", err)
	}
	pids, err := list(group)
	if err != nil {
		return fmt.Errorf("group enumeration: %w", err)
	}
	for _, pid := range pids {
		if pid == self {
			return nil
		}
	}
	return fmt.Errorf("group enumeration did not include current process %d", self)
}

func platformResourceSampler(options Options) (groupSampler, resourceBudget, error) {
	cpu := options.CPUTimeLimit
	if cpu == 0 {
		if DefaultCPUSoftSeconds == 0 || DefaultCPUHardSeconds < DefaultCPUSoftSeconds || DefaultCPUSoftSeconds > uint64((1<<63-1)/int64(time.Second)) {
			return nil, resourceBudget{}, fmt.Errorf("localexec: invalid default CPU budget")
		}
		cpu = time.Duration(DefaultCPUSoftSeconds) * time.Second
	}
	if cpu < 0 {
		return nil, resourceBudget{}, fmt.Errorf("localexec: CPU time limit must be positive")
	}
	memory := options.MemoryLimitBytes
	if memory == 0 {
		memory = DefaultMemoryHighBytes + DefaultMemoryHeadroomBytes
	}
	if err := probeDarwinAccess(os.Getpid(), syscall.Getpgrp(), darwinProcessUsage, darwinGroupPIDs); err != nil {
		return nil, resourceBudget{}, fmt.Errorf("%w: Darwin sampler probe: %w", ErrResourceMonitoring, err)
	}
	return darwinGroupUsage, resourceBudget{cpu: uint64(cpu), resident: memory}, nil
}

// rusage CPU counters use Mach absolute ticks, including on Apple Silicon.
// Never treat them as nanoseconds: this Mac's ratio is 125/3. The Mach
// timebase trap is -89 on arm64. On amd64 Go's Syscall adds the Unix class
// 0x02000000, so its input is adjusted to obtain Mach class 0x01000059.
// The trap writes the ratio directly; BSD-style carry/errno return handling
// is not itself a portable Mach return-value convention. Validate the output.
// Source: Apple's osfmk/kern/syscall_sw.c and Go syscall/asm_darwin_*.s.
type machTimebase struct{ numer, denom uint32 }

var readDarwinTimebase = sync.OnceValues(func() (machTimebase, error) {
	var info machTimebase
	var trap uintptr
	switch runtime.GOARCH {
	case "arm64":
		trap = ^uintptr(88)
	case "amd64":
		trap = ^uintptr(0x00ffffa6)
	default:
		return info, fmt.Errorf("unsupported Darwin timebase architecture %s", runtime.GOARCH)
	}
	_, _, errno := syscall.Syscall(trap, uintptr(unsafe.Pointer(&info)), 0, 0)
	runtime.KeepAlive(&info)
	if errno != 0 || info.numer == 0 || info.denom == 0 {
		return info, fmt.Errorf("invalid Mach timebase %d/%d (errno %d)", info.numer, info.denom, errno)
	}
	return info, nil
})

func ticksToNanos(ticks uint64, numer, denom uint32) uint64 {
	hi, lo := bits.Mul64(ticks, uint64(numer))
	if hi >= uint64(denom) {
		return math.MaxUint64
	}
	value, _ := bits.Div64(hi, lo, uint64(denom))
	return value
}

func signalResourceGroup(pid int, leaderStart uint64, known map[processIdentity]bool, sig syscall.Signal) error {
	return signalVerifiedDarwinGroup(pid, leaderStart, known, sig, darwinGroupUsage, syscall.Kill)
}

func signalVerifiedDarwinGroup(pid int, leaderStart uint64, known map[processIdentity]bool, sig syscall.Signal, sample groupSampler, signal func(int, syscall.Signal) error) error {
	usages, err := sample(pid)
	if err != nil {
		return fmt.Errorf("%w: cannot verify signal target: %w", ErrResourceOwnership, err)
	}
	if len(usages) == 0 {
		return syscall.ESRCH
	}
	if !groupIdentityMatches(pid, leaderStart, known, usages) {
		return ErrResourceOwnership
	}
	live := false
	for _, usage := range usages {
		live = live || usage.alive
	}
	// Darwin returns EPERM when only exited members remain. They consume no
	// live resource and need no repeated signal; preserve permission errors
	// for every group that still has an active member.
	if !live {
		return syscall.ESRCH
	}
	err = signal(-pid, sig)
	if errors.Is(err, syscall.EPERM) {
		// A concurrent monitor/teardown signal can stop the final live member
		// after the sample above. Re-prove quiescence instead of reporting a
		// permission failure for an all-exited group. Active, inaccessible or
		// unrelated groups must still preserve the original permission error.
		current, readErr := sample(pid)
		if readErr == nil && len(current) == 0 {
			return syscall.ESRCH
		}
		if readErr == nil && groupIdentityMatches(pid, leaderStart, known, current) {
			active := false
			for _, usage := range current {
				active = active || usage.alive
			}
			if !active {
				return syscall.ESRCH
			}
		}
	}
	return err
}

const allowUnmonitoredGroupSignals = false

func platformProcessUsage(pid int) (processUsage, error) { return darwinProcessUsage(pid) }
func platformGroupUsage(pid int) ([]processUsage, error) { return darwinGroupUsage(pid) }
