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
// kernel-blocked. The child environment is empty except PATH (from the
// host), HOME (the workspace root), and TMPDIR (a workspace subdirectory
// removed after exit). Commands are argv-only.
package localexec
