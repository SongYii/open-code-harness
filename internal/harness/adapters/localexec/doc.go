// Package localexec implements tools.CommandRunner via bounded os/exec.
//
// Runner.Enforcement reports, per effect (filesystem, network, memory), how
// completely commands are confined — a fact computed from what is actually
// active, never an assumed promise. Until a platform backend is wired in,
// every effect reports EnforcementNone: this is not Seatbelt, bwrap, or
// Landlock, and curl-from-exec is not kernel-blocked. The child environment
// is empty except PATH (from the host), HOME (the workspace root), and
// TMPDIR (a workspace subdirectory removed after exit). Commands are
// argv-only.
package localexec
