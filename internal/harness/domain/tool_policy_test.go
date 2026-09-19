package domain

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestValidateToolPolicyIdentity(t *testing.T) {
	t.Parallel()

	valid := ToolPolicyIdentity{
		ID:           "deny_tools",
		Version:      "1.0.0+build",
		ConfigDigest: strings.Repeat("a", 64),
	}
	if err := ValidateToolPolicyIdentity(nil); err != nil {
		t.Fatalf("ValidateToolPolicyIdentity(nil) error = %v", err)
	}
	if err := ValidateToolPolicyIdentity(&valid); err != nil {
		t.Fatalf("ValidateToolPolicyIdentity(valid) error = %v", err)
	}

	tests := []struct {
		name     string
		identity ToolPolicyIdentity
	}{
		{name: "empty ID", identity: ToolPolicyIdentity{Version: valid.Version, ConfigDigest: valid.ConfigDigest}},
		{name: "oversized ID", identity: ToolPolicyIdentity{ID: strings.Repeat("a", 129), Version: valid.Version, ConfigDigest: valid.ConfigDigest}},
		{name: "invalid ID", identity: ToolPolicyIdentity{ID: "deny/tools", Version: valid.Version, ConfigDigest: valid.ConfigDigest}},
		{name: "empty version", identity: ToolPolicyIdentity{ID: valid.ID, ConfigDigest: valid.ConfigDigest}},
		{name: "oversized version", identity: ToolPolicyIdentity{ID: valid.ID, Version: strings.Repeat("a", 129), ConfigDigest: valid.ConfigDigest}},
		{name: "invalid version", identity: ToolPolicyIdentity{ID: valid.ID, Version: "版本", ConfigDigest: valid.ConfigDigest}},
		{name: "non hex digest", identity: ToolPolicyIdentity{ID: valid.ID, Version: valid.Version, ConfigDigest: strings.Repeat("g", 64)}},
		{name: "uppercase digest", identity: ToolPolicyIdentity{ID: valid.ID, Version: valid.Version, ConfigDigest: strings.Repeat("A", 64)}},
		{name: "short digest", identity: ToolPolicyIdentity{ID: valid.ID, Version: valid.Version, ConfigDigest: strings.Repeat("a", 62)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateToolPolicyIdentity(&test.identity); err == nil {
				t.Fatalf("ValidateToolPolicyIdentity(%#v) succeeded", test.identity)
			}
		})
	}
}

func TestCloneToolPolicyIdentity(t *testing.T) {
	t.Parallel()

	if CloneToolPolicyIdentity(nil) != nil {
		t.Fatal("CloneToolPolicyIdentity(nil) returned non-nil")
	}
	original := &ToolPolicyIdentity{ID: "deny_tools", Version: "1.0.0", ConfigDigest: strings.Repeat("a", 64)}
	cloned := CloneToolPolicyIdentity(original)
	if cloned == original || *cloned != *original {
		t.Fatalf("CloneToolPolicyIdentity() = %#v", cloned)
	}
	original.ID = "mutated"
	if cloned.ID != "deny_tools" {
		t.Fatalf("clone changed with source: %#v", cloned)
	}
}

func TestPolicyDecisionIdentityRoundTripAndLegacyBytes(t *testing.T) {
	t.Parallel()

	legacy := codecTestRecord(PolicyDecisionRecorded{
		TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Name: "read_file",
		Effect: PolicyEffectAllow, RuleID: "default.read", Reason: "in_workspace",
	})
	legacyJSON, err := MarshalRecordedEvent(legacy)
	if err != nil {
		t.Fatal(err)
	}
	wantLegacy := `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-11T01:02:03Z","type":"policy.decision.recorded","data":{"turnID":"turn-1","itemID":"item-1","callID":"call-1","name":"read_file","effect":"allow","ruleID":"default.read","reason":"in_workspace"}}`
	if string(legacyJSON) != wantLegacy || bytes.Contains(legacyJSON, []byte(`"policy"`)) {
		t.Fatalf("legacy bytes changed:\n got %s\nwant %s", legacyJSON, wantLegacy)
	}

	identity := &ToolPolicyIdentity{ID: "deny_tools", Version: "1.0.0", ConfigDigest: strings.Repeat("a", 64)}
	custom := codecTestRecord(PolicyDecisionRecorded{
		Policy: identity, TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Name: "exec",
		Effect: PolicyEffectDeny, RuleID: "deny_tools.name", Reason: "configured_deny",
	})
	encoded, err := MarshalRecordedEvent(custom)
	if err != nil {
		t.Fatal(err)
	}
	wantCustom := `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-11T01:02:03Z","type":"policy.decision.recorded","data":{"policy":{"id":"deny_tools","version":"1.0.0","configDigest":"` + strings.Repeat("a", 64) + `"},"turnID":"turn-1","itemID":"item-1","callID":"call-1","name":"exec","effect":"deny","ruleID":"deny_tools.name","reason":"configured_deny"}}`
	if string(encoded) != wantCustom {
		t.Fatalf("custom bytes:\n got %s\nwant %s", encoded, wantCustom)
	}
	decoded, err := UnmarshalRecordedEvent(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, custom) {
		t.Fatalf("decoded = %#v, want %#v", decoded, custom)
	}
	again, err := MarshalRecordedEvent(decoded)
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatalf("round trip = %s, %v", again, err)
	}
}

