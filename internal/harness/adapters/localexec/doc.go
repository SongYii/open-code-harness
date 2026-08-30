// Package localexec implements tools.CommandRunner via bounded os/exec.
//
// Runner.Enforcement reports, per effect (filesystem, network, memory), how
// completely commands are confined — a fact computed from what is actually
// active, never an assumed promise. On Linux, when bwrap is on PATH and a
// scoped probe invocation succeeds, every Run call is wrapped in a bwrap
// sandbox (unshared user/pid/ipc/uts/cgroup/net namespaces, every
// capability dropped, a read-only view of the host with only the
// workspace root rebound read-write), and Enforcement reports Filesystem
// and Network as full. Off Linux, or when bwrap is missing, on WSL1, or
// its probe fails, Run behaves exactly as it does with no platform
// backend: every effect reports EnforcementNone, and curl-from-exec is not
// kernel-blocked. On Linux, when the calling process's own cgroup is on
// the v2 unified hierarchy and can delegate the memory controller to a
// child, one cgroup is created for the Runner's lifetime with memory.high
// and memory.max (high plus headroom); each command's process is moved
// into it, and a background monitor kills the process group — reporting
// CommandResult.ResourceLimited instead of TimedOut — if usage is still
// above 90% of memory.high after the kernel's high counter fires, ahead of
// the kernel's own hard OOM kill at memory.max. When that delegation isn't
// available (a common shape for an interactive shell or session scope
// that was never set up for it), the quota is skipped and Enforcement
// reports Memory as none; this never fails Runner construction. Call
// Close to release a Runner's quota and stop its monitor goroutine when
// done with it. The child environment is empty except PATH (from the
// host), HOME (the workspace root), and TMPDIR (a workspace subdirectory
// removed after exit). Commands are argv-only.
package localexec
