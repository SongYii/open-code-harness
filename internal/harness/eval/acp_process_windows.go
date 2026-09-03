//go:build windows

package eval

import (
	"io"
	"time"
)

// acpProcessSupported is false on every build this file compiles for:
// design §16 explicitly does not implement ACP subprocess supervision on
// Windows (no process-group equivalent this package uses on Unix, and no
// parent-only termination substitute design accepts in its place).
// RunACPAttempt refuses before ever calling startACPProcess; matrix
// expansion refuses an acp_subprocess Cell before any Attempt is
// published (design §26) using the same capability-rejection path every
// other unsupported executor kind already uses.
const acpProcessSupported = false

// acpProcess mirrors acp_process_unix.go's type shape so acp_executor.go
// needs no build-tag branching of its own beyond calling
// startACPProcess; every method is unreachable in practice since
// startACPProcess always fails first.
type acpProcess struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func startACPProcess(binaryPath string, argv []string, env []string, workingDir string, stderrLimit int64) (*acpProcess, error) {
	return nil, errACPSubprocessUnsupportedOnWindows
}

func (p *acpProcess) pid() int    { return 0 }
func (p *acpProcess) wait() error { return errACPSubprocessUnsupportedOnWindows }
func (p *acpProcess) waitTimeout(time.Duration) (bool, error) {
	return false, errACPSubprocessUnsupportedOnWindows
}
func (p *acpProcess) isReaped() bool { return true }

// acpSignalTerm/acpSignalKill mirror acp_process_unix.go's own two names
// as inert placeholders — killProcessGroup here always refuses before
// either value could matter.
const (
	acpSignalTerm      = 15
	acpSignalKill      = 9
	acpSignalInterrupt = 2
)

func (p *acpProcess) killProcessGroup(signal int) error { return errACPSubprocessUnsupportedOnWindows }
