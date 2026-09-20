package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func validateToolPolicyEvidence(expected *SubjectToolPolicy, events []verifierAuditEvent) error {
	for _, event := range events {
		if event.Type != domain.EventPolicyDecisionRecorded {
			continue
		}
		var data struct {
			Policy *domain.ToolPolicyIdentity `json:"policy"`
		}
		trimmed := bytes.TrimSpace(event.Data)
		if len(trimmed) == 0 || trimmed[0] != '{' {
			return fmt.Errorf("eval: malformed policy decision audit data")
		}
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return fmt.Errorf("eval: malformed policy decision audit data: %w", err)
		}
		if data.Policy != nil {
			if err := domain.ValidateToolPolicyIdentity(data.Policy); err != nil {
				return fmt.Errorf("eval: invalid tool policy audit identity: %w", err)
			}
		}
		if expected == nil {
			if data.Policy != nil {
				return fmt.Errorf("eval: builtin Subject audit carries custom tool policy attribution")
			}
			continue
		}
		if data.Policy == nil || data.Policy.ID != expected.ID || data.Policy.Version != expected.Version || data.Policy.ConfigDigest != expected.ConfigDigest {
			return fmt.Errorf("eval: audit tool policy attribution disagrees with frozen Subject")
		}
	}
	return nil
}

func validateCollectedToolPolicyEvidence(evidenceRoot string, entries []ManifestEntry, expected *SubjectToolPolicy) error {
	var events []verifierAuditEvent
	found := false
	for _, entry := range entries {
		if entry.Role != "audit" || entry.State != EntryCollected {
			continue
		}
		clean := path.Clean(entry.Path)
		if clean == "audit" || !strings.HasPrefix(clean, "audit/") {
			return fmt.Errorf("eval: collected audit entry is outside the core audit directory")
		}
		fullPath := filepath.Join(evidenceRoot, filepath.FromSlash(clean))
		if !pathWithin(fullPath, evidenceRoot) {
			return fmt.Errorf("eval: collected audit entry escapes the evidence root")
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return fmt.Errorf("eval: read collected audit evidence: %w", err)
		}
		decoded, err := decodeAuditEvents(data)
		if err != nil {
			return err
		}
		events = append(events, decoded...)
		found = true
	}
	if !found {
		return nil
	}
	return validateToolPolicyEvidence(expected, events)
}
