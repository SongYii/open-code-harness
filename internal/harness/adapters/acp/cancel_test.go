package acp

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// TestServeReturnsWhenContextIsCancelledWhileIdle is the regression this
// package was missing. Serve decodes frames through a blocking read; once an
// initialized agent goes idle with no further frames coming, that read never
// returns, so cancelling the Serve context alone never unblocked it. A real
// `och -acp` process therefore ignored SIGINT indefinitely while idle.
//
// The deadline here is generous and channel-driven: the point is that Serve
// returns at all, not how fast. No sleep is used to create the idle state —
// the agent is idle precisely because nothing writes another frame.
func TestServeReturnsWhenContextIsCancelledWhileIdle(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	fake := newFake()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = agentOut.Close()
	})

	// Initialize so the agent is a real, live server rather than one that
	// happens to return before it ever read anything.
	writeLine(t, clientOut, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`)
	if init := readJSON(t, clientIn); init["id"] != float64(1) {
		t.Fatalf("initialize id = %v", init["id"])
	}

	// The agent is now blocked reading the next frame. Nothing will send one.
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve returned %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled while idle")
	}
}

// TestServeReturnsContextErrorRatherThanAClosedInputError pins the second
// half of the contract: unblocking the read by closing the input must not
// leak that mechanism to the caller as an incidental "file already closed"
// error. The caller asked for cancellation and must observe cancellation.
func TestServeReturnsContextErrorRatherThanAClosedInputError(t *testing.T) {
	agentIn, clientOut := io.Pipe()
	_, agentOut := io.Pipe()
	fake := newFake()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Config{Sessions: fake, History: fake, Workspace: "/workspace"}, agentIn, agentOut)
	}()
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = agentOut.Close()
	})

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Serve returned %v, want context.Canceled", err)
		}
		if err != nil && errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("Serve leaked its own unblocking mechanism to the caller: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after cancellation")
	}
}
