package eval

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// contextTrace is one Attempt's Context evidence, parsed once from canonical
// audit and indexed for every Context verifier to share.
//
// It exists so the verifiers stop re-walking the same JSON with slightly
// different ad-hoc loops. More importantly, it is where the correlation rules
// live: a verifier that had to re-derive "does this request pair with a
// preparation" for itself would eventually get it subtly wrong in one place
// and pass on evidence that does not actually hold together.
//
// Building is fail-closed. Anything the builder cannot make sense of —
// unreadable audit, malformed JSON, an orphan terminal event, a broken
// pairing, a forked checkpoint chain — is an error, and a caller must turn
// that into an indeterminate criterion rather than a behavioural fail. A
// behavioural fail means "the evidence is intact and shows this did not
// happen", which is a different and much stronger claim.
type contextTrace struct {
	compactions     map[string]*contextCompaction
	compactionOrder []string

	decisions     map[string]*contextDecision
	decisionOrder []string

	checkpoints     map[string]domain.ContextCheckpointRecord
	checkpointOrder []string

	// Usages are provider usage records in canonical order, which is what
	// the usage-anchor criterion needs to establish the preceding eligible
	// anchor for a later preparation.
	Usages []domain.ModelUsageRecorded
}

// contextCompaction is one compaction bracket. Exactly one of Completed or
// Failed is non-nil once the bracket closes; both nil means the bracket was
// still open when evidence collection stopped, which is a fact a verifier may
// legitimately observe rather than a builder error.
type contextCompaction struct {
	ID        string
	Started   domain.ContextCompactionStarted
	Completed *domain.ContextCompactionCompleted
	Failed    *domain.ContextCompactionFailed
}

// contextDecision is one Context preparation and the conversation request it
// produced, if that request was recorded.
type contextDecision struct {
	Prepared domain.ContextPreparedRecorded
	Request  *domain.ModelRequestRecorded
}

// auditEventOrder is the canonical position of one parsed event, used only to
// enforce "the preparation came earlier than the request it paired with".
type auditEventOrder struct {
	preparedAt map[string]int
}

func (trace *contextTrace) Compaction(id string) (*contextCompaction, bool) {
	compaction, ok := trace.compactions[id]
	return compaction, ok
}

func (trace *contextTrace) Decision(id string) (*contextDecision, bool) {
	decision, ok := trace.decisions[id]
	return decision, ok
}

func (trace *contextTrace) Checkpoint(id string) (domain.ContextCheckpointRecord, bool) {
	checkpoint, ok := trace.checkpoints[id]
	return checkpoint, ok
}

// CompactionsInOrder and DecisionsInOrder return canonical append order, not
// map order, so a verifier's own detail text and any "first"/"last" reasoning
// is reproducible across runs.
func (trace *contextTrace) CompactionsInOrder() []*contextCompaction {
	ordered := make([]*contextCompaction, 0, len(trace.compactionOrder))
	for _, id := range trace.compactionOrder {
		ordered = append(ordered, trace.compactions[id])
	}
	return ordered
}

func (trace *contextTrace) DecisionsInOrder() []*contextDecision {
	ordered := make([]*contextDecision, 0, len(trace.decisionOrder))
	for _, id := range trace.decisionOrder {
		ordered = append(ordered, trace.decisions[id])
	}
	return ordered
}

func (trace *contextTrace) CheckpointsInOrder() []domain.ContextCheckpointRecord {
	ordered := make([]domain.ContextCheckpointRecord, 0, len(trace.checkpointOrder))
	for _, id := range trace.checkpointOrder {
		ordered = append(ordered, trace.checkpoints[id])
	}
	return ordered
}

