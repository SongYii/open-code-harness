//go:build unix

package acp

import (
	"io"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// cancelableInput wraps Serve's input so a read that is blocked waiting for a
// frame can be released on demand.
//
// Closing the input is not sufficient on its own. Go leaves the standard
// descriptors in blocking mode and outside the runtime poller, so closing
// fd 0 does not interrupt a read already blocked in the kernel — verified
// directly, not assumed. Setting O_NONBLOCK on it would work, but that flag
// lives on the shared open file description, so a process could hand an
// unexpected EAGAIN to whoever else holds that description, including the
// shell that launched it. This wrapper therefore never touches the input's
// flags: it polls the descriptor alongside a private pipe, and cancellation
// writes to that pipe.
//
// A non-file input (an io.Pipe in tests, say) has no descriptor to poll, but
// it also has no such problem: closing an io.PipeReader really does release a
// blocked read. Those fall back to Close.
type cancelableInput struct {
	reader io.ReadCloser

	// pollFD is the input's descriptor when it has one, and -1 otherwise.
	pollFD int
	// wakeR/wakeW are the private cancellation pipe. Writing to wakeW makes
	// the poll below return without any frame having arrived.
	wakeR *os.File
	wakeW *os.File

	cancelOnce sync.Once
	closeOnce  sync.Once
}

// newCancelableInput prepares in for cancellable reading. It never fails in a
// way that should stop a server from starting: if the private pipe cannot be
// created, the input degrades to close-based cancellation, which is still
// correct for every non-file reader and no worse than before for a file.
func newCancelableInput(in io.ReadCloser) *cancelableInput {
	input := &cancelableInput{reader: in, pollFD: -1}
	file, ok := in.(*os.File)
	if !ok {
		return input
	}
	wakeR, wakeW, err := os.Pipe()
	if err != nil {
		return input
	}
	// Fd puts the file back into blocking mode and drops it from the runtime
	// poller, which is exactly what this wrapper wants: it does the readiness
	// waiting itself and issues the read only once data is already there.
	input.pollFD = int(file.Fd())
	input.wakeR = wakeR
	input.wakeW = wakeW
	return input
}

// Read waits until either the input has data or cancellation is requested. It
// issues the underlying read only when the descriptor is already readable, so
// this never introduces a blocking read of its own.
func (input *cancelableInput) Read(p []byte) (int, error) {
	if input.pollFD < 0 {
		return input.reader.Read(p)
	}
	for {
		fds := []unix.PollFd{
			{Fd: int32(input.pollFD), Events: unix.POLLIN},
			{Fd: int32(input.wakeR.Fd()), Events: unix.POLLIN},
		}
		if _, err := unix.Poll(fds, -1); err != nil {
			if err == unix.EINTR {
				continue
			}
			return 0, err
		}
		// Cancellation wins over a frame that arrived in the same wakeup: a
		// caller that asked to stop must not be handed one more request to
		// dispatch.
		if fds[1].Revents != 0 {
			return 0, io.EOF
		}
		if fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
			return input.reader.Read(p)
		}
	}
}

// Cancel releases a blocked Read. It is safe to call more than once and from
// any goroutine, including before Read is ever entered.
func (input *cancelableInput) Cancel() {
	input.cancelOnce.Do(func() {
		if input.wakeW != nil {
			_, _ = input.wakeW.Write([]byte{0})
			return
		}
		// No descriptor to poll: closing is what releases this reader.
		_ = input.reader.Close()
	})
}

// Close releases the wrapper's own pipe. It deliberately does not close the
// caller's input: Serve borrows stdin for the length of one call and closing
// a descriptor the process still owns is not this package's decision.
func (input *cancelableInput) Close() error {
	input.closeOnce.Do(func() {
		if input.wakeR != nil {
			_ = input.wakeR.Close()
		}
		if input.wakeW != nil {
			_ = input.wakeW.Close()
		}
	})
	return nil
}