func TestPolicyDecisionIdentityRejectsNonStrictPayloads(t *testing.T) {
	t.Parallel()

	digest := strings.Repeat("a", 64)
	prefix := `{"schemaVersion":1,"id":"event-1","commandId":"command-1","sessionId":"session-1","sequence":1,"occurredAt":"2026-08-11T01:02:03Z","type":"policy.decision.recorded","data":`
	suffix := `,"turnID":"turn-1","itemID":"item-1","callID":"call-1","name":"exec","effect":"deny","ruleID":"deny_tools.name","reason":"configured_deny"}}`
	tests := []struct {
		name   string
		policy string
	}{
		{name: "unknown key", policy: `{"id":"deny_tools","version":"1.0.0","configDigest":"` + digest + `","extra":true}`},
		{name: "missing key", policy: `{"id":"deny_tools","version":"1.0.0"}`},
		{name: "duplicate key", policy: `{"id":"deny_tools","id":"other","version":"1.0.0","configDigest":"` + digest + `"}`},
		{name: "null", policy: `null`},
		{name: "invalid label", policy: `{"id":"deny/tools","version":"1.0.0","configDigest":"` + digest + `"}`},
		{name: "uppercase digest", policy: `{"id":"deny_tools","version":"1.0.0","configDigest":"` + strings.Repeat("A", 64) + `"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := []byte(prefix + `{"policy":` + test.policy + suffix)
			if _, err := UnmarshalRecordedEvent(input); !IsCode(err, CodeInvalidEvent) {
				t.Fatalf("UnmarshalRecordedEvent() error = %v, want %q", err, CodeInvalidEvent)
			}
		})
	}
}

func TestPolicyDecisionIdentityIsCloned(t *testing.T) {
	t.Parallel()

	identity := &ToolPolicyIdentity{ID: "deny_tools", Version: "1.0.0", ConfigDigest: strings.Repeat("a", 64)}
	event := PolicyDecisionRecorded{
		Policy: identity, TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Name: "exec",
		Effect: PolicyEffectDeny, RuleID: "deny_tools.name", Reason: "configured_deny",
	}
	clonedEvent, err := CloneEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	cloned := clonedEvent.(PolicyDecisionRecorded)
	if cloned.Policy == identity || *cloned.Policy != *identity {
		t.Fatalf("cloned identity = %#v", cloned.Policy)
	}
	identity.ID = "mutated"
	if cloned.Policy.ID != "deny_tools" {
		t.Fatalf("clone changed with source: %#v", cloned.Policy)
	}
}

func TestDecideRecordPolicyDecisionClonesIdentity(t *testing.T) {
	t.Parallel()

	state := compactActiveSession(t)
	state = applyCompactRecord(t, state, TurnStarted{TurnID: "turn-1", Input: "inspect"})
	state = applyCompactRecord(t, state, validToolCallStarted("turn-1", "item-tool"))
	identity := &ToolPolicyIdentity{ID: "deny_tools", Version: "1.0.0", ConfigDigest: strings.Repeat("a", 64)}
	command := RecordPolicyDecision{
		Policy: identity, SessionID: state.ID, TurnID: "turn-1", ItemID: "item-tool",
		CallID: "call-1", Name: "exec", Effect: PolicyEffectDeny, RuleID: "deny_tools.name", Reason: "configured_deny",
	}
	events, err := Decide(state, command)
	if err != nil {
		t.Fatal(err)
	}
	got := events[0].Event.(PolicyDecisionRecorded)
	if got.Policy == identity || *got.Policy != *identity {
		t.Fatalf("event identity = %#v", got.Policy)
	}
	identity.ID = "mutated"
	if got.Policy.ID != "deny_tools" {
		t.Fatalf("event identity changed with command: %#v", got.Policy)
	}
}