// buildContextTrace parses every collected audit entry through reader,
// preserving canonical order, and validates the correlation rules before any
// criterion is allowed to run.
func buildContextTrace(reader *ArtifactReader) (*contextTrace, error) {
	events, ok := readAuditEventsStrict(reader)
	if !ok {
		return nil, fmt.Errorf("context trace: audit evidence is missing or unreadable")
	}

	trace := &contextTrace{
		compactions: map[string]*contextCompaction{},
		decisions:   map[string]*contextDecision{},
		checkpoints: map[string]domain.ContextCheckpointRecord{},
	}
	order := auditEventOrder{preparedAt: map[string]int{}}
	// lastAttempt tracks the highest attempt index already seen for one
	// assistant item, so an out-of-order or repeated attempt is caught.
	lastAttempt := map[string]uint32{}
	// checkpointPredecessor detects a forked chain: two checkpoints claiming
	// the same predecessor means the evidence describes two histories.
	checkpointPredecessor := map[string]string{}

	for index, event := range events {
		switch event.Type {
		case domain.EventContextCompactionStarted:
			var started domain.ContextCompactionStarted
			if err := decodeAuditPayload(event.Data, &started); err != nil {
				return nil, fmt.Errorf("context trace: %s: %w", event.Type, err)
			}
			id := string(started.ID)
			if _, exists := trace.compactions[id]; exists {
				return nil, fmt.Errorf("context trace: compaction %q started twice", id)
			}
			trace.compactions[id] = &contextCompaction{ID: id, Started: started}
			trace.compactionOrder = append(trace.compactionOrder, id)

		case domain.EventContextCompactionCompleted:
			var completed domain.ContextCompactionCompleted
			if err := decodeAuditPayload(event.Data, &completed); err != nil {
				return nil, fmt.Errorf("context trace: %s: %w", event.Type, err)
			}
			compaction, err := trace.closeBracket(string(completed.ID))
			if err != nil {
				return nil, err
			}
			compaction.Completed = &completed
			if err := trace.addCheckpoint(completed.Checkpoint, checkpointPredecessor); err != nil {
				return nil, err
			}

		case domain.EventContextCompactionFailed:
			var failed domain.ContextCompactionFailed
			if err := decodeAuditPayload(event.Data, &failed); err != nil {
				return nil, fmt.Errorf("context trace: %s: %w", event.Type, err)
			}
			compaction, err := trace.closeBracket(string(failed.ID))
			if err != nil {
				return nil, err
			}
			compaction.Failed = &failed

		case domain.EventContextPreparedRecorded:
			var prepared domain.ContextPreparedRecorded
			if err := decodeAuditPayload(event.Data, &prepared); err != nil {
				return nil, fmt.Errorf("context trace: %s: %w", event.Type, err)
			}
			if err := validatePreparedFacts(prepared); err != nil {
				return nil, err
			}
			itemKey := string(prepared.TurnID) + "\x00" + string(prepared.ItemID)
			if previous, seen := lastAttempt[itemKey]; seen && prepared.AttemptIndex <= previous {
				return nil, fmt.Errorf("context trace: item %q attempt index %d does not follow %d",
					prepared.ItemID, prepared.AttemptIndex, previous)
			}
			lastAttempt[itemKey] = prepared.AttemptIndex

			id := string(prepared.ContextDecisionID)
			if id == "" {
				return nil, fmt.Errorf("context trace: a context.prepared record carries no contextDecisionID")
			}
			if _, exists := trace.decisions[id]; exists {
				return nil, fmt.Errorf("context trace: duplicate context decision %q", id)
			}
			trace.decisions[id] = &contextDecision{Prepared: prepared}
			trace.decisionOrder = append(trace.decisionOrder, id)
			order.preparedAt[id] = index

		case domain.EventModelRequestRecorded:
			var request domain.ModelRequestRecorded
			if err := decodeAuditPayload(event.Data, &request); err != nil {
				return nil, fmt.Errorf("context trace: %s: %w", event.Type, err)
			}
			// A summarizer request never goes through Context preparation, so
			// it carries no decision and must not be held to the pairing rule.
			if request.ContextDecisionID == "" {
				continue
			}
			id := string(request.ContextDecisionID)
			decision, exists := trace.decisions[id]
			if !exists {
				return nil, fmt.Errorf("context trace: request names context decision %q with no earlier preparation", id)
			}
			if preparedAt, ok := order.preparedAt[id]; !ok || preparedAt >= index {
				return nil, fmt.Errorf("context trace: request for decision %q precedes its own preparation", id)
			}
			if decision.Prepared.TurnID != request.TurnID || decision.Prepared.ItemID != request.ItemID ||
				decision.Prepared.AttemptIndex != request.AttemptIndex {
				return nil, fmt.Errorf("context trace: decision %q pairs turn/item/attempt %s/%s/%d with request %s/%s/%d",
					id, decision.Prepared.TurnID, decision.Prepared.ItemID, decision.Prepared.AttemptIndex,
					request.TurnID, request.ItemID, request.AttemptIndex)
			}
			if decision.Request != nil {
				return nil, fmt.Errorf("context trace: context decision %q produced more than one request", id)
			}
			decision.Request = &request

		case domain.EventModelUsageRecorded:
			var usage domain.ModelUsageRecorded
			if err := decodeAuditPayload(event.Data, &usage); err != nil {
				return nil, fmt.Errorf("context trace: %s: %w", event.Type, err)
			}
			trace.Usages = append(trace.Usages, usage)
		}
	}
	return trace, nil
}

// closeBracket resolves a terminal compaction event to its own earlier start
// and refuses a second terminal event for the same bracket.
func (trace *contextTrace) closeBracket(id string) (*contextCompaction, error) {
	compaction, exists := trace.compactions[id]
	if !exists {
		return nil, fmt.Errorf("context trace: compaction %q ended with no earlier start", id)
	}
	if compaction.Completed != nil || compaction.Failed != nil {
		return nil, fmt.Errorf("context trace: compaction %q has more than one terminal event", id)
	}
	return compaction, nil
}

