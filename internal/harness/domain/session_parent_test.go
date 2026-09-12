package domain

import (
	"reflect"
	"testing"
	"time"
)

func TestCreateSessionCarriesDurableParentLineage(t *testing.T) {
	t.Parallel()
	parent := &SessionParent{SessionID: "parent-session", TurnID: "parent-turn", ItemID: "parent-item", CallID: "parent-call"}
	wantParent := *parent
	events, err := Decide(Session{}, CreateSession{SessionID: "child-session", WorkspaceRoot: "/workspace", Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	want := SessionCreated{WorkspaceRoot: "/workspace", Parent: &wantParent}
	if len(events) != 1 || !reflect.DeepEqual(events[0].Event, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	parent.CallID = "mutated"
	if got := events[0].Event.(SessionCreated).Parent.CallID; got != "parent-call" {
		t.Fatalf("event parent call ID = %q, want defensive copy", got)
	}

	record := RecordedEvent{SchemaVersion: 1, ID: "event-1", CommandID: "command-1", SessionID: "child-session", Sequence: 1, OccurredAt: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), Event: events[0].Event}
	state, err := Apply(Session{}, record)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.Parent, want.Parent) {
		t.Fatalf("state parent = %#v, want %#v", state.Parent, want.Parent)
	}
	state.Parent.CallID = "state-mutated"
	if got := record.Event.(SessionCreated).Parent.CallID; got != "parent-call" {
		t.Fatalf("record parent call ID = %q, want defensive copy", got)
	}
}

func TestCreateSessionRejectsInvalidParentLineage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		parent SessionParent
	}{
		{name: "missing session", parent: SessionParent{TurnID: "turn", ItemID: "item", CallID: "call"}},
		{name: "missing turn", parent: SessionParent{SessionID: "parent", ItemID: "item", CallID: "call"}},
		{name: "missing item", parent: SessionParent{SessionID: "parent", TurnID: "turn", CallID: "call"}},
		{name: "missing call", parent: SessionParent{SessionID: "parent", TurnID: "turn", ItemID: "item"}},
		{name: "self parent", parent: SessionParent{SessionID: "child", TurnID: "turn", ItemID: "item", CallID: "call"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if events, err := Decide(Session{}, CreateSession{SessionID: "child", WorkspaceRoot: "/workspace", Parent: &test.parent}); err == nil || events != nil {
				t.Fatalf("Decide() = (%#v, %v), want rejection", events, err)
			}
		})
	}
}

func TestSessionCreatedParentCodecIsOptionalAndStrict(t *testing.T) {
	t.Parallel()
	parent := &SessionParent{SessionID: "parent", TurnID: "turn", ItemID: "item", CallID: "call"}
	record := RecordedEvent{SchemaVersion: 1, ID: "event-1", CommandID: "command-1", SessionID: "child", Sequence: 1, OccurredAt: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), Event: SessionCreated{WorkspaceRoot: "/workspace", Parent: parent}}
	wire, err := MarshalRecordedEvent(record)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalRecordedEvent(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded.Event, record.Event) {
		t.Fatalf("decoded event = %#v, want %#v", decoded.Event, record.Event)
	}

	legacy := `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"child","sequence":1,"occurredAt":"2026-09-12T00:00:00Z","type":"session.created","data":{"workspaceRoot":"/workspace"}}`
	decoded, err = UnmarshalRecordedEvent([]byte(legacy))
	if err != nil || decoded.Event.(SessionCreated).Parent != nil {
		t.Fatalf("legacy decode = (%#v, %v), want nil parent", decoded.Event, err)
	}

	record.Event = SessionCreated{WorkspaceRoot: "/workspace", Parent: &SessionParent{SessionID: "child", TurnID: "turn", ItemID: "item", CallID: "call"}}
	if _, err := MarshalRecordedEvent(record); !IsCode(err, CodeInvalidEvent) {
		t.Fatalf("MarshalRecordedEvent(self parent) error = %v, want invalid event", err)
	}
}
