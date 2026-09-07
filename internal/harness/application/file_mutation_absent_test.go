package application_test

import (
	"context"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// TestObservedAbsentThenExternallyCreatedSaysReRead closes a gap in the
// observation tests rather than adding a new behaviour.
//
// Three situations produce a refused create, and two of them were already
// covered: a session that never looked at the target, and a session that read
// the file and then lost a race. The third is a session that DID look, found
// nothing there, and then lost a race to an external creator. It is the one
// that is easy to fold into the first, because both end at the adapter as
// "you asked to create and something exists".
//
// Folding them is wrong, and wrong in the exact way Task 3 already corrected
// once in the other direction. "Read the file before changing it" is an
// instruction to do something this session has already done; it did read, and
// what it read was that nothing was there. The honest answer is that the file
// changed since it looked, which is what sends it to re-read rather than to
// repeat a read it remembers making.
func TestObservedAbsentThenExternallyCreatedSaysReRead(t *testing.T) {
	fs := testkit.NewMemFS("/workspace")

	readMissing := engine.StreamEvent{Type: engine.StreamEventToolCall, ToolCall: &engine.ToolCall{
		ID: "call-read", Name: tools.NameReadFile, Arguments: `{"path":"shared.txt"}`,
	}}
	model := newSequenceModel(
		step(readMissing), turnDone,
		step(writeCallEvent("agent")), turnDone,
	)
	service, _ := newToolService(t, model, fs, nil, nil, allowWrites())
	created, err := service.CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: "/workspace"})
	if err != nil {
		t.Fatal(err)
	}

	// Turn 1 reads a file that is not there. The read fails and the session
	// records the target as observed-absent.
	runTurns(t, service, created.SessionID, "read-missing")

	// Something outside this process creates it.
	fs.AddFile("shared.txt", []byte("external"))

	// Turn 2's write is correctly refused either way. What this pins is which
	// refusal the model is told, because that decides what it does next.
	runTurns(t, service, created.SessionID, "write")

	got := lastToolMessage(model.Calls()[3].Messages).Text
	if got == application.ToolTextFSNotObserved {
		t.Fatalf("text = %q; this session did read the target and observed it absent, so an instruction to read it is one it has already followed", got)
	}
	if got != application.ToolTextFSStaleVersion {
		t.Fatalf("text = %q, want %q", got, application.ToolTextFSStaleVersion)
	}

	// The refused write must also have left the external file alone.
	read, readErr := fs.Read(context.Background(), "/workspace/shared.txt", 64)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(read.Data) != "external" {
		t.Fatalf("content = %q; the refused write overwrote the external creation", read.Data)
	}
}
