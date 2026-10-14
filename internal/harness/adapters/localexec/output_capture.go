package localexec

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

const commandOutputDrainGrace = 500 * time.Millisecond

// Own the pipe separately from Cmd.Wait: Go suppresses ErrWaitDelay when
// the leader exits nonzero. Exit status alone cannot prove complete output.
type commandOutput struct {
	reader, writer *os.File
	done           chan struct{}
	err            error
}

func captureCommandOutput(cmd *exec.Cmd, out io.Writer) (*commandOutput, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	capture := &commandOutput{reader: r, writer: w, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = w, w
	go func() { _, capture.err = io.Copy(out, r); close(capture.done) }()
	return capture, nil
}

// Keep Run's cancellation/deadline select responsive while the sole waiter
// collects the independently observed drain result, even for a nonzero exit.
func waitCommandOutput(cmd *exec.Cmd, capture *commandOutput) *commandWait {
	w := &commandWait{done: make(chan struct{})}
	go func() { waitErr := cmd.Wait(); w.err = errors.Join(waitErr, capture.drain()); close(w.done) }()
	return w
}

func (capture *commandOutput) close() {
	_ = capture.writer.Close()
	_ = capture.reader.Close()
	<-capture.done
}

func (capture *commandOutput) drain() error {
	timer := time.NewTimer(commandOutputDrainGrace)
	defer timer.Stop()
	select {
	case <-capture.done:
		if capture.err != nil {
			return errors.Join(tools.ErrCommandOutputIncomplete, capture.err)
		}
		return nil
	case <-timer.C:
		select {
		case <-capture.done:
			if capture.err != nil {
				return errors.Join(tools.ErrCommandOutputIncomplete, capture.err)
			}
			return nil
		default:
		}
		_ = capture.reader.Close()
		<-capture.done
		return errors.Join(tools.ErrCommandOutputIncomplete, exec.ErrWaitDelay)
	}
}
