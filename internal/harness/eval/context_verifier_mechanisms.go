package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// Verifier IDs for the mechanisms whose evidence spans more than one event.
const (
	VerifierContextMidTurn          = "context-mid-turn-v1"
	VerifierContextToolResultPruned = "context-tool-result-pruned-v1"
	VerifierContextOverflowRecover  = "context-overflow-recovered-v1"
	VerifierContextMultiChunk       = "context-multi-chunk-summary-v1"
	VerifierContextUsageAnchor      = "context-usage-anchor-v1"
)

// Fixture protocol coordinates the verifiers correlate against. They mirror
// cmd/och-eval's own fixture constants; eval must not import that command,
// and duplicating them keeps a change on either side from passing unnoticed
// on both at once.
const (
	contextPruneSuccessSentinel = "OCH_EVAL_CONTEXT_PRUNE_PROJECTION_OK"
	contextPostCompactionResult = "OCH_EVAL_CONTEXT_POST_COMPACTION_OK"
	contextFixtureFailureMarker = "OCH_EVAL_CONTEXT_FIXTURE_CONTRACT_FAILED"

	projectedToolResultOpen  = "[tool result projected by Open Code Harness]"
	projectedToolResultClose = "[end projected tool result]"
)

var (
	projectedOriginalBytesPattern = regexp.MustCompile(`original_bytes: (\d+)`)
	projectedDigestPattern        = regexp.MustCompile(`sha256: ([0-9a-f]{64})`)
	fixtureChunkDepthPattern      = regexp.MustCompile(`fixture_chunk_depth: (\d+)`)
)

// assistantTextCarries reports whether any completed assistant message
// carries marker. It is how a verifier confirms the fixture's own independent
// judgement reached the Session.
func assistantTextCarries(trace *contextTrace, marker string) bool {
	for _, assistant := range trace.Assistants {
		if strings.Contains(assistant.Text, marker) {
			return true
		}
	}
	return false
}

// verifyContextMidTurn requires a second preparation on the same Turn, after
// a Tool Result, using the mid_turn trigger. Preparation happening again is
// not enough: it must be the mid-turn one, and it must genuinely follow an
// earlier preparation on that same Turn.
//
// It deliberately does not require attempt index 2. AttemptIndex is scoped to
// one assistant item, and production emits the mid-turn continuation as a new
// item on the same Turn, so its index is 1. Index 2 identifies a second
// attempt at the same item, which is the overflow-retry shape — a different
// mechanism with its own criterion. Requiring index 2 here would have made
// this criterion unsatisfiable against real evidence.
func verifyContextMidTurn(reader *ArtifactReader, _ Scenario) CriterionResult {
	return contextCriterion(VerifierContextMidTurn, reader, func(trace *contextTrace) CriterionResult {
		seenOnTurn := map[domain.TurnID]int{}
		for _, decision := range trace.DecisionsInOrder() {
			prepared := decision.Prepared
			if prepared.Trigger != domain.ContextTriggerMidTurn {
				seenOnTurn[prepared.TurnID]++
				continue
			}
			if seenOnTurn[prepared.TurnID] == 0 {
				return failed(VerifierContextMidTurn, fmt.Sprintf(
					"mid_turn preparation %q is the first preparation on turn %s, so nothing preceded it",
					prepared.ContextDecisionID, prepared.TurnID))
			}
			if decision.Request == nil {
				return failed(VerifierContextMidTurn, fmt.Sprintf(
					"mid_turn preparation %q dispatched no request", prepared.ContextDecisionID))
			}
			if !requestCarriesToolMessage(*decision.Request) {
				return failed(VerifierContextMidTurn, fmt.Sprintf(
					"mid_turn request for %q carries no Tool Result", prepared.ContextDecisionID))
			}
			return passed(VerifierContextMidTurn, fmt.Sprintf(
				"mid_turn preparation %q followed %d earlier preparation(s) on turn %s and carried a Tool Result",
				prepared.ContextDecisionID, seenOnTurn[prepared.TurnID], prepared.TurnID))
		}
		return failed(VerifierContextMidTurn, fmt.Sprintf(
			"no %s preparation among %d decisions", domain.ContextTriggerMidTurn, len(trace.DecisionsInOrder())))
	})
}

func requestCarriesToolMessage(request domain.ModelRequestRecorded) bool {
	for _, message := range request.Messages {
		if message.Role == "tool" {
			return true
		}
	}
	return false
}

