//go:build !unix

package localexec

import (
	"os/exec"
	"time"
)

const stdioProcessSupported = false

func stopStdioProcess(*exec.Cmd, <-chan struct{}, *resourceMonitor, time.Duration) error {
	return ErrProcessTeardownUnproven
}
