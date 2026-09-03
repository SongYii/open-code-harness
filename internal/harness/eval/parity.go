package eval

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// ParityPairKey groups Attempts that may form a valid baseline/candidate
// pair (design §22): equal Scenario digest and repetition index. Every
// other field design §22 lists (Executor kind, fixture digest, limits,
// pairing seed) is already an EvalSet-level invariant shared by every
// Attempt co-resident under one EvalSetID, so this package needs no
// separate representation of them here — the executor-parity report mode
// this file implements is exactly the one case where Executor Kind is
// design's own "where required by report mode" exception: it is the
// deliberately varying dimension between the two arms, never a
// pairing-match field.
type ParityPairKey struct {
	ScenarioDigest  Digest
	RepetitionIndex int
}

// ParityPairKeyForAttempt derives attempt's own pairing key.
func ParityPairKeyForAttempt(attempt Attempt) ParityPairKey {
	return ParityPairKey{ScenarioDigest: attempt.ScenarioDigest, RepetitionIndex: attempt.RepetitionIndex}
}

// ParityArm is one Attempt's already-loaded, already-scoreable evidence,
// reduced to design §11's declared semantic parity fields plus the two
// facts the pairing rule itself inspects (Executor Kind, Subject digest).
// Constructing one never reads or writes a Score, and never touches an
// excluded fact: event/command/runtime IDs, timestamps, temp paths,
// scheduling order, raw transcript/audit bytes, or executor/host/lease/
// shutdown/reload/recovery lifecycle facts.
type ParityArm struct {
	AttemptID     AttemptID
	ExecutorKind  ExecutorKind
	SubjectDigest Digest
	Facts         ParitySemanticFacts
}

// LoadParityArm reads directories' already-published Attempt/Outcome and
// staged evidence documents (the same frozen-evidence path RegradeAttempt
// itself reads from, design §14/§20) and extracts one arm ready for
// ComparePairedArms.
func LoadParityArm(directories AttemptRootDirectories) (ParityArm, error) {
	reader, err := NewArtifactReader(directories)
	if err != nil {
		return ParityArm{}, fmt.Errorf("eval: load parity arm: %w", err)
	}
	publishedAttempt, err := ReadAttempt(directories.Root)
	if err != nil {
		return ParityArm{}, fmt.Errorf("eval: load parity arm: %w", err)
	}
	documents, _, err := readEvidenceDocuments(reader, publishedAttempt)
	if err != nil {
		return ParityArm{}, fmt.Errorf("eval: load parity arm: frozen evidence: %w", err)
	}
	facts, err := extractParitySemanticFacts(reader)
	if err != nil {
		return ParityArm{}, fmt.Errorf("eval: load parity arm: %w", err)
	}
	return ParityArm{
		AttemptID:     documents.Attempt.ID,
		ExecutorKind:  documents.Executor.Kind,
		SubjectDigest: documents.Attempt.SubjectDigest,
		Facts:         facts,
	}, nil
}

// ParitySemanticFacts is the normalized projection design §11 permits a
// parity comparison to inspect: terminal Session/Turn state, tool facts,
// usage facts, workspace result, and request-envelope properties. Every
// field here is either a scalar count/flag or a slice sorted into a
// caller-independent, deterministic order — comparing two instances is a
// plain deep-equal per field, never order- or ID-sensitive.
type ParitySemanticFacts struct {
	OutcomeStatus     OutcomeStatus
	TerminalTurnCount int
	TerminalOpen      bool
	TerminalRunning   bool
	ToolCalls         []ParityToolCall
	Usage             []ParityUsage
	RequestEnvelopes  []ParityRequestEnvelope
	Workspace         []ParityWorkspaceEntry
}

// ParityToolCall is one tool call's declared semantic shape: what was
// asked for (Name/Arguments), what Policy decided (PolicyEffect, empty
// when no policy decision was ever recorded for this call), whether an
// approval resolved it (ApprovalDecision, empty when none was requested),
// and its terminal Result ("completed", "failed:<code>",
// "interrupted:<code>", or "" if the Scenario ended before this call
// resolved). CallID is deliberately not a field — it is this side's own
// internal correlation key only, discarded once ordering is fixed.
type ParityToolCall struct {
	Name             string
	Arguments        string
	PolicyEffect     string
	ApprovalDecision string
	Result           string
}

