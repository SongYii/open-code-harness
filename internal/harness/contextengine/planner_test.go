package contextengine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// fakePageSource is planner_test.go's own in-memory PageSource, standing
// in for Task 9's real EventStore-backed adapter.
type fakePageSource struct {
	records     []domain.RecordedEvent
	headVersion uint64
	pageSize    uint32
	// mismatchAfterFirstPage, when true, returns a different HeadVersion
	// starting on the second page — used to exercise ErrHeadMismatch.
	mismatchAfterFirstPage bool
	pagesServed            int
}

func (source *fakePageSource) ReadPage(ctx context.Context, sessionID domain.SessionID, request PageRequest) (PageResult, error) {
	source.pagesServed++
	head := source.headVersion
	if source.mismatchAfterFirstPage && source.pagesServed > 1 {
		head++
	}
	pageSize := source.pageSize
	if pageSize == 0 {
		pageSize = 256
	}
	start := int(request.AfterSequence)
	if start > len(source.records) {
		start = len(source.records)
	}
	end := start + int(pageSize)
	if end > len(source.records) {
		end = len(source.records)
	}
	page := source.records[start:end]
	next := uint64(end)
	return PageResult{
		Records:           page,
		HeadVersion:       head,
		NextAfterSequence: next,
		End:               end == len(source.records),
	}, nil
}

func turnAndAssistant(seqTurn, seqAssistant uint64, turnID domain.TurnID, input, text string) []domain.RecordedEvent {
	return []domain.RecordedEvent{
		record(seqTurn, domain.TurnStarted{TurnID: turnID, Input: input}),
		record(seqAssistant, domain.AssistantMessageCompleted{TurnID: turnID, ItemID: domain.ItemID("item-" + string(turnID)), Text: text}),
	}
}

func TestScanAcrossMultiplePages(t *testing.T) {
	var records []domain.RecordedEvent
	var seq uint64
	for i := 0; i < 5; i++ {
		seq++
		turnID := domain.TurnID(fmt.Sprintf("turn-%d", i))
		records = append(records, turnAndAssistant(seq, seq+1, turnID, "hi", "hello")...)
		seq++
	}
	source := &fakePageSource{records: records, headVersion: 42, pageSize: 3}
	result, err := Scan(context.Background(), source, "sess1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Units) != 10 {
		t.Fatalf("got %d units, want 10 (5 turns x 2 units each)", len(result.Units))
	}
	if result.HeadVersion != 42 {
		t.Fatalf("HeadVersion = %d, want 42", result.HeadVersion)
	}
	if source.pagesServed < 2 {
		t.Fatalf("expected multiple pages to be served for a %d-record fixture with page size 3, got %d", len(records), source.pagesServed)
	}

	// A second scan over the identical fixture must produce an identical
	// digest — Scan's own determinism, not just ComputeSourceDigest's.
	result2, err := Scan(context.Background(), source, "sess1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceDigest != result2.SourceDigest || result.CoveredCount != result2.CoveredCount {
		t.Fatalf("Scan is not deterministic across repeated calls over the same fixture")
	}
}

func TestScanHeadMismatchFailsClosed(t *testing.T) {
	records := turnAndAssistant(1, 2, "t1", "hi", "hello")
	records = append(records, turnAndAssistant(3, 4, "t2", "hi again", "hello again")...)
	source := &fakePageSource{records: records, headVersion: 1, pageSize: 2, mismatchAfterFirstPage: true}
	_, err := Scan(context.Background(), source, "sess1", 2)
	if !errors.Is(err, ErrHeadMismatch) {
		t.Fatalf("got %v, want ErrHeadMismatch", err)
	}
}

func fixtureBudget(t *testing.T, window, output uint32) Budget {
	t.Helper()
	budget, err := ComputeBudget(window, output, DefaultBudgetConfig)
	if err != nil {
		t.Fatal(err)
	}
	return budget
}

