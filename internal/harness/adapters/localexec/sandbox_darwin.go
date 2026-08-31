//go:build darwin

package localexec

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// seatbeltExecutable is hardcoded to the system path rather than resolved
// via PATH lookup, mirroring Codex's own rationale (codex-rs/sandboxing/
// src/seatbelt.rs, MACOS_PATH_TO_SEATBELT_EXECUTABLE): a PATH-based lookup
// of the sandboxing mechanism itself could be hijacked by a malicious PATH
// entry, defeating confinement before it starts. A package-level var
// rather than a const solely so a same-package test can substitute a
// fake sandbox-exec without writing to /usr/bin; production code never
// changes it.
var seatbeltExecutable = "/usr/bin/sandbox-exec"

// codexBaseSeatbeltPolicy is Chrome-derived, production-hardened baseline
// process/signal/sysctl/IPC/PTY allowances every real process needs to
// even start under Seatbelt, reused verbatim (Apache-2.0) from
// openai/codex's codex-rs/sandboxing/src/seatbelt_base_policy.sbpl, pinned
// commit dde85b4 (docs/research/architecture-gates/
// 2026-08-30-exec-sandboxing-and-resource-quotas.md). This project's own
// design doc scopes only the filesystem/network layer below; inventing a
// minimal base policy from scratch, with no macOS host to test it on,
// would risk shipping a "sandbox" that cannot run any real command at
// all — this baseline is already proven in production.
const codexBaseSeatbeltPolicy = `(version 1)

; inspired by Chrome's sandbox policy:
; https://source.chromium.org/chromium/chromium/src/+/main:sandbox/policy/mac/common.sb;l=273-319;drc=7b3962fe2e5fc9e2ee58000dc8fbf3429d84d3bd
; https://source.chromium.org/chromium/chromium/src/+/main:sandbox/policy/mac/renderer.sb;l=64;drc=7b3962fe2e5fc9e2ee58000dc8fbf3429d84d3bd

; start with closed-by-default
(deny default)

; child processes inherit the policy of their parent
(allow process-exec)
(allow process-fork)
(allow signal (target same-sandbox))

; process-info
(allow process-info* (target same-sandbox))

(allow file-write-data
  (require-all
    (path "/dev/null")
    (vnode-type CHARACTER-DEVICE)))

; sysctls permitted.
(allow sysctl-read
  (sysctl-name "hw.activecpu")
  (sysctl-name "hw.busfrequency_compat")
  (sysctl-name "hw.byteorder")
  (sysctl-name "hw.cacheconfig")
  (sysctl-name "hw.cachelinesize_compat")
  (sysctl-name "hw.cpufamily")
  (sysctl-name "hw.cpufrequency_compat")
  (sysctl-name "hw.cputype")
  (sysctl-name "hw.l1dcachesize_compat")
  (sysctl-name "hw.l1icachesize_compat")
  (sysctl-name "hw.l2cachesize_compat")
  (sysctl-name "hw.l3cachesize_compat")
  (sysctl-name "hw.logicalcpu_max")
  (sysctl-name "hw.machine")
  (sysctl-name "hw.model")
  (sysctl-name "hw.memsize")
  (sysctl-name "hw.ncpu")
  (sysctl-name "hw.nperflevels")
  ; Chrome locks these CPU feature detection down a bit more tightly,
  ; but mostly for fingerprinting concerns which isn't an issue for codex.
  (sysctl-name-prefix "hw.optional.arm.")
  (sysctl-name-prefix "hw.optional.armv8_")
  (sysctl-name "hw.packages")
  (sysctl-name "hw.pagesize_compat")
  (sysctl-name "hw.pagesize")
  (sysctl-name "hw.physicalcpu")
  (sysctl-name "hw.physicalcpu_max")
  (sysctl-name "hw.logicalcpu")
  (sysctl-name "hw.cpufrequency")
  (sysctl-name "hw.tbfrequency_compat")
  (sysctl-name "hw.vectorunit")
  (sysctl-name "machdep.cpu.brand_string")
  (sysctl-name "kern.argmax")
  (sysctl-name "kern.hostname")
  (sysctl-name "kern.maxfilesperproc")
  (sysctl-name "kern.maxproc")
  (sysctl-name "kern.osproductversion")
  (sysctl-name "kern.osrelease")
  (sysctl-name "kern.ostype")
  (sysctl-name "kern.osvariant_status")
  (sysctl-name "kern.osversion")
  (sysctl-name "kern.secure_kernel")
  ; Python's ProcessPoolExecutor queries this through sysconf(_SC_SEM_NSEMS_MAX).
  (sysctl-name "kern.sysv.semmns")
  (sysctl-name "kern.usrstack64")
  (sysctl-name "kern.version")
  (sysctl-name "sysctl.proc_cputype")
  (sysctl-name "vm.loadavg")
  (sysctl-name-prefix "hw.perflevel")
  (sysctl-name-prefix "kern.proc.pgrp.")
  (sysctl-name-prefix "kern.proc.pid.")
  (sysctl-name-prefix "net.routetable.")
)

; Allow Java to read some CPU info. This is misclassified as a "write" because
; userspace passes a memory buffer to the sysctl, but conceptually it is a read.
(allow sysctl-write
  (sysctl-name "kern.grade_cputype"))

; IOKit
(allow iokit-open
  (iokit-registry-entry-class "RootDomainUserClient")
)

; needed to look up user info, see https://crbug.com/792228
(allow mach-lookup
  (global-name "com.apple.system.opendirectoryd.libinfo")
)

; Needed for python multiprocessing on MacOS for the SemLock
(allow ipc-posix-sem)

; Needed for PyTorch/libomp on macOS to register OpenMP runtimes.
(allow ipc-posix-shm-read-data
  ipc-posix-shm-write-create
  ipc-posix-shm-write-unlink
  (ipc-posix-name-regex #"^/__KMP_REGISTERED_LIB_[0-9]+$"))

(allow mach-lookup
  (global-name "com.apple.PowerManagement.control")
)

; allow openpty()
(allow pseudo-tty)
(allow file-read* file-write* file-ioctl (literal "/dev/ptmx"))
(allow file-read* file-write*
  (require-all
    (regex #"^/dev/ttys[0-9]+")
    (extension "com.apple.sandbox.pty")))
; PTYs created before entering seatbelt may lack the extension; allow ioctl
; on those slave ttys so interactive shells detect a TTY and remain functional.
(allow file-ioctl (regex #"^/dev/ttys[0-9]+"))
`

