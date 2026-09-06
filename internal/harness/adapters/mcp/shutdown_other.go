//go:build !unix

package mcp

import (
	"errors"
	"os/exec"
	"time"
)

// Shutdown grades, defined here too so the platform-neutral code that names
// them compiles everywhere. Only GracefulGrace is consulted on this path.
const (
	GracefulGrace  = 10 * time.Second
	GroupTermGrace = 3 * time.Second
	GroupKillGrace = 5 * time.Second
)

// ErrTeardownUnproven exists on every platform so a caller can name it, but
// this path never returns it: without process-group signalling there is no
// escalation whose outcome could be left unproven.
var ErrTeardownUnproven = errors.New("mcp: server process group teardown could not be proven")

// shutdownProcess runs the SDK's own stdio shutdown and stops there.
//
// The unix build escalates past it to the process group and proves the group
// is gone. Neither is available here: process groups and the signals that
// address them are a POSIX concept, and this repository does not claim
// support for supervising subprocesses on Windows — the ACP subprocess
// executor already refuses outright on that platform for the same reason
// (`acpProcessSupported = false`), rather than approximating a
// kill-only-the-parent substitute.
//
// So a server that spawns children of its own can leave them running here.
// That limitation is stated rather than hidden, and it is the same one the
// SDK's own ladder has on every platform.
func shutdownProcess(command *exec.Cmd, closeSession func() error) error {
	if closeSession == nil {
		return nil
	}
	return closeSession()
}
