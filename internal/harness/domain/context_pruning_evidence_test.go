package domain

import (
	"encoding/json"
	"testing"
	"time"
)

// TestContextPreparedRecordedCarriesPrunedToolResultCount pins the
// per-request pruning fact onto durable evidence. contextengine already
// reports how many Tool Results were actually projected for a request, but
// until this field existed a verifier could only infer projection from the
// paired model request's messages — it could not cross-check the Context
// Engine's own decision. Configuration is never proof; this observed count
// is.
func TestContextPreparedRecordedCarriesPrunedToolResultCount(t *testing.T) {
	event := validContextPreparedRecorded("turn-1", "item-1")
	event.ContextDecisionID = "ctxdecision-00000000000000000000000000000001"
	event.PrunedToolResultCount = 3

	record := RecordedEvent{
		SchemaVersion: 1, ID: "event-1", CommandID: "command-1", SessionID: "session-1",
		Sequence: 7, OccurredAt: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC), Event: event,
	}
	data, err := MarshalRecordedEvent(record)
	if err != nil {
		t.Fatalf("MarshalRecordedEvent: %v", err)
	}
	decoded, err := UnmarshalRecordedEvent(data)
	if err != nil {
		t.Fatalf("UnmarshalRecordedEvent: %v", err)
	}
	prepared, ok := decoded.Event.(ContextPreparedRecorded)
	if !ok {
		t.Fatalf("decoded event type = %T, want ContextPreparedRecorded", decoded.Event)
	}
	if prepared.PrunedToolResultCount != 3 {
		t.Fatalf("PrunedToolResultCount = %d, want 3", prepared.PrunedToolResultCount)
	}
}

// TestContextPreparedRecordedOmitsZeroPrunedToolResultCount keeps the field
// additive: an event written before it existed must decode unchanged, and a
// zero count must not start appearing in canonical bytes that previously
// did not carry it.
func TestContextPreparedRecordedOmitsZeroPrunedToolResultCount(t *testing.T) {
	event := validContextPreparedRecorded("turn-1", "item-1")
	_, payload, err := MarshalEventPayload(event)
	if err != nil {
		t.Fatalf("MarshalEventPayload: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if _, present := fields["prunedToolResultCount"]; present {
		t.Fatalf("a zero pruning count was serialized: %s", payload)
	}
}