func (trace *contextTrace) addCheckpoint(checkpoint domain.ContextCheckpointRecord, predecessors map[string]string) error {
	if checkpoint.ID == "" {
		return fmt.Errorf("context trace: a completed compaction carries a checkpoint with no id")
	}
	if _, exists := trace.checkpoints[checkpoint.ID]; exists {
		return fmt.Errorf("context trace: duplicate checkpoint %q", checkpoint.ID)
	}
	if previous := checkpoint.PreviousCheckpointID; previous != "" {
		for existing, claimed := range predecessors {
			if claimed == previous {
				return fmt.Errorf("context trace: checkpoints %q and %q both claim predecessor %q",
					existing, checkpoint.ID, previous)
			}
		}
		if previous == checkpoint.ID {
			return fmt.Errorf("context trace: checkpoint %q is its own predecessor", checkpoint.ID)
		}
	}
	predecessors[checkpoint.ID] = checkpoint.PreviousCheckpointID
	if err := refuseCheckpointCycle(checkpoint.ID, predecessors); err != nil {
		return err
	}
	trace.checkpoints[checkpoint.ID] = checkpoint
	trace.checkpointOrder = append(trace.checkpointOrder, checkpoint.ID)
	return nil
}

// refuseCheckpointCycle walks the predecessor chain from id. A chain that
// revisits a checkpoint describes a history that cannot have happened.
func refuseCheckpointCycle(id string, predecessors map[string]string) error {
	seen := map[string]bool{}
	for current := id; current != ""; {
		if seen[current] {
			return fmt.Errorf("context trace: checkpoint predecessor chain from %q cycles", id)
		}
		seen[current] = true
		next, ok := predecessors[current]
		if !ok {
			return nil
		}
		current = next
	}
	return nil
}

// validatePreparedFacts enforces the numeric invariants the production
// contract guarantees. Protected-tail is deliberately absent: it is not a
// context.prepared field and a verifier must not invent it.
func validatePreparedFacts(prepared domain.ContextPreparedRecorded) error {
	if prepared.AttemptIndex == 0 {
		return fmt.Errorf("context trace: context.prepared for item %q carries attempt index 0", prepared.ItemID)
	}
	if prepared.BudgetTarget == 0 || prepared.BudgetTrigger == 0 || prepared.BudgetHardInput == 0 {
		return fmt.Errorf("context trace: context.prepared for item %q carries a zero budget (target=%d trigger=%d hardInput=%d)",
			prepared.ItemID, prepared.BudgetTarget, prepared.BudgetTrigger, prepared.BudgetHardInput)
	}
	if !(prepared.BudgetTarget < prepared.BudgetTrigger && prepared.BudgetTrigger < prepared.BudgetHardInput) {
		return fmt.Errorf("context trace: context.prepared for item %q has unordered budgets (target=%d trigger=%d hardInput=%d)",
			prepared.ItemID, prepared.BudgetTarget, prepared.BudgetTrigger, prepared.BudgetHardInput)
	}
	if prepared.MeterID == "" {
		return fmt.Errorf("context trace: context.prepared for item %q names no meter", prepared.ItemID)
	}
	return nil
}

// decodeAuditPayload strictly decodes one audit event payload. Unknown fields
// are tolerated because the audit replica may legitimately be newer than this
// verifier build, but a payload that is not an object at all, or whose typed
// fields do not decode, is a contradiction rather than a tolerable difference.
func decodeAuditPayload(data []byte, target any) error {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return fmt.Errorf("payload is not a JSON object")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("payload did not decode: %w", err)
	}
	return nil
}

// readAuditEventsStrict is readAuditEvents' fail-closed sibling: the existing
// helper stops at the first unparsable line and reports what it managed to
// read, which is right for the old smoke verifier's own contract but wrong
// here. A Context criterion must never be evaluated against a truncated view
// of the evidence.
func readAuditEventsStrict(reader *ArtifactReader) ([]verifierAuditEvent, bool) {
	if reader == nil {
		return nil, false
	}
	entries := reader.Entries("audit")
	if !hasCollectedEntry(entries) {
		return nil, false
	}
	var events []verifierAuditEvent
	for _, entry := range entries {
		if entry.State != EntryCollected {
			continue
		}
		data, err := reader.ReadEntry(entry.Path)
		if err != nil {
			return nil, false
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var envelope verifierAuditEnvelope
			if err := json.Unmarshal(line, &envelope); err != nil {
				return nil, false
			}
			for _, raw := range envelope.Events {
				var event verifierAuditEvent
				if err := json.Unmarshal(raw, &event); err != nil {
					return nil, false
				}
				events = append(events, event)
			}
		}
	}
	return events, true
}
