package acp

import (
	"encoding/json"
	"testing"
)

func sessionUpdateParams(t *testing.T, update string) json.RawMessage {
	t.Helper()
	return json.RawMessage(`{"sessionId":"s1","update":` + update + `}`)
}

func TestTrajectoryAppliesAgentMessageChunk(t *testing.T) {
	traj := NewTrajectory()
	event := traj.Apply(sessionUpdateParams(t, `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}`))
	if event.Kind != RenderMessageChunk || event.FromUser || event.Text != "hello" {
		t.Fatalf("Apply() = %#v, want an agent message chunk \"hello\"", event)
	}
}

func TestTrajectoryAppliesUserMessageChunk(t *testing.T) {
	traj := NewTrajectory()
	event := traj.Apply(sessionUpdateParams(t, `{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hi"}}`))
	if event.Kind != RenderMessageChunk || !event.FromUser || event.Text != "hi" {
		t.Fatalf("Apply() = %#v, want a user message chunk \"hi\"", event)
	}
}

func TestTrajectoryAppliesToolCall(t *testing.T) {
	traj := NewTrajectory()
	event := traj.Apply(sessionUpdateParams(t, `{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"Read file","kind":"read","status":"pending"}`))
	want := ToolCall{ID: "tc1", Title: "Read file", Kind: "read", Status: ToolCallPending}
	if event.Kind != RenderToolCall || event.Tool != want {
		t.Fatalf("Apply() = %#v, want RenderToolCall with %#v", event, want)
	}
}

func TestTrajectoryToolCallThenTwoUpdatesReachesATerminalStatus(t *testing.T) {
	traj := NewTrajectory()
	traj.Apply(sessionUpdateParams(t, `{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"Read file","kind":"read","status":"pending"}`))

	inProgress := traj.Apply(sessionUpdateParams(t, `{"sessionUpdate":"tool_call_update","toolCallId":"tc1","status":"in_progress"}`))
	if inProgress.Kind != RenderToolCallUpdate || inProgress.Tool.Status != ToolCallInProgress {
		t.Fatalf("first update = %#v, want in_progress", inProgress)
	}
	if inProgress.Tool.Title != "Read file" || inProgress.Tool.Kind != "read" {
		t.Fatalf("first update = %#v, want title/kind preserved from creation", inProgress)
	}

	completed := traj.Apply(sessionUpdateParams(t, `{"sessionUpdate":"tool_call_update","toolCallId":"tc1","status":"completed"}`))
	if completed.Kind != RenderToolCallUpdate || completed.Tool.Status != ToolCallCompleted {
		t.Fatalf("second update = %#v, want completed", completed)
	}
}

func TestTrajectoryInterleavedToolCallsDoNotCrossContaminate(t *testing.T) {
	traj := NewTrajectory()
	traj.Apply(sessionUpdateParams(t, `{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"A","kind":"read","status":"pending"}`))
	traj.Apply(sessionUpdateParams(t, `{"sessionUpdate":"tool_call","toolCallId":"tc2","title":"B","kind":"write","status":"pending"}`))

	updated1 := traj.Apply(sessionUpdateParams(t, `{"sessionUpdate":"tool_call_update","toolCallId":"tc1","status":"completed"}`))
	if updated1.Tool.ID != "tc1" || updated1.Tool.Title != "A" || updated1.Tool.Status != ToolCallCompleted {
		t.Fatalf("tc1 update = %#v, want A completed", updated1)
	}

	updated2 := traj.Apply(sessionUpdateParams(t, `{"sessionUpdate":"tool_call_update","toolCallId":"tc2","status":"failed"}`))
	if updated2.Tool.ID != "tc2" || updated2.Tool.Title != "B" || updated2.Tool.Status != ToolCallFailed {
		t.Fatalf("tc2 update = %#v, want B failed", updated2)
	}
	// tc1 must still read back as completed, not clobbered by tc2's update.
	if traj.calls["tc1"].Status != ToolCallCompleted {
		t.Fatalf("tc1 status after tc2's update = %q, want completed (cross-contamination)", traj.calls["tc1"].Status)
	}
}

func TestTrajectoryUnrecognizedVariantIsALabeledAnomalyNotAPanicOrDrop(t *testing.T) {
	traj := NewTrajectory()
	event := traj.Apply(sessionUpdateParams(t, `{"sessionUpdate":"plan","entries":[]}`))
	if event.Kind != RenderAnomaly {
		t.Fatalf("Apply() = %#v, want RenderAnomaly for an unrecognized variant", event)
	}
	if event.SessionUpdate != "plan" {
		t.Fatalf("Apply() SessionUpdate = %q, want the raw variant name preserved", event.SessionUpdate)
	}
	if len(event.Raw) == 0 {
		t.Fatal("Apply() anomaly carries no raw payload to log")
	}
}

func TestTrajectoryToolCallUpdateForAnUnknownIDIsALabeledAnomalyNotAPhantomEntry(t *testing.T) {
	traj := NewTrajectory()
	event := traj.Apply(sessionUpdateParams(t, `{"sessionUpdate":"tool_call_update","toolCallId":"never-created","status":"completed"}`))
	if event.Kind != RenderAnomaly {
		t.Fatalf("Apply() = %#v, want RenderAnomaly for an unknown toolCallId", event)
	}
	if _, exists := traj.calls["never-created"]; exists {
		t.Fatal("Apply() created a phantom tool call entry for an update it could not attach to anything")
	}
}

func TestTrajectoryMalformedParamsIsALabeledAnomaly(t *testing.T) {
	traj := NewTrajectory()
	event := traj.Apply(json.RawMessage(`{"sessionId":"s1"}`)) // no "update" field at all
	if event.Kind != RenderAnomaly {
		t.Fatalf("Apply() = %#v, want RenderAnomaly when params carries no update object", event)
	}
}