// verifyContextToolResultPruned enforces every one of the design's six
// pruning evidence checks. Configuration such as a positive
// maxPrunedToolResultsPerRequest is never proof; each check below reads an
// observed fact.
func verifyContextToolResultPruned(reader *ArtifactReader, _ Scenario) CriterionResult {
	return contextCriterion(VerifierContextToolResultPruned, reader, func(trace *contextTrace) CriterionResult {
		for _, decision := range trace.DecisionsInOrder() {
			prepared := decision.Prepared
			if prepared.PrunedToolResultCount == 0 || decision.Request == nil {
				continue
			}
			projected, callID, ok := projectedToolMessage(*decision.Request)
			if !ok {
				return failed(VerifierContextToolResultPruned, fmt.Sprintf(
					"decision %q reports %d projected Tool Results but its request carries no projected frame",
					prepared.ContextDecisionID, prepared.PrunedToolResultCount))
			}
			if callID == "" {
				return failed(VerifierContextToolResultPruned, fmt.Sprintf(
					"the projected Tool Result for decision %q lost its original tool call id", prepared.ContextDecisionID))
			}
			if !strings.Contains(projected, projectedToolResultClose) {
				return failed(VerifierContextToolResultPruned, fmt.Sprintf(
					"the projected frame for decision %q does not close", prepared.ContextDecisionID))
			}
			originalBytes, digest, err := projectedFrameFacts(projected)
			if err != nil {
				return failed(VerifierContextToolResultPruned, fmt.Sprintf(
					"decision %q: %s", prepared.ContextDecisionID, err.Error()))
			}
			if uint64(len(projected)) >= originalBytes {
				return failed(VerifierContextToolResultPruned, fmt.Sprintf(
					"the projected frame for decision %q is %d bytes against an original of %d",
					prepared.ContextDecisionID, len(projected), originalBytes))
			}
			if prepared.EstimatedTotalTokens > prepared.BudgetHardInput {
				return failed(VerifierContextToolResultPruned, fmt.Sprintf(
					"decision %q estimated %d tokens against hard input %d",
					prepared.ContextDecisionID, prepared.EstimatedTotalTokens, prepared.BudgetHardInput))
			}
			if matched, detail := workspaceFileMatches(reader, originalBytes, digest); !matched {
				return failed(VerifierContextToolResultPruned, fmt.Sprintf(
					"decision %q: %s", prepared.ContextDecisionID, detail))
			}
			if !assistantTextCarries(trace, contextPruneSuccessSentinel) {
				return failed(VerifierContextToolResultPruned,
					"the fixture never confirmed it received the complete projected frame")
			}
			return passed(VerifierContextToolResultPruned, fmt.Sprintf(
				"decision %q projected %d Tool Result(s); frame kept call id %q over %d original bytes and the fixture confirmed it",
				prepared.ContextDecisionID, prepared.PrunedToolResultCount, callID, originalBytes))
		}
		return failed(VerifierContextToolResultPruned,
			fmt.Sprintf("no decision among %d reported a projected Tool Result", len(trace.DecisionsInOrder())))
	})
}

func projectedToolMessage(request domain.ModelRequestRecorded) (text, callID string, ok bool) {
	for _, message := range request.Messages {
		if message.Role == "tool" && strings.Contains(message.Text, projectedToolResultOpen) {
			return message.Text, message.ToolCallID, true
		}
	}
	return "", "", false
}

func projectedFrameFacts(projected string) (originalBytes uint64, digest string, err error) {
	sizeMatch := projectedOriginalBytesPattern.FindStringSubmatch(projected)
	if sizeMatch == nil {
		return 0, "", fmt.Errorf("the projected frame declares no original_bytes")
	}
	parsed, parseErr := strconv.ParseUint(sizeMatch[1], 10, 64)
	if parseErr != nil || parsed == 0 {
		return 0, "", fmt.Errorf("the projected frame declares a malformed original_bytes")
	}
	digestMatch := projectedDigestPattern.FindStringSubmatch(projected)
	if digestMatch == nil {
		return 0, "", fmt.Errorf("the projected frame declares no sha256")
	}
	return parsed, digestMatch[1], nil
}

// workspaceFileMatches confirms the projected frame's declared size and
// digest name a file this Attempt actually collected. It is what stops a
// frame from claiming to summarize content that was never read.
func workspaceFileMatches(reader *ArtifactReader, originalBytes uint64, digest string) (bool, string) {
	entries := reader.Entries("workspace")
	if !hasCollectedEntry(entries) {
		return false, "no collected workspace evidence to check the projected frame against"
	}
	for _, entry := range entries {
		if entry.State != EntryCollected || uint64(entry.ByteLength) != originalBytes {
			continue
		}
		data, err := reader.ReadEntry(entry.Path)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) == digest {
			return true, ""
		}
	}
	return false, fmt.Sprintf("no collected workspace file matches the projected frame (%d bytes, sha256 %s…)",
		originalBytes, digest[:12])
}

