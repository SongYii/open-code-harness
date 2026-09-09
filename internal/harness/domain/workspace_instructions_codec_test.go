package domain

import (
	"reflect"
	"strings"
	"testing"
)

func TestCodecWorkspaceInstructionsIsCanonicalStrictAndRoundTrips(t *testing.T) {
	record := canonicalCodecRecord(7, validWorkspaceInstructionsRecorded())
	encoded, err := MarshalRecordedEvent(record)
	if err != nil {
		t.Fatalf("MarshalRecordedEvent() error = %v", err)
	}
	want := `{"schemaVersion":1,"id":"event-7","commandId":"command-7","sessionId":"session-1","sequence":7,"occurredAt":"2026-08-11T01:02:03Z","type":"workspace.instructions.recorded","data":{"formatVersion":"workspace_instructions_v1","promptID":"och_coding_agent_v1","promptDigest":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","epoch":1,"discovered":[{"path":"AGENTS.md","scope":"."}],"changes":[{"action":"set","path":"AGENTS.md","scope":".","digest":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","content":"Run focused tests.\n"}],"renderedMessage":"\u003cworkspace_instruction_change\u003eset\u003c/workspace_instruction_change\u003e","effectiveSetDigest":"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}}`
	if string(encoded) != want {
		t.Fatalf("encoded = %s\nwant = %s", encoded, want)
	}
	decoded, err := UnmarshalRecordedEvent(encoded)
	if err != nil || !reflect.DeepEqual(decoded, record) {
		t.Fatalf("UnmarshalRecordedEvent() = (%#v, %v), want (%#v, nil)", decoded, err, record)
	}

	for _, invalid := range []string{
		strings.Replace(want, `"epoch":1`, `"epoch":1,"extra":true`, 1),
		strings.Replace(want, `"scope":"."}`, `"scope":".","extra":true}`, 1),
		strings.Replace(want, `"content":"Run focused tests.\n"}`, `"content":"Run focused tests.\n","extra":true}`, 1),
	} {
		if _, err := UnmarshalRecordedEvent([]byte(invalid)); !IsCode(err, CodeInvalidEvent) {
			t.Fatalf("UnmarshalRecordedEvent(%s) error = %v, want %q", invalid, err, CodeInvalidEvent)
		}
	}
}