// ParityUsage is one model call's declared usage facts. LatencyMs and
// ProviderRequestID are deliberately excluded: latency is a live
// wall-clock fact, never a semantic invariant a deterministic fixture
// promises to reproduce, and a provider request ID is exactly the kind of
// generated identifier design §11 excludes.
type ParityUsage struct {
	InputTokens       uint64
	OutputTokens      uint64
	CachedInputTokens uint64
	FinishReason      string
}

// ParityRequestEnvelope is one model request's declared envelope
// properties (design §11's "request-envelope properties"). Messages
// content is deliberately excluded — comparing full prompt text ties
// parity to incidental wording rather than a declared semantic property,
// and every fact that wording would reveal (which tool ran, what it
// returned) is already covered by ParityToolCall.
type ParityRequestEnvelope struct {
	ModelID             string
	ContextWindowTokens uint32
	MaxOutputTokens     uint32
}

// ParityWorkspaceEntry is one collected workspace artifact's declared
// content identity: its path relative to the workspace root (never an
// absolute, machine-local path) and its SHA-256 content digest.
type ParityWorkspaceEntry struct {
	Path   string
	SHA256 string
}

// extractParitySemanticFacts projects reader's own Outcome and audit
// evidence into ParitySemanticFacts. Every correlation it performs (tool
// call CallID, approval ApprovalID, request TurnID/ItemID) is entirely
// internal to this one side's own event stream — none of those IDs ever
// appear in the returned value, and this function never compares against
// any other Attempt's evidence.
func extractParitySemanticFacts(reader *ArtifactReader) (ParitySemanticFacts, error) {
	outcome := reader.Outcome()
	facts := ParitySemanticFacts{OutcomeStatus: outcome.Status}
	if outcome.TerminalSession != nil {
		facts.TerminalTurnCount = outcome.TerminalSession.TurnCount
		facts.TerminalOpen = outcome.TerminalSession.Open
		facts.TerminalRunning = outcome.TerminalSession.Running
	}

	events, ok := readAuditEvents(reader)
	if !ok {
		return ParitySemanticFacts{}, fmt.Errorf("eval: parity: audit evidence is not fully collected")
	}

	type toolCallBuild struct {
		order            int
		name, arguments  string
		policyEffect     string
		approvalDecision string
		result           string
	}
	calls := make(map[string]*toolCallBuild)
	var callOrder []string
	approvalCallID := make(map[domain.ApprovalID]string)

	for _, event := range events {
		switch event.Type {
		case domain.EventToolCallStarted:
			var data domain.ToolCallStarted
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return ParitySemanticFacts{}, fmt.Errorf("eval: parity: decode %s: %w", event.Type, err)
			}
			if _, exists := calls[data.CallID]; !exists {
				calls[data.CallID] = &toolCallBuild{order: len(callOrder), name: data.Name, arguments: data.Arguments}
				callOrder = append(callOrder, data.CallID)
			}
		case domain.EventPolicyDecisionRecorded:
			var data domain.PolicyDecisionRecorded
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return ParitySemanticFacts{}, fmt.Errorf("eval: parity: decode %s: %w", event.Type, err)
			}
			if call, ok := calls[data.CallID]; ok {
				call.policyEffect = data.Effect
			}
		case domain.EventApprovalRequested:
			var data domain.ApprovalRequested
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return ParitySemanticFacts{}, fmt.Errorf("eval: parity: decode %s: %w", event.Type, err)
			}
			approvalCallID[data.ApprovalID] = data.CallID
		case domain.EventApprovalResolved:
			var data domain.ApprovalResolved
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return ParitySemanticFacts{}, fmt.Errorf("eval: parity: decode %s: %w", event.Type, err)
			}
			if callID, ok := approvalCallID[data.ApprovalID]; ok {
				if call, ok := calls[callID]; ok {
					call.approvalDecision = data.Decision
				}
			}
		case domain.EventToolCallCompleted:
			var data domain.ToolCallCompleted
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return ParitySemanticFacts{}, fmt.Errorf("eval: parity: decode %s: %w", event.Type, err)
			}
			if call, ok := calls[data.CallID]; ok {
				call.result = "completed"
			}
		case domain.EventToolCallFailed:
			var data domain.ToolCallFailed
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return ParitySemanticFacts{}, fmt.Errorf("eval: parity: decode %s: %w", event.Type, err)
			}
			if call, ok := calls[data.CallID]; ok {
				call.result = "failed:" + data.Code
			}
		case domain.EventToolCallInterrupted:
			var data domain.ToolCallInterrupted
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return ParitySemanticFacts{}, fmt.Errorf("eval: parity: decode %s: %w", event.Type, err)
			}
			if call, ok := calls[data.CallID]; ok {
				call.result = "interrupted:" + data.Code
			}
		case domain.EventModelUsageRecorded:
			var data domain.ModelUsageRecorded
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return ParitySemanticFacts{}, fmt.Errorf("eval: parity: decode %s: %w", event.Type, err)
			}
			facts.Usage = append(facts.Usage, ParityUsage{
				InputTokens: data.InputTokens, OutputTokens: data.OutputTokens,
				CachedInputTokens: data.CachedInputTokens, FinishReason: data.FinishReason,
			})
		case domain.EventModelRequestRecorded:
			var data domain.ModelRequestRecorded
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return ParitySemanticFacts{}, fmt.Errorf("eval: parity: decode %s: %w", event.Type, err)
			}
			facts.RequestEnvelopes = append(facts.RequestEnvelopes, ParityRequestEnvelope{
				ModelID: data.ModelID, ContextWindowTokens: data.ContextWindowTokens, MaxOutputTokens: data.MaxOutputTokens,
			})
		}
	}

	for _, callID := range callOrder {
		call := calls[callID]
		facts.ToolCalls = append(facts.ToolCalls, ParityToolCall{
			Name: call.name, Arguments: call.arguments,
			PolicyEffect: call.policyEffect, ApprovalDecision: call.approvalDecision, Result: call.result,
		})
	}

	for _, entry := range reader.Entries("workspace") {
		if entry.State != EntryCollected {
			continue
		}
		facts.Workspace = append(facts.Workspace, ParityWorkspaceEntry{Path: entry.Path, SHA256: entry.SHA256})
	}
	sort.Slice(facts.Workspace, func(i, j int) bool { return facts.Workspace[i].Path < facts.Workspace[j].Path })

	return facts, nil
}