// verifyContextOverflowRecovered requires a real provider rejection to have
// been recovered: two attempts on one item, an overflow_retry compaction, a
// strictly smaller retry estimate, and a Turn that finished.
func verifyContextOverflowRecovered(reader *ArtifactReader, _ Scenario) CriterionResult {
	return contextCriterion(VerifierContextOverflowRecover, reader, func(trace *contextTrace) CriterionResult {
		retries := completedCompactionsByTrigger(trace, domain.ContextTriggerOverflowRetry)
		if len(retries) == 0 {
			return failed(VerifierContextOverflowRecover, fmt.Sprintf(
				"no completed %s compaction among %d brackets",
				domain.ContextTriggerOverflowRetry, len(trace.CompactionsInOrder())))
		}
		if len(retries) > 1 {
			return failed(VerifierContextOverflowRecover, fmt.Sprintf(
				"%d overflow_retry compactions, want exactly one recoverable overflow", len(retries)))
		}
		first, second, ok := firstTwoAttempts(trace)
		if !ok {
			return failed(VerifierContextOverflowRecover,
				"no assistant item carries both an initial attempt and a retry")
		}
		if second.Prepared.EstimatedTotalTokens >= first.Prepared.EstimatedTotalTokens {
			return failed(VerifierContextOverflowRecover, fmt.Sprintf(
				"retry estimate %d did not shrink below the initial %d",
				second.Prepared.EstimatedTotalTokens, first.Prepared.EstimatedTotalTokens))
		}
		if len(trace.CompletedTurns) == 0 {
			return failed(VerifierContextOverflowRecover, "no Turn completed after the overflow retry")
		}
		if !assistantTextCarries(trace, contextPostCompactionResult) {
			return failed(VerifierContextOverflowRecover,
				"the post-compaction request never reached the fixture's own success sentinel")
		}
		return passed(VerifierContextOverflowRecover, fmt.Sprintf(
			"overflow on item %s recovered: attempt 1 estimated %d, retry estimated %d, Turn completed",
			first.Prepared.ItemID, first.Prepared.EstimatedTotalTokens, second.Prepared.EstimatedTotalTokens))
	})
}

func completedCompactionsByTrigger(trace *contextTrace, trigger string) []*contextCompaction {
	var matched []*contextCompaction
	for _, compaction := range trace.CompactionsInOrder() {
		if compaction.Started.Trigger == trigger && compaction.Completed != nil {
			matched = append(matched, compaction)
		}
	}
	return matched
}

// firstTwoAttempts returns the first item that carries attempts 1 and 2.
func firstTwoAttempts(trace *contextTrace) (first, second *contextDecision, ok bool) {
	byItem := map[string][]*contextDecision{}
	var order []string
	for _, decision := range trace.DecisionsInOrder() {
		key := string(decision.Prepared.TurnID) + "\x00" + string(decision.Prepared.ItemID)
		if _, seen := byItem[key]; !seen {
			order = append(order, key)
		}
		byItem[key] = append(byItem[key], decision)
	}
	for _, key := range order {
		decisions := byItem[key]
		if len(decisions) >= 2 && decisions[0].Prepared.AttemptIndex == 1 && decisions[1].Prepared.AttemptIndex == 2 {
			return decisions[0], decisions[1], true
		}
	}
	return nil, nil, false
}

