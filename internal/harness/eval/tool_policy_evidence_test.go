package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestValidateToolPolicyEvidenceAgreement(t *testing.T) {
	custom := customSubjectToolPolicy(t, `{"names":["exec"]}`)
	matching := &domain.ToolPolicyIdentity{ID: custom.ID, Version: custom.Version, ConfigDigest: custom.ConfigDigest}
	different := &domain.ToolPolicyIdentity{ID: custom.ID, Version: "2.0.0", ConfigDigest: custom.ConfigDigest}
	event := func(identity *domain.ToolPolicyIdentity) verifierAuditEvent {
		data, err := json.Marshal(domain.PolicyDecisionRecorded{
			Policy: identity, TurnID: "turn-1", ItemID: "item-1", CallID: "call-1", Name: "exec",
			Effect: domain.PolicyEffectDeny, RuleID: "deny_tools.exec", Reason: "configured",
		})
		if err != nil {
			t.Fatal(err)
		}
		return verifierAuditEvent{Type: domain.EventPolicyDecisionRecorded, Data: data}
	}
	tests := []struct {
		name     string
		expected *SubjectToolPolicy
		events   []verifierAuditEvent
		wantErr  bool
	}{
		{name: "builtin no decision", events: nil},
		{name: "custom no decision", expected: custom, events: nil},
		{name: "builtin unattributed", events: []verifierAuditEvent{event(nil)}},
		{name: "custom matching", expected: custom, events: []verifierAuditEvent{event(matching)}},
		{name: "builtin rejects attribution", events: []verifierAuditEvent{event(matching)}, wantErr: true},
		{name: "custom rejects missing", expected: custom, events: []verifierAuditEvent{event(nil)}, wantErr: true},
		{name: "custom rejects mismatch", expected: custom, events: []verifierAuditEvent{event(different)}, wantErr: true},
		{name: "custom rejects malformed", expected: custom, events: []verifierAuditEvent{{Type: domain.EventPolicyDecisionRecorded, Data: json.RawMessage(`{"policy":`)}}, wantErr: true},
		{name: "custom rejects invalid identity", expected: custom, events: []verifierAuditEvent{event(&domain.ToolPolicyIdentity{ID: custom.ID, Version: custom.Version, ConfigDigest: strings.Repeat("A", 64)})}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateToolPolicyEvidence(test.expected, test.events)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateToolPolicyEvidence() error = %v, wantErr=%t", err, test.wantErr)
			}
		})
	}
}

func TestCollectEvidenceRejectsToolPolicyMismatchBeforePublication(t *testing.T) {
	directories, execution, documents := runHappyAttempt(t)
	documents.Subject.Policy.ToolPolicy = customSubjectToolPolicy(t, `{"names":["write_file"]}`)
	subjectDigest, err := SubjectDigest(documents.Subject)
	if err != nil {
		t.Fatal(err)
	}
	documents.Attempt.SubjectDigest = subjectDigest
	documents.EvalSet = testEvalSetFor(t, LaneFixture, documents.Scenario, documents.Subject, documents.Executor, nil)
	documents.Attempt.EvalSetID = documents.EvalSet.ID
	if err := os.WriteFile(filepath.Join(directories.Root, attemptFilename), marshal(t, documents.Attempt), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := CollectEvidence(context.Background(), directories, execution, execution.Outcome, documents, CollectionLimits{}); err == nil {
		t.Fatal("CollectEvidence accepted audit attribution that disagreed with Subject")
	}
	if _, err := os.Stat(filepath.Join(directories.Root, outcomeFilename)); !os.IsNotExist(err) {
		t.Fatalf("mismatched collection published Outcome: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directories.Evidence, manifestFilename)); !os.IsNotExist(err) {
		t.Fatalf("mismatched collection published manifest: %v", err)
	}
}

func TestReadEvidenceDocumentsRejectsTamperedToolPolicyAttribution(t *testing.T) {
	directories, _, _ := collectedHappyAttempt(t)
	manifest, err := ReadEvidenceManifest(directories.Root)
	if err != nil {
		t.Fatal(err)
	}
	identity := domain.ToolPolicyIdentity{ID: "deny_tools", Version: "1.0.0", ConfigDigest: strings.Repeat("a", 64)}
	found := false
	for index := range manifest.Entries {
		entry := &manifest.Entries[index]
		if entry.Role != "audit" || entry.State != EntryCollected {
			continue
		}
		fullPath := filepath.Join(directories.Evidence, filepath.FromSlash(entry.Path))
		data, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatal(err)
		}
		data, changed := injectToolPolicyAttribution(t, data, identity)
		if !changed {
			continue
		}
		if err := os.WriteFile(fullPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		entry.SHA256 = hex.EncodeToString(sum[:])
		entry.ByteLength = int64(len(data))
		found = true
		break
	}
	if !found {
		t.Fatal("happy attempt carried no policy decision to tamper")
	}
	if err := os.WriteFile(filepath.Join(directories.Evidence, manifestFilename), marshal(t, manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatal(err)
	}
	published, err := ReadAttempt(directories.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := readEvidenceDocuments(reader, published); err == nil {
		t.Fatal("readEvidenceDocuments accepted custom attribution for builtin Subject")
	}
}

func TestReadEvidenceDocumentsAllowsCustomSubjectWithoutAudit(t *testing.T) {
	subject := validSubject()
	subject.Policy.ToolPolicy = customSubjectToolPolicy(t, `{"names":["exec"]}`)
	directories, _ := collectedIdentityAttempt(t, LaneFixture, subject, nil, identityAttemptOptions{})
	reader, err := NewArtifactReader(directories)
	if err != nil {
		t.Fatal(err)
	}
	published, err := ReadAttempt(directories.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := readEvidenceDocuments(reader, published); err != nil {
		t.Fatalf("custom Subject with no collected audit should remain readable: %v", err)
	}
}

func injectToolPolicyAttribution(t *testing.T, data []byte, identity domain.ToolPolicyIdentity) ([]byte, bool) {
	t.Helper()
	lines := bytesSplitLines(data)
	changed := false
	for lineIndex, line := range lines {
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(line, &envelope); err != nil {
			t.Fatal(err)
		}
		rawEvents, ok := envelope["events"]
		if !ok {
			continue
		}
		var events []map[string]json.RawMessage
		if err := json.Unmarshal(rawEvents, &events); err != nil {
			t.Fatal(err)
		}
		for eventIndex := range events {
			var eventType string
			if err := json.Unmarshal(events[eventIndex]["type"], &eventType); err != nil {
				t.Fatal(err)
			}
			if eventType != domain.EventPolicyDecisionRecorded {
				continue
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(events[eventIndex]["data"], &payload); err != nil {
				t.Fatal(err)
			}
			payload["policy"] = json.RawMessage(marshal(t, identity))
			events[eventIndex]["data"] = json.RawMessage(marshal(t, payload))
			changed = true
			break
		}
		if changed {
			envelope["events"] = json.RawMessage(marshal(t, events))
			lines[lineIndex] = marshal(t, envelope)
			break
		}
	}
	return joinJSONLLines(lines), changed
}

func bytesSplitLines(data []byte) [][]byte {
	trimmed := strings.TrimSuffix(string(data), "\n")
	parts := strings.Split(trimmed, "\n")
	lines := make([][]byte, len(parts))
	for index := range parts {
		lines[index] = []byte(parts[index])
	}
	return lines
}

func joinJSONLLines(lines [][]byte) []byte {
	values := make([]string, len(lines))
	for index := range lines {
		values[index] = string(lines[index])
	}
	return []byte(strings.Join(values, "\n") + "\n")
}