// seatbeltWorkspacePolicy is this project's own layer (design doc §4):
// deny all writes except the resolved workspace root (passed as the
// WORKSPACE_ROOT parameter, following Codex's and Maka's own "-D<PARAM>"
// pattern rather than inlining the path as a literal string, avoiding any
// need to escape quotes/backslashes in a workspace path), broad reads
// (matching Codex's own finding that restrictive reads are the fragile
// direction), and no network exception, matching §3.2's Linux decision.
const seatbeltWorkspacePolicy = `
(deny file-write*)
(allow file-write* (subpath (param "WORKSPACE_ROOT")))
(allow file-read*)
(deny network*)
`

const seatbeltPolicy = codexBaseSeatbeltPolicy + seatbeltWorkspacePolicy

// seatbeltProbePolicy is the minimal policy the availability probe runs
// under: enough to exec a trivial command, nothing else.
const seatbeltProbePolicy = "(version 1)\n(deny default)\n(allow process-exec)\n(allow process-fork)\n"

func probeSeatbelt() (available bool, reason string) {
	info, err := os.Stat(seatbeltExecutable)
	if err != nil || info.IsDir() {
		return false, "sandbox-exec not found at " + seatbeltExecutable
	}
	probe := exec.Command(seatbeltExecutable, "-p", seatbeltProbePolicy, "/usr/bin/true")
	if err := probe.Run(); err != nil {
		return false, "sandbox-exec probe failed: " + err.Error()
	}
	return true, ""
}

// seatbeltArgv builds the sandbox-exec argv (excluding the executable
// itself) that wraps target: the composed policy, the workspace root bound
// to the WORKSPACE_ROOT parameter the policy references, then target.
func seatbeltArgv(workspace string, target []string) []string {
	argv := []string{
		"-p", seatbeltPolicy,
		"-DWORKSPACE_ROOT=" + workspace,
	}
	return append(argv, target...)
}

// seatbeltCommandArgv returns the executable and full argv Run should
// exec to confine target under Seatbelt.
func seatbeltCommandArgv(workspace string, target []string) (name string, argv []string) {
	return seatbeltExecutable, seatbeltArgv(workspace, target)
}

// rlimitEnforcementLevel reports the enforcement level RLIMIT_AS provides
// for the Memory effect: always "partial" on Darwin once this file is
// built in, since it is applied unconditionally, independent of whether
// Seatbelt itself is available.
func rlimitEnforcementLevel() EnforcementLevel { return EnforcementPartial }

// cpuRlimitEnforcementLevel reports the enforcement level RLIMIT_CPU
// provides for the CPU effect: always "full" on Darwin once this file is
// built in (CPU quota design §1.5) — unlike RLIMIT_AS for memory, which
// bounds address space rather than resident memory and is rated only
// "partial", RLIMIT_CPU's own accounting (CPU time actually consumed) has
// no equivalent imprecision in what it bounds. Applied unconditionally,
// independent of whether Seatbelt itself is available.
func cpuRlimitEnforcementLevel() EnforcementLevel { return EnforcementFull }

