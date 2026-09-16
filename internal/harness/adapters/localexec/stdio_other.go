//go:build !unix

package localexec

const stdioProcessSupported = false

func stopStdioProcess(int, <-chan struct{}) error { return ErrProcessTeardownUnproven }
