//go:build !unix

package localexec

const stdioProcessSupported = false

func stopStdioProcess(int, <-chan struct{}, *resourceMonitor) error {
	return ErrProcessTeardownUnproven
}