// verifyContextMultiChunk requires a rolling checkpoint built from at least
// two summarizer calls, cross-checked against the fixture's own independently
// carried depth. Matching depth is what proves each later summarizer request
// actually received the previous chunk's output rather than starting over.
func verifyContextMultiChunk(reader *ArtifactReader, _ Scenario) CriterionResult {
	return contextCriterion(VerifierContextMultiChunk, reader, func(trace *contextTrace) CriterionResult {
		var best domain.ContextCheckpointRecord
		found := false
		for _, checkpoint := range trace.CheckpointsInOrder() {
			if checkpoint.Kind != domain.ContextCheckpointKindRollingSummary {
				continue
			}
			if !found || checkpoint.SummaryChunks > best.SummaryChunks {
				best, found = checkpoint, true
			}
		}
		if !found {
			return failed(VerifierContextMultiChunk, "no rolling summary checkpoint was established")
		}
		if best.SummaryChunks < 2 {
			return failed(VerifierContextMultiChunk, fmt.Sprintf(
				"checkpoint %q records %d summary chunk(s), want at least 2", best.ID, best.SummaryChunks))
		}
		if strings.TrimSpace(best.Summary) == "" {
			return failed(VerifierContextMultiChunk,
				fmt.Sprintf("multi-chunk checkpoint %q carries an empty summary", best.ID))
		}
		depthMatch := fixtureChunkDepthPattern.FindStringSubmatch(best.Summary)
		if depthMatch == nil {
			return failed(VerifierContextMultiChunk, fmt.Sprintf(
				"checkpoint %q carries no fixture_chunk_depth to cross-check its %d chunks",
				best.ID, best.SummaryChunks))
		}
		depth, err := strconv.ParseUint(depthMatch[1], 10, 32)
		if err != nil {
			return failed(VerifierContextMultiChunk,
				fmt.Sprintf("checkpoint %q carries a malformed fixture_chunk_depth", best.ID))
		}
		if uint32(depth) != best.SummaryChunks {
			return failed(VerifierContextMultiChunk, fmt.Sprintf(
				"checkpoint %q records %d chunks but the fixture observed depth %d",
				best.ID, best.SummaryChunks, depth))
		}
		return passed(VerifierContextMultiChunk, fmt.Sprintf(
			"checkpoint %q rolled through %d summarizer calls, matching the fixture's own observed depth",
			best.ID, best.SummaryChunks))
	})
}

// verifyContextUsageAnchor requires the non-lowering provider-usage anchor to
// have actually decided a later preparation. A high fixture usage number or
// enabled configuration alone cannot pass: the applied anchor must be
// recorded, must not lower the plain estimate, and a pre-turn compaction must
// have followed.
func verifyContextUsageAnchor(reader *ArtifactReader, _ Scenario) CriterionResult {
	return contextCriterion(VerifierContextUsageAnchor, reader, func(trace *contextTrace) CriterionResult {
		for _, decision := range trace.DecisionsInOrder() {
			prepared := decision.Prepared
			if !prepared.UsageAnchorApplied {
				continue
			}
			if prepared.UsageAnchorTokens == 0 {
				return failed(VerifierContextUsageAnchor, fmt.Sprintf(
					"decision %q applied a usage anchor of zero tokens", prepared.ContextDecisionID))
			}
			if prepared.UsageAnchorTokens < prepared.EstimatedMessageTokens {
				return failed(VerifierContextUsageAnchor, fmt.Sprintf(
					"decision %q anchored at %d tokens, below its own message estimate of %d",
					prepared.ContextDecisionID, prepared.UsageAnchorTokens, prepared.EstimatedMessageTokens))
			}
			anchor, ok := eligibleAnchorBefore(trace, prepared)
			if !ok {
				return failed(VerifierContextUsageAnchor, fmt.Sprintf(
					"decision %q applied an anchor with no earlier provider usage record to establish it",
					prepared.ContextDecisionID))
			}
			if anchor < prepared.UsageAnchorTokens {
				return failed(VerifierContextUsageAnchor, fmt.Sprintf(
					"decision %q anchored at %d tokens above the highest earlier provider usage of %d",
					prepared.ContextDecisionID, prepared.UsageAnchorTokens, anchor))
			}
			if len(completedCompactionsByTrigger(trace, domain.ContextTriggerPreTurn)) == 0 {
				return failed(VerifierContextUsageAnchor,
					"the applied usage anchor did not lead to a completed pre-turn compaction")
			}
			return passed(VerifierContextUsageAnchor, fmt.Sprintf(
				"decision %q applied a non-lowering anchor of %d tokens from an earlier provider usage of %d, and pre-turn compaction followed",
				prepared.ContextDecisionID, prepared.UsageAnchorTokens, anchor))
		}
		return failed(VerifierContextUsageAnchor, fmt.Sprintf(
			"no decision among %d recorded an applied usage anchor", len(trace.DecisionsInOrder())))
	})
}

// eligibleAnchorBefore returns the highest provider input-token count
// recorded before this preparation's own item, which is the anchor the
// Context Engine was entitled to use.
func eligibleAnchorBefore(trace *contextTrace, prepared domain.ContextPreparedRecorded) (uint64, bool) {
	var highest uint64
	found := false
	for _, usage := range trace.Usages {
		if usage.TurnID == prepared.TurnID && usage.ItemID == prepared.ItemID {
			continue
		}
		if usage.InputTokens > highest {
			highest, found = usage.InputTokens, true
		}
	}
	return highest, found
}
