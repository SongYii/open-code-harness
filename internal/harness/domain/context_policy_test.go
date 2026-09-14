package domain

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestContextPolicyRoundTripAndLegacyOmission(t *testing.T) {
	identity := &ContextPolicyIdentity{ID: "keep_last_n_turns", Version: "1.0.0", ConfigDigest: strings.Repeat("a", 64)}
	prepared := validContextPreparedRecorded("turn-1", "item-1")
	started := ContextCompactionStarted{ID: "compact-1", Trigger: ContextTriggerPreTurn, Strategy: ContextStrategySummary, BaseSourceHead: 10, SourceSchema: "och_context_source_v1", MeterID: "och_wire_estimate_v1"}
	for _, event := range []Event{prepared, started} {
		record := compactRecord(compactActiveSession(t), event)
		legacy, err := MarshalRecordedEvent(record)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(legacy, []byte(`"policy"`)) {
			t.Fatal("default payload changed")
		}
		decoded, err := UnmarshalRecordedEvent(legacy)
		if err != nil {
			t.Fatal(err)
		}
		again, err := MarshalRecordedEvent(decoded)
		if err != nil || !bytes.Equal(legacy, again) {
			t.Fatal("legacy bytes changed")
		}
		switch value := event.(type) {
		case ContextPreparedRecorded:
			value.Policy = identity
			event = value
		case ContextCompactionStarted:
			value.Policy = identity
			event = value
		}
		record.Event = event
		encoded, err := MarshalRecordedEvent(record)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err = UnmarshalRecordedEvent(encoded)
		if err != nil || !reflect.DeepEqual(decoded.Event, event) {
			t.Fatalf("custom identity roundtrip: %v", err)
		}
		for _, bad := range [][]byte{
			bytes.Replace(encoded, []byte(`"version":"1.0.0"`), []byte(`"version":"1.0.0","extra":true`), 1),
			bytes.Replace(encoded, []byte(`"version":"1.0.0"`), []byte(`"version":"1.0.0","version":"2"`), 1),
			bytes.Replace(encoded, []byte(strings.Repeat("a", 64)), []byte("bad"), 1),
		} {
			if _, err := UnmarshalRecordedEvent(bad); err == nil {
				t.Fatal("malformed policy evidence accepted")
			}
		}
		cloned, err := CloneEvent(event)
		if err != nil {
			t.Fatal(err)
		}
		switch value := cloned.(type) {
		case ContextPreparedRecorded:
			if value.Policy == identity {
				t.Fatal("clone shares identity")
			}
		case ContextCompactionStarted:
			if value.Policy == identity {
				t.Fatal("clone shares identity")
			}
		}
	}
}
