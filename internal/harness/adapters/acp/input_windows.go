//go:build windows

package acp

import (
	"io"
	"sync"
)

// cancelableInput is the Windows counterpart. Releasing a blocked console
// read there needs overlapping I/O against CONIN$, which this project does
// not implement: the accepted design keeps Windows on cross-build and does
// not run the ACP subprocess executor on it. Cancellation therefore falls
// back to closing the input, which is correct for every pipe-backed reader
// and honest about what it cannot do for a console.
type cancelableInput struct {
	reader     io.ReadCloser
	cancelOnce sync.Once
}

func newCancelableInput(in io.ReadCloser) *cancelableInput {
	return &cancelableInput{reader: in}
}

func (input *cancelableInput) Read(p []byte) (int, error) { return input.reader.Read(p) }

func (input *cancelableInput) Cancel() {
	input.cancelOnce.Do(func() { _ = input.reader.Close() })
}

func (input *cancelableInput) Close() error { return nil }
