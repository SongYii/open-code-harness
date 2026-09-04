//go:build unix

package acp

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

// TestCancelableInputReleasesABlockedFileRead is the test whose absence let a
// wrong fix look correct. An io.Pipe releases a blocked read when it is
// closed, so a Serve test built on one passes even when the mechanism cannot
// release a real descriptor. Go leaves the standard descriptors in blocking
// mode and outside the runtime poller, so closing fd 0 does not interrupt a
// read already blocked in the kernel — this pins the file-backed path
// directly.
func TestCancelableInputReleasesABlockedFileRead(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	input := newCancelableInput(reader)
	t.Cleanup(func() { _ = input.Close() })

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, readErr := input.Read(buf)
		done <- readErr
	}()

	// Nothing is ever written, so the read above is genuinely blocked.
	input.Cancel()

	select {
	case readErr := <-done:
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			t.Fatalf("Read returned %v, want release", readErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Cancel did not release a read blocked on a real descriptor")
	}
}

// TestCancelableInputStillDeliversDataBeforeCancellation proves the wrapper
// did not turn every read into a cancellation: an ordinary frame must still
// arrive normally.
func TestCancelableInputStillDeliversDataBeforeCancellation(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	input := newCancelableInput(reader)
	t.Cleanup(func() { _ = input.Close() })

	if _, err := writer.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 5)
	n, err := input.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("Read = %q, want %q", string(buf[:n]), "hello")
	}
}

// TestCancelableInputCancelBeforeReadIsObserved covers the ordering a
// cancelled-before-start caller produces.
func TestCancelableInputCancelBeforeReadIsObserved(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	input := newCancelableInput(reader)
	t.Cleanup(func() { _ = input.Close() })

	input.Cancel()
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		_, _ = input.Read(buf)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a read entered after Cancel never returned")
	}
}
