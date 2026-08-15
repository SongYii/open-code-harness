// Package localexec implements tools.CommandRunner via bounded os/exec.
//
// Enforcement is partial: this is not Seatbelt, bwrap, or Landlock.
// curl-from-exec is not kernel-blocked. The child environment is empty
// except PATH (from the host), HOME (the workspace root), and TMPDIR
// (a workspace subdirectory removed after exit). Commands are argv-only.
package localexec
