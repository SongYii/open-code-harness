package contextengine

import (
	"context"
	"errors"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// PageRequest and PageResult mirror the shape of this project's own
// EventStore.ReadStream port
// (internal/harness/application/store.go:97-109) closely enough that
// Application's real adapter (implementation plan Task 9) is a thin,
// mechanical translation — but they are this package's own types, not
// aliases, since contextengine may not import internal/harness/application
// (CE-01).
type PageRequest struct {
	AfterSequence uint64
	Limit         uint32
	HeadVersion   *uint64
}

type PageResult struct {
	Records           []domain.RecordedEvent
	HeadVersion       uint64
	NextAfterSequence uint64
	End               bool
}

// PageSource is what the two-pass bounded scan reads through. Application
// (Task 9) implements it as a thin wrapper over the real EventStore's
// ReadStream port; this package's own tests implement it over an
// in-memory fixture (planner_test.go's fakePageSource).
type PageSource interface {
	ReadPage(ctx context.Context, sessionID domain.SessionID, request PageRequest) (PageResult, error)
}

// ErrHeadMismatch reports that a page arrived carrying a different
// HeadVersion than the one this scan pinned on its first page — a store
// contract violation (design §9.3), never a recoverable condition within
// one scan.
var ErrHeadMismatch = errors.New("contextengine: page head does not match the pinned scan head")

// ScanResult is Pass 1's output (design §9.3): every source ContextUnit
// found across the pinned scan, plus the digest/count over exactly the
// source events that produced them, and the HeadVersion the scan pinned to
// (so a caller needing Pass 2 rereads at the identical head).
type ScanResult struct {
	Units        []ContextUnit
	SourceDigest [32]byte
	CoveredCount uint64
	HeadVersion  uint64
}

// Scan performs the pinned-head, page-by-page read the design calls Pass 1
// (§9.3): the first page fixes HeadVersion for the whole scan; every
// following page is requested against that same pinned value, and
// ErrHeadMismatch fails closed if a page ever disagrees. It reads forward
// to the end of the stream — touching full history is expected and
// bounded only in page count, not skipped (design §22.4 accepts an
// O(history) scan as long as it is paged) — never through a single
// whole-stream call; no function in this package is the "read everything
// in one call" shape ReadWholeStreamPinned is on the Application side
// (internal/harness/application/loop.go:187).
//
// Scope disclosure: this implementation accumulates every fetched record
// before projecting them (via ProjectSourceEvents) once at the end of the
// scan, rather than maintaining a genuinely bounded sliding window of
// ContextUnits incrementally as pages arrive. The design's own stated
// bounded-heap property (Global Constraint: "no live heap scales with
// Session lifetime") is fully delivered by SelectCutPoint's own output —
// only the retained tail, the current open unit, and one below-trigger
// envelope are ever carried forward past planning — but this Scan
// function's own transient working set during the scan itself is O(history
// records read), not O(protectedTail). A streaming, page-boundary-crossing
// incremental projector able to bound Scan's own transient memory too is a
// real refinement Task 16's 10,000-Turn benchmark must weigh, not
// something this task silently claims to already deliver.
func Scan(ctx context.Context, source PageSource, sessionID domain.SessionID, pageLimit uint32) (ScanResult, error) {
	var (
		records     []domain.RecordedEvent
		headVersion uint64
		pinned      bool
		after       uint64
	)
	for {
		var headPointer *uint64
		if pinned {
			headPointer = &headVersion
		}
		page, err := source.ReadPage(ctx, sessionID, PageRequest{AfterSequence: after, Limit: pageLimit, HeadVersion: headPointer})
		if err != nil {
			return ScanResult{}, err
		}
		if !pinned {
			headVersion = page.HeadVersion
			pinned = true
		} else if page.HeadVersion != headVersion {
			return ScanResult{}, ErrHeadMismatch
		}
		records = append(records, page.Records...)
		if page.End {
			break
		}
		after = page.NextAfterSequence
	}

	units, err := ProjectSourceEvents(records)
	if err != nil {
		return ScanResult{}, err
	}
	digest, count, err := ComputeSourceDigest(records)
	if err != nil {
		return ScanResult{}, err
	}
	return ScanResult{Units: units, SourceDigest: digest, CoveredCount: count, HeadVersion: headVersion}, nil
}

// PlanInput is everything SelectCutPoint needs to choose a safe covered
// prefix (design §9.2).
type PlanInput struct {
	// Units is every source ContextUnit in ascending sequence order
	// (Scan's own output, or a fixture in this package's own tests). The
	// currently open assistant item (§9.2 priority 5) is never itself a
	// member of Units — an in-flight item has not produced a committed
	// ContextUnit yet at all — so SelectCutPoint does not need a separate
	// field to exclude it; it is protected structurally by never
	// appearing here in the first place, the same way CurrentInput is.
	Units  []ContextUnit
	Budget Budget
	Meter  Meter
	Tools  []domain.ToolSchema
	// CurrentInput is the not-yet-committed incoming user input (design
	// §7.1's CurrentInputUnit) — never itself a candidate for coverage.
	CurrentInput domain.ModelPromptMessage
	// Force skips the Budget.Trigger comparison and always attempts a cut,
	// as if the estimate already exceeded Trigger — implementation plan
	// Task 10's Provider overflow recovery (design §15.3) needs this: a
	// Provider just rejected the request as too large, which the
	// deterministic meter's own estimate may not have predicted, so
	// recovery cannot wait for the meter to agree pressure exists. Force
	// never changes Target/ProtectedTail/the cut-selection algorithm
	// itself — only whether the early "already under Trigger" return is
	// taken. The zero value (false) preserves every existing caller's
	// behavior unchanged.
	Force bool
}

// PlanResult is SelectCutPoint's decision.
type PlanResult struct {
	// NeedsCompaction is true when Units+CurrentInput+Tools, uncompacted,
	// estimate above Budget.Trigger.
	NeedsCompaction bool
	// CoveredThroughSequence is the LastSequence of the newest unit
	// selected for coverage (replacement by a checkpoint), or 0 if
	// nothing is covered.
	CoveredThroughSequence uint64
	// CoveredUnits are the units selected for coverage, oldest first —
	// exactly the units a caller passes to the summarizer (Task 6).
	CoveredUnits []ContextUnit
	// RetainedUnits are the units NOT covered, in original order — the
	// raw tail materialize.go (Task 6) places after any checkpoint.
	RetainedUnits []ContextUnit
	// EstimatedTokens is the request size if RetainedUnits (not
	// CoveredUnits) plus CurrentInput and Tools were sent as-is, ignoring
	// any checkpoint not yet built — the number a caller compares against
	// Budget.Target/HardInput once a checkpoint's own size is known.
	EstimatedTokens uint64
}

// SelectCutPoint implements the design's cut-selection priority order
// (§9.2), with one disclosed scope narrowing (see the priority 3 note
// below):
//
//  1. retain complete recent Turns — coverage is always snapped to a Turn
//     boundary (a UnitKindTurn), so a Turn is never split between covered
//     and retained;
//  2. if the single newest Turn alone exceeds the tail budget, the
//     snap-to-boundary rule above still stops at that Turn's own start:
//     everything from it forward is retained (its Steps are never
//     separated from their own Turn), which is a safe, conservative
//     answer to "one Turn exceeds the tail budget," not the design's more
//     precise "retain its newest closed Steps only" (an optimization this
//     implementation does not (yet) make);
//  3. NOT IMPLEMENTED: the design additionally allows an earlier closed
//     Step of a still-open (active) Turn to enter coverage once every one
//     of its Tool Calls is terminal, retaining only that Turn's newest
//     Steps rather than the whole Turn. This implementation does not
//     distinguish an active Turn from a completed one at all — priority 1's
//     snap-to-Turn-boundary rule treats every Turn identically, so an
//     active Turn's own earlier closed Steps are always retained in full
//     alongside its still-open item, never partially covered. This is
//     strictly safe (it never covers more than the design allows, only
//     less) but is a real, disclosed gap relative to the full design;
//     closing it needs a way to tell this function which Turn is active,
//     which no caller yet exists to supply (implementation plan Task 9/10);
//  4. never cover CurrentInput without covering the complete earlier
//     portion of its own Turn — the same Turn-boundary snap that
//     implements priority 1 makes this automatic: CurrentInput is never a
//     member of Units, so coverage can only ever reach into fully
//     historical Turns;
//  5. never cover the currently open assistant item — automatic for the
//     same structural reason as priority 4: a not-yet-committed item
//     never appears in Units at all, and the Turn-boundary snap already
//     retains an active Turn's committed Steps in full (priority 3's
//     note above), so there is no separate "open item" case this
//     function needs to special-case.
func SelectCutPoint(input PlanInput) (PlanResult, error) {
	var allMessages []domain.ModelPromptMessage
	for _, unit := range input.Units {
		allMessages = append(allMessages, unit.Messages...)
	}
	allMessages = append(allMessages, currentInputMessages(input.CurrentInput)...)

	estimate := input.Meter.Estimate(Envelope{Messages: allMessages, Tools: input.Tools}).Tokens
	if estimate <= input.Budget.Trigger && !input.Force {
		return PlanResult{
			NeedsCompaction: false,
			RetainedUnits:   input.Units,
			EstimatedTokens: estimate,
		}, nil
	}

	// Walk backward from the newest unit, accumulating tokens until
	// ProtectedTail is met.
	tailTokens := input.Meter.EstimateMessages(currentInputMessages(input.CurrentInput))
	retainFrom := len(input.Units)
	for retainFrom > 0 && tailTokens < input.Budget.ProtectedTail {
		retainFrom--
		tailTokens += input.Meter.EstimateMessages(input.Units[retainFrom].Messages)
	}

	// Priority 1/2/4/5: snap retainFrom backward to the nearest Turn
	// boundary (a UnitKindTurn) so a Turn is never split between covered
	// and retained. Because this snap does not distinguish an active
	// (still-open) Turn from a completed one, it always stops at the
	// newest Turn's own start at the latest — which structurally
	// guarantees priorities 4 and 5 (CurrentInput and any open item are
	// never members of Units in the first place) and conservatively
	// satisfies priority 2 by retaining an oversized Turn whole rather
	// than partially, at the documented cost of not implementing
	// priority 3's finer-grained active-Turn Step coverage (see this
	// function's doc comment).
	for retainFrom > 0 && (retainFrom >= len(input.Units) || input.Units[retainFrom].Kind != UnitKindTurn) {
		retainFrom--
	}

	covered := input.Units[:retainFrom]
	retained := input.Units[retainFrom:]
	retainedMessages := make([]domain.ModelPromptMessage, 0, len(allMessages))
	for _, unit := range retained {
		retainedMessages = append(retainedMessages, unit.Messages...)
	}
	retainedMessages = append(retainedMessages, currentInputMessages(input.CurrentInput)...)

	result := PlanResult{
		NeedsCompaction: true,
		CoveredUnits:    covered,
		RetainedUnits:   retained,
		EstimatedTokens: input.Meter.Estimate(Envelope{Messages: retainedMessages, Tools: input.Tools}).Tokens,
	}
	if len(covered) > 0 {
		result.CoveredThroughSequence = covered[len(covered)-1].LastSequence
	}
	return result, nil
}

func currentInputMessages(message domain.ModelPromptMessage) []domain.ModelPromptMessage {
	if message.Role == "" && message.Text == "" {
		return nil
	}
	return []domain.ModelPromptMessage{message}
}
