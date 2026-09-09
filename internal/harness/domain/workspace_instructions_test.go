package domain

import (
	"reflect"
	"testing"
	"time"
)

const testSHA256 = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func validWorkspaceInstructionsRecorded() WorkspaceInstructionsRecorded {
	return WorkspaceInstructionsRecorded{
		FormatVersion: WorkspaceInstructionsFormatV1,
		PromptID:      "och_coding_agent_v1",
		PromptDigest:  testSHA256,
		Epoch:         1,
		Discovered: []InstructionScope{
			{Path: "AGENTS.md", Scope: "."},
		},
		Changes: []InstructionChange{
			{
				Action:  InstructionActionSet,
				Path:    "AGENTS.md",
				Scope:   ".",
				Digest:  testSHA256,
				Content: "Run focused tests.\n",
			},
		},
		RenderedMessage:    "<workspace_instruction_change>set</workspace_instruction_change>",
		EffectiveSetDigest: testSHA256,
	}
}

func TestRecordWorkspaceInstructionsAcceptsLiveIdleAndRunningSessions(t *testing.T) {
	now := time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC)
	idle := Session{ID: "session-1", Status: SessionStatusActive, Version: 1, WorkspaceRoot: "/workspace"}
	running := idle.Clone()
	running.ActiveTurn = &Turn{ID: "turn-1", Input: "inspect", StartedAt: now, LastTransitionAt: now}
	closed := idle.Clone()
	closed.Status = SessionStatusClosed
	deleted := idle.Clone()
	deleted.Status = SessionStatusDeleted

	for _, test := range []struct {
		name  string
		state Session
		code  ErrorCode
	}{
		{name: "idle", state: idle},
		{name: "running", state: running},
		{name: "closed", state: closed, code: CodeSessionClosed},
		{name: "deleted", state: deleted, code: CodeInvalidCommand},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload := validWorkspaceInstructionsRecorded()
			events, err := Decide(test.state, RecordWorkspaceInstructions{
				SessionID:                     test.state.ID,
				WorkspaceInstructionsRecorded: payload,
			})
			if test.code != "" {
				if events != nil || !IsCode(err, test.code) {
					t.Fatalf("Decide() = (%#v, %v), want code %q", events, err, test.code)
				}
				return
			}
			want := []UncommittedEvent{{Event: payload}}
			if err != nil || !reflect.DeepEqual(events, want) {
				t.Fatalf("Decide() = (%#v, %v), want (%#v, nil)", events, err, want)
			}
		})
	}
}

func TestWorkspaceInstructionsValidationRejectsUnsafeOrAmbiguousTransitions(t *testing.T) {
	valid := validWorkspaceInstructionsRecorded()
	tests := []struct {
		name   string
		mutate func(*WorkspaceInstructionsRecorded)
	}{
		{name: "empty batch", mutate: func(v *WorkspaceInstructionsRecorded) { v.Discovered = nil; v.Changes = nil; v.Diagnostics = nil }},
		{name: "absolute path", mutate: func(v *WorkspaceInstructionsRecorded) { v.Changes[0].Path = "/AGENTS.md" }},
		{name: "backtracking path", mutate: func(v *WorkspaceInstructionsRecorded) { v.Changes[0].Path = "src/../AGENTS.md" }},
		{name: "wrong filename", mutate: func(v *WorkspaceInstructionsRecorded) { v.Changes[0].Path = "agents.md" }},
		{name: "invalid action", mutate: func(v *WorkspaceInstructionsRecorded) { v.Changes[0].Action = "update" }},
		{name: "invalid digest", mutate: func(v *WorkspaceInstructionsRecorded) { v.Changes[0].Digest = "sha256:nope" }},
		{name: "set has prior digest", mutate: func(v *WorkspaceInstructionsRecorded) { v.Changes[0].PriorDigest = testSHA256 }},
		{name: "remove has content", mutate: func(v *WorkspaceInstructionsRecorded) { v.Changes[0].Action = InstructionActionRemove }},
		{name: "model change lacks rendered message", mutate: func(v *WorkspaceInstructionsRecorded) { v.RenderedMessage = "" }},
		{name: "invalid UTF-8 content", mutate: func(v *WorkspaceInstructionsRecorded) { v.Changes[0].Content = string([]byte{0xff}) }},
		{name: "duplicate discovered path", mutate: func(v *WorkspaceInstructionsRecorded) { v.Discovered = append(v.Discovered, v.Discovered[0]) }},
		{name: "unsorted discovered paths", mutate: func(v *WorkspaceInstructionsRecorded) {
			v.Discovered = []InstructionScope{{Path: "z/AGENTS.md", Scope: "z"}, {Path: "a/AGENTS.md", Scope: "a"}}
		}},
	}

	state := Session{ID: "session-1", Status: SessionStatusActive, Version: 1, WorkspaceRoot: "/workspace"}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := cloneWorkspaceInstructionsRecorded(valid)
			test.mutate(&payload)
			events, err := Decide(state, RecordWorkspaceInstructions{SessionID: state.ID, WorkspaceInstructionsRecorded: payload})
			if events != nil || !IsCode(err, CodeInvalidCommand) {
				t.Fatalf("Decide() = (%#v, %v), want %q", events, err, CodeInvalidCommand)
			}
		})
	}
}

func TestWorkspaceInstructionsApplyAdvancesOnlyBoundedSessionMetadata(t *testing.T) {
	state := Session{ID: "session-1", Status: SessionStatusActive, Version: 1, WorkspaceRoot: "/workspace"}
	record := RecordedEvent{
		SchemaVersion: 1,
		ID:            "event-2",
		CommandID:     "command-2",
		SessionID:     state.ID,
		Sequence:      2,
		OccurredAt:    time.Date(2026, 9, 8, 1, 0, 0, 0, time.UTC),
		Event:         validWorkspaceInstructionsRecorded(),
	}
	got, err := Apply(state, record)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	want := state
	want.Version = 2
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Apply() = %#v, want %#v", got, want)
	}
}

func TestCloneWorkspaceInstructionsRecordedOwnsSlices(t *testing.T) {
	original := validWorkspaceInstructionsRecorded()
	clonedEvent, err := CloneEvent(original)
	if err != nil {
		t.Fatalf("CloneEvent() error = %v", err)
	}
	cloned := clonedEvent.(WorkspaceInstructionsRecorded)
	cloned.Discovered[0].Path = "changed/AGENTS.md"
	cloned.Changes[0].Content = "changed"
	if original.Discovered[0].Path != "AGENTS.md" || original.Changes[0].Content != "Run focused tests.\n" {
		t.Fatalf("clone aliases original: %#v", original)
	}
}