// DefaultCPUSoftSeconds and DefaultCPUHardSeconds configure RLIMIT_CPU
// (Darwin only). DefaultCPUSoftSeconds is chosen to numerically match
// application.DefaultExecTimeout (30s): localexec is a lower-level
// adapter and internal/harness/architecture's own dependency-boundary
// rules forbid it from importing internal/harness/application, so this
// cannot be a compiler-enforced reference the way an import would be —
// if DefaultExecTimeout ever changes, this constant needs a matching,
// disclosed update; nothing links the two automatically. The 1-second
// gap to DefaultCPUHardSeconds exists for signal disambiguation, not
// grace (CPU quota design §4): the kernel sends SIGXCPU at the soft
// limit, then SIGKILL at the hard one if the process is still running,
// so a process terminated by SIGXCPU specifically is attributable to
// this quota with high confidence.
// These are vars, not consts, solely so a same-package test can lower them
// temporarily for a fast, real integration test instead of waiting out a
// full 30s soft limit; production code never changes them (the same
// reasoning seatbeltExecutable's own var-not-const already documents in
// this file).
var (
	DefaultCPUSoftSeconds uint64 = 30
	DefaultCPUHardSeconds uint64 = 31
)

// beginRlimitBracket lowers this process's own RLIMIT_AS and RLIMIT_CPU,
// holding mu for the duration, and returns a func that restores the
// original limits and releases mu. Both rlimits are inherited by a child
// at fork; Go's os/exec gives no hook to set either only in the child
// between fork and exec, so both are bracketed around the parent's own
// Start() call instead — the standard technique for this constraint,
// bounding the window during which the harness's own process (and any
// concurrent Run call's own Start) is briefly bound by the same lowered
// ceilings. The two rlimits fail independently: if reading either
// original limit fails, that one is left untouched while the other is
// still bracketed (CPU quota design §1.1/§3, applying the same
// independent-failure principle Task 1 established for the two cgroup
// controllers on Linux, to the two rlimits here).
//
// RLIMIT_AS is best-effort: it bounds virtual address space, not
// resident memory, and a breach surfaces as the child's own allocator
// getting ENOMEM, not a clean external kill (design doc §4) — it does
// not set ResourceLimited. RLIMIT_CPU is a real kill: the kernel
// terminates the process itself once its own accounted CPU time crosses
// the hard limit; Runner.Run's own signal-inspection code path (CPU
// quota design §4) is what turns that into ResourceLimited.
func beginRlimitBracket(mu sync.Locker, limitBytes uint64) func() {
	mu.Lock()
	var originalAS unix.Rlimit
	asOK := unix.Getrlimit(unix.RLIMIT_AS, &originalAS) == nil
	if asOK {
		lowered := unix.Rlimit{Cur: limitBytes, Max: originalAS.Max}
		if originalAS.Max != unix.RLIM_INFINITY && lowered.Cur > originalAS.Max {
			lowered.Cur = originalAS.Max
		}
		_ = unix.Setrlimit(unix.RLIMIT_AS, &lowered)
	}
	var originalCPU unix.Rlimit
	cpuOK := unix.Getrlimit(unix.RLIMIT_CPU, &originalCPU) == nil
	if cpuOK {
		lowered := unix.Rlimit{Cur: DefaultCPUSoftSeconds, Max: DefaultCPUHardSeconds}
		if originalCPU.Max != unix.RLIM_INFINITY && lowered.Max > originalCPU.Max {
			lowered.Max = originalCPU.Max
			if lowered.Cur > lowered.Max {
				lowered.Cur = lowered.Max
			}
		}
		_ = unix.Setrlimit(unix.RLIMIT_CPU, &lowered)
	}
	return func() {
		if asOK {
			_ = unix.Setrlimit(unix.RLIMIT_AS, &originalAS)
		}
		if cpuOK {
			_ = unix.Setrlimit(unix.RLIMIT_CPU, &originalCPU)
		}
		mu.Unlock()
	}
}

// isCPUResourceLimitExit reports whether waitErr's underlying exit status
// shows the process was terminated by SIGXCPU — the kernel's own
// soft-limit signal for RLIMIT_CPU, and, deliberately, the *only* signal
// this project treats as attributable to the CPU quota (CPU quota design
// §4). A bare SIGKILL is not treated as attributable on its own, even
// though it is what the kernel sends at the hard limit if a process
// somehow survives SIGXCPU: an unrelated external SIGKILL arriving in the
// same narrow window this function is checked in cannot be distinguished
// from the hard limit's own kill, and the overwhelming majority of real
// commands (which install no custom signal handling for SIGXCPU) already
// die from the soft limit's SIGXCPU well before the hard limit's SIGKILL
// would ever fire — this is a disclosed, accepted residual gap for the
// rare program that specifically catches or ignores SIGXCPU, not an
// oversight.
func isCPUResourceLimitExit(waitErr error) bool {
	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return false
	}
	return status.Signal() == syscall.SIGXCPU
}