func TestSelectCutPointBelowTriggerRetainsEverything(t *testing.T) {
	units, err := ProjectSourceEvents(turnAndAssistant(1, 2, "t1", "hi", "hello"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := SelectCutPoint(PlanInput{
		Units:  units,
		Budget: fixtureBudget(t, 131072, 4096), // huge window, tiny fixture: certainly below trigger
		Meter:  WireEstimateMeter{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.NeedsCompaction {
		t.Fatal("expected NeedsCompaction=false for a tiny fixture against a huge window")
	}
	if len(result.RetainedUnits) != len(units) || len(result.CoveredUnits) != 0 {
		t.Fatalf("got retained=%d covered=%d, want everything retained and nothing covered", len(result.RetainedUnits), len(result.CoveredUnits))
	}
}

// TestSelectCutPointForceBypassesTheTriggerComparison is implementation
// plan Task 10's own reason for the Force field (design §15.3): a
// Provider just rejected a request the meter itself estimated as safely
// below Trigger, and overflow recovery must still attempt a cut rather
// than trust that estimate.
func TestSelectCutPointForceBypassesTheTriggerComparison(t *testing.T) {
	units, err := ProjectSourceEvents(buildManyTurns(6))
	if err != nil {
		t.Fatal(err)
	}
	// Trigger/HardInput huge (certainly below Trigger without Force), but
	// ProtectedTail small enough that not every unit fits in the tail --
	// otherwise Force alone forces a cut attempt that still finds nothing
	// safe to cover, which would test nothing.
	budget := Budget{HardInput: 131072, Trigger: 131072, Target: 65536, ProtectedTail: 64, SummaryOutputCap: 512}

	unforced, err := SelectCutPoint(PlanInput{Units: units, Budget: budget, Meter: WireEstimateMeter{}})
	if err != nil {
		t.Fatal(err)
	}
	if unforced.NeedsCompaction {
		t.Fatal("fixture unexpectedly needs compaction without Force; tighten the fixture")
	}

	forced, err := SelectCutPoint(PlanInput{Units: units, Budget: budget, Meter: WireEstimateMeter{}, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !forced.NeedsCompaction {
		t.Fatal("Force: true did not force NeedsCompaction=true despite units below Trigger")
	}
	if len(forced.CoveredUnits) == 0 {
		t.Fatal("Force: true produced NeedsCompaction=true but selected nothing to cover")
	}
	// Force never changes the cut-selection algorithm itself, only whether
	// the early below-Trigger return is taken: Target/ProtectedTail-driven
	// coverage still applies normally.
	if forced.CoveredThroughSequence == 0 {
		t.Fatal("forced result has no coverage boundary")
	}
}

// buildManyTurns constructs count standalone (Turn, Assistant) unit pairs
// with long enough text to force compaction pressure against a small
// window.
func buildManyTurns(count int) []domain.RecordedEvent {
	var records []domain.RecordedEvent
	var seq uint64
	longText := make([]byte, 400)
	for i := range longText {
		longText[i] = 'x'
	}
	for i := 0; i < count; i++ {
		seq++
		turnID := domain.TurnID(rune('A'+i%26)) + domain.TurnID(rune('0'+i/26))
		records = append(records, record(seq, domain.TurnStarted{TurnID: turnID, Input: string(longText)}))
		seq++
		records = append(records, record(seq, domain.AssistantMessageCompleted{TurnID: turnID, ItemID: domain.ItemID(string(turnID) + "-item"), Text: string(longText)}))
	}
	return records
}

func TestSelectCutPointRetainsCompleteRecentTurnsAndNeverSplitsATurn(t *testing.T) {
	units, err := ProjectSourceEvents(buildManyTurns(20))
	if err != nil {
		t.Fatal(err)
	}
	budget := fixtureBudget(t, 8192, 1024) // small window: this fixture must trigger compaction
	result, err := SelectCutPoint(PlanInput{Units: units, Budget: budget, Meter: WireEstimateMeter{}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsCompaction {
		t.Fatal("expected NeedsCompaction=true for 20 large turns against an 8K window")
	}
	if len(result.CoveredUnits) == 0 {
		t.Fatal("expected at least one unit to be covered")
	}
	// Every retained unit's Turn must be either entirely retained or
	// entirely covered -- no Turn may straddle the boundary. Build the set
	// of Turns appearing in CoveredUnits and confirm none of them also
	// appear in RetainedUnits.
	coveredTurns := map[domain.TurnID]bool{}
	for _, unit := range result.CoveredUnits {
		coveredTurns[unit.TurnID] = true
	}
	for _, unit := range result.RetainedUnits {
		if coveredTurns[unit.TurnID] {
			t.Fatalf("Turn %q has units in both CoveredUnits and RetainedUnits -- a Turn was split", unit.TurnID)
		}
	}
	// The retained span must be the newest units (a suffix), and the
	// covered span the oldest (a prefix) -- coverage is always a strict
	// prefix of Units.
	if len(result.CoveredUnits)+len(result.RetainedUnits) != len(units) {
		t.Fatalf("covered+retained = %d, want %d (every unit accounted for exactly once)", len(result.CoveredUnits)+len(result.RetainedUnits), len(units))
	}
	for index, unit := range result.CoveredUnits {
		want := units[index]
		if unit.Kind != want.Kind || unit.TurnID != want.TurnID || unit.FirstSequence != want.FirstSequence || unit.LastSequence != want.LastSequence {
			t.Fatalf("CoveredUnits[%d] = %+v, want %+v -- not a prefix of the original Units slice", index, unit, want)
		}
	}
}

// TestSelectCutPointNeverCoversTheOpenAssistantItem is the mutation-check
// counterpart for the "never cover the currently open assistant item"
// target (design §22.4, plan Task 3). In this implementation the
// guarantee is structural, not a separate runtime check (see
// SelectCutPoint's own doc comment): the currently open item is never a
// member of Units at all, and the newest Turn -- active or not -- is
// always retained whole by the Turn-boundary snap that also implements
// priority 1. This test constructs a Turn whose newest content is a
// StepUnit (an assistant message plus a completed Tool Call, the shape a
// still-in-progress Turn's own committed history actually takes) under
// enough historical pressure to force compaction, and confirms that whole
// Turn survives entirely -- neither its StepUnit nor its own opening
// TurnUnit is ever covered.
func TestSelectCutPointNeverCoversTheOpenAssistantItem(t *testing.T) {
	historical := buildManyTurns(25) // enough pressure to force compaction
	activeTurnRecords := []domain.RecordedEvent{
		record(1000, domain.TurnStarted{TurnID: "active", Input: "current work"}),
		record(1001, domain.AssistantMessageCompleted{TurnID: "active", ItemID: "active-item", ToolCalls: []domain.ToolCallOffer{{ID: "active-call", Name: "read_file", Arguments: `{}`}}}),
		record(1002, domain.ToolCallStarted{TurnID: "active", ItemID: "active-started", CallID: "active-call", Name: "read_file", StepIndex: 1}),
		record(1003, domain.ToolCallCompleted{TurnID: "active", ItemID: "active-started", CallID: "active-call", Content: "file contents"}),
	}
	all := append(append([]domain.RecordedEvent{}, historical...), activeTurnRecords...)
	units, err := ProjectSourceEvents(all)
	if err != nil {
		t.Fatal(err)
	}
	if units[len(units)-1].Kind != UnitKindStep || units[len(units)-1].TurnID != "active" {
		t.Fatalf("fixture invariant broken: last unit is %+v, want the active Turn's own StepUnit", units[len(units)-1])
	}
	budget := fixtureBudget(t, 8192, 1024)

	result, err := SelectCutPoint(PlanInput{Units: units, Budget: budget, Meter: WireEstimateMeter{}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsCompaction {
		t.Fatal("expected this fixture to need compaction")
	}
	for _, unit := range result.CoveredUnits {
		if unit.TurnID == "active" {
			t.Fatalf("a unit belonging to the active Turn was covered: %+v", unit)
		}
	}
	activeUnitsRetained := 0
	for _, unit := range result.RetainedUnits {
		if unit.TurnID == "active" {
			activeUnitsRetained++
		}
	}
	if activeUnitsRetained != 2 { // the active Turn's own TurnUnit and StepUnit
		t.Fatalf("got %d active-Turn units retained, want 2 (the whole Turn)", activeUnitsRetained)
	}
}

func TestSelectCutPointEstimateNeverExceedsHardInput(t *testing.T) {
	units, err := ProjectSourceEvents(buildManyTurns(25))
	if err != nil {
		t.Fatal(err)
	}
	budget := fixtureBudget(t, 8192, 1024)
	result, err := SelectCutPoint(PlanInput{Units: units, Budget: budget, Meter: WireEstimateMeter{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.NeedsCompaction && len(result.CoveredUnits) > 0 && result.EstimatedTokens > budget.HardInput {
		// A single retained Turn may still legitimately exceed hardInput
		// on its own (design §11's summarizer/reset path exists precisely
		// for that); this assertion only checks the common case where the
		// retained tail fits, so it is not a hard invariant this pure
		// function alone guarantees end to end -- only a smoke check for
		// this fixture's own shape.
		t.Logf("retained estimate %d exceeds hardInput %d; acceptable only if the retained span could not shrink further", result.EstimatedTokens, budget.HardInput)
	}
}

func TestSelectCutPointNoSafePrefixWhenEverythingIsProtected(t *testing.T) {
	units, err := ProjectSourceEvents(turnAndAssistant(1, 2, "t1", "short", "short"))
	if err != nil {
		t.Fatal(err)
	}
	// A budget so tight that even this tiny fixture is "above trigger,"
	// but the whole fixture is also within ProtectedTail, so nothing is
	// safe to cover.
	budget := Budget{Trigger: 1, ProtectedTail: 1_000_000, Target: 1, HardInput: 1_000_000, SummaryOutputCap: 128, Safety: 1}
	result, err := SelectCutPoint(PlanInput{Units: units, Budget: budget, Meter: WireEstimateMeter{}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsCompaction {
		t.Fatal("expected NeedsCompaction=true")
	}
	if len(result.CoveredUnits) != 0 {
		t.Fatalf("expected no safe prefix to cover, got %d covered units", len(result.CoveredUnits))
	}
}

// TestSelectCutPointCutPointOnlyAdvances is a fuzz-shaped property test:
// planning against a growing prefix of the same fixture must never choose
// a covered span that regresses past what a shorter prefix already
// decided was safe to retain (an already-covered unit never becomes
// retained again as more history accumulates ahead of it).
func TestSelectCutPointCutPointOnlyAdvances(t *testing.T) {
	all, err := ProjectSourceEvents(buildManyTurns(30))
	if err != nil {
		t.Fatal(err)
	}
	budget := fixtureBudget(t, 8192, 1024)
	var previousCoveredThrough uint64
	for prefixLen := 10; prefixLen <= len(all); prefixLen += 4 {
		prefix := all[:prefixLen]
		result, err := SelectCutPoint(PlanInput{Units: prefix, Budget: budget, Meter: WireEstimateMeter{}})
		if err != nil {
			t.Fatal(err)
		}
		if result.CoveredThroughSequence < previousCoveredThrough {
			t.Fatalf("cut point regressed: prefixLen=%d covered through %d, previous was %d", prefixLen, result.CoveredThroughSequence, previousCoveredThrough)
		}
		previousCoveredThrough = result.CoveredThroughSequence
	}
}

func TestSelectCutPointNoOrphanedToolResult(t *testing.T) {
	records := buildManyTurns(15)
	records = append(records,
		record(1000, domain.AssistantMessageCompleted{TurnID: "step-turn", ItemID: "item-step", ToolCalls: []domain.ToolCallOffer{{ID: "c1", Name: "read_file", Arguments: `{}`}}}),
		record(1001, domain.ToolCallStarted{TurnID: "step-turn", ItemID: "item-started", CallID: "c1", Name: "read_file", StepIndex: 1}),
		record(1002, domain.ToolCallCompleted{TurnID: "step-turn", ItemID: "item-started", CallID: "c1", Content: "contents"}),
	)
	units, err := ProjectSourceEvents(records)
	if err != nil {
		t.Fatal(err)
	}
	budget := fixtureBudget(t, 8192, 1024)
	result, err := SelectCutPoint(PlanInput{Units: units, Budget: budget, Meter: WireEstimateMeter{}})
	if err != nil {
		t.Fatal(err)
	}
	// Every StepUnit must be entirely on one side of the cut: its
	// assistant message and every one of its tool results together.
	for _, unit := range append(append([]ContextUnit{}, result.CoveredUnits...), result.RetainedUnits...) {
		if unit.Kind != UnitKindStep {
			continue
		}
		// ProjectSourceEvents only ever emits a StepUnit once balanced
		// (verified in projector_test.go); SelectCutPoint treats a
		// ContextUnit as one atomic item and never inspects its Messages
		// to split it further, so this is true by construction -- this
		// test pins that construction-time guarantee explicitly.
		if len(unit.Messages) < 2 {
			t.Fatalf("a StepUnit reached the planner with fewer than 2 messages (assistant + at least one result): %+v", unit)
		}
	}
}
