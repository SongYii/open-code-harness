//go:build darwin

package localexec

import (
	"os"
	"os/exec"
	"sync"

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

// beginRlimitBracket lowers this process's own RLIMIT_AS to limitBytes,
// holding mu for the duration, and returns a func that restores the
// original limit and releases mu. RLIMIT_AS is inherited by a child at
// fork; Go's os/exec gives no hook to set it only in the child between
// fork and exec, so the limit is bracketed around the parent's own
// Start() call instead — the standard technique for this constraint,
// bounding the window during which the harness's own process (and any
// concurrent Run call's own Start) is briefly bound by the same lowered
// ceiling. Best-effort: RLIMIT_AS bounds virtual address space, not
// resident memory, and a breach surfaces as the child's own allocator
// getting ENOMEM, not a clean external kill (design doc §4) — it does not
// set ResourceLimited.
func beginRlimitBracket(mu sync.Locker, limitBytes uint64) func() {
	mu.Lock()
	var original unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_AS, &original); err != nil {
		mu.Unlock()
		return func() {}
	}
	lowered := unix.Rlimit{Cur: limitBytes, Max: original.Max}
	if original.Max != unix.RLIM_INFINITY && lowered.Cur > original.Max {
		lowered.Cur = original.Max
	}
	_ = unix.Setrlimit(unix.RLIMIT_AS, &lowered)
	return func() {
		_ = unix.Setrlimit(unix.RLIMIT_AS, &original)
		mu.Unlock()
	}
}