// ParityMismatch is one declared semantic field a comparison found to
// differ between the baseline and candidate arm.
type ParityMismatch struct {
	Field     string `json:"field"`
	Baseline  string `json:"baseline"`
	Candidate string `json:"candidate"`
}

// ComparePairedArms compares two arms already confirmed to be a valid
// pair (equal ParityPairKey, differing Subject digest, baseline/candidate
// Executor Kinds this report mode expects) and returns every declared
// semantic field that differs. An empty result is parity; the caller
// decides what an empty vs. non-empty result means for gating.
func ComparePairedArms(baseline, candidate ParityArm) []ParityMismatch {
	var mismatches []ParityMismatch
	add := func(field string, base, cand any) {
		baseStr, candStr := fmt.Sprint(base), fmt.Sprint(cand)
		if baseStr != candStr {
			mismatches = append(mismatches, ParityMismatch{Field: field, Baseline: baseStr, Candidate: candStr})
		}
	}

	a, b := baseline.Facts, candidate.Facts
	add("outcomeStatus", a.OutcomeStatus, b.OutcomeStatus)
	add("terminalTurnCount", a.TerminalTurnCount, b.TerminalTurnCount)
	add("terminalOpen", a.TerminalOpen, b.TerminalOpen)
	add("terminalRunning", a.TerminalRunning, b.TerminalRunning)
	add("toolCalls", a.ToolCalls, b.ToolCalls)
	add("usage", a.Usage, b.Usage)
	add("requestEnvelopes", a.RequestEnvelopes, b.RequestEnvelopes)
	add("workspace", a.Workspace, b.Workspace)

	return mismatches
}
