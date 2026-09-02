package contextengine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// This file's benchmarks measure design §22.4's own required properties at
// 100/1,000/10,000-Turn scale (10x steps).
//
// BenchmarkScan and BenchmarkSelectCutPoint measure the ORIGINAL gap this
// task's own investigation found: called with afterSequence == 0 (a full
// scan from the beginning of the stream, e.g. this package's own
// fixtures below, or a real caller's rare "checkpoint failed replay
// validation" fallback), both re-read/re-flatten EVERY canonical source
// record and scale linearly with Turn count (100 -> 1,000 turns is roughly
// a 10x jump in bytes/op for both) -- a real, disclosed cost that remains
// exactly this expensive on that specific, no-longer-common path.
// BenchmarkScanFromCheckpoint below measures the fix: Scan's own
// afterSequence parameter (planner.go) lets a caller holding a checkpoint
// resume scanning from its own Coverage.ThroughSequence -- a genuine Turn
// boundary -- instead of always starting at the beginning of the stream,
// which is what Application's PrepareContext/CompactSession now do
// whenever a usable checkpoint exists. BenchmarkScanFromCheckpoint holds
// the *distance since the last checkpoint* fixed while the *total* history
// preceding it grows 100x, and shows flat cost -- the "live heap bounded
// by budget, not Turn count" property design §22.4 requires, now delivered
// on the common steady-state path, not merely on BenchmarkMaterialize's
// own output side. This does not change BenchmarkScan/BenchmarkSelectCutPoint's
// own numbers below: they intentionally keep measuring the afterSequence
// == 0 case, since that path's cost is real and unchanged.

// syntheticHistory builds turnCount Turns' worth of canonical source
// records (TurnStarted + AssistantMessageCompleted per Turn, a modest
// realistic exchange length) -- design §22.4's 100/1,000/10,000-Turn
// benchmark streams.
func syntheticHistory(turnCount int) []domain.RecordedEvent {
	input := strings.Repeat("what should I do next? ", 8)         // ~50 wire tokens
	reply := strings.Repeat("here is my suggestion for it. ", 12) // ~90 wire tokens
	records := make([]domain.RecordedEvent, 0, turnCount*2)
	seq := uint64(0)
	for i := 0; i < turnCount; i++ {
		seq++
		turnID := domain.TurnID(fmt.Sprintf("turn-%d", i))
		records = append(records, record(seq, domain.TurnStarted{TurnID: turnID, Input: input}))
		seq++
		records = append(records, record(seq, domain.AssistantMessageCompleted{
			TurnID: turnID, ItemID: domain.ItemID(fmt.Sprintf("item-%d", i)), Text: reply,
		}))
	}
	return records
}

// BenchmarkScan measures Pass 1's own transient cost as history grows.
// Design §22.4 requires below-trigger live heap bounded by the configured
// budget, not Turn count; Scan's own doc comment (planner.go) already
// discloses that its OWN transient working set is O(history records read)
// -- this benchmark is that disclosed property's actual measurement, not a
// proof it is already bounded.
func BenchmarkScan(b *testing.B) {
	for _, turnCount := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("turns=%d", turnCount), func(b *testing.B) {
			records := syntheticHistory(turnCount)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				source := &fakePageSource{records: records, headVersion: uint64(len(records)), pageSize: 256}
				if _, err := Scan(context.Background(), source, "bench-session", 256, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkScanFromCheckpoint measures the same Pass 1 read with a
// checkpoint already covering everything except a fixed-size recent tail
// (protectedTailTurns), as afterSequence lets Scan resume from that
// checkpoint's own boundary instead of the beginning of the stream. Unlike
// BenchmarkScan above, the per-op cost here is expected to stay FLAT as
// turnCount (the history BEFORE the checkpoint) grows 100x, since none of
// that history is read at all -- this is the steady-state cost a
// long-lived, regularly compacted session actually pays on the pre-turn
// planning path today.
func BenchmarkScanFromCheckpoint(b *testing.B) {
	const protectedTailTurns = 5
	for _, turnCount := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("turns=%d", turnCount), func(b *testing.B) {
			records := syntheticHistory(turnCount)
			afterSequence := uint64(len(records) - protectedTailTurns*2)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				source := &fakePageSource{records: records, headVersion: uint64(len(records)), pageSize: 256}
				if _, err := Scan(context.Background(), source, "bench-session", 256, afterSequence); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSelectCutPoint measures Pass 2's own cost as history grows.
// Its cut-selection walk itself only needs to reach protectedTail, but its
// very first step -- the upfront Trigger-comparison estimate -- flattens
// every unit's messages into one slice before Meter.Estimate ever runs, so
// this stays O(history) too, not merely Scan (above): see this benchmark
// file's own top-level disclosure for what that means in practice.
func BenchmarkSelectCutPoint(b *testing.B) {
	budget := mustBudget(b, 100000, 4000)
	for _, turnCount := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("turns=%d", turnCount), func(b *testing.B) {
			units, err := ProjectSourceEvents(syntheticHistory(turnCount))
			if err != nil {
				b.Fatal(err)
			}
			meter := WireEstimateMeter{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := SelectCutPoint(PlanInput{Units: units, Budget: budget, Meter: meter}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMaterialize measures the actually-dispatched envelope's own
// size as history grows, given a checkpoint already covers everything but
// the protected tail: design §22.4 requires no request exceed hardInput or
// 4 MiB before dispatch, and requires live heap bounded by the configured
// budget, not Turn count -- this benchmark demonstrates the OUTPUT side of
// that property directly (the materialized envelope Application actually
// dispatches), independent of Scan's own input-side cost above.
func BenchmarkMaterialize(b *testing.B) {
	budget := mustBudget(b, 100000, 4000)
	for _, turnCount := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("turns=%d", turnCount), func(b *testing.B) {
			units, err := ProjectSourceEvents(syntheticHistory(turnCount))
			if err != nil {
				b.Fatal(err)
			}
			meter := WireEstimateMeter{}
			plan, err := SelectCutPoint(PlanInput{Units: units, Budget: budget, Meter: meter})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			var lastEstimate uint64
			for i := 0; i < b.N; i++ {
				prepared := Materialize(MaterializeInput{
					RetainedTail: plan.RetainedUnits,
					CurrentInput: domain.ModelPromptMessage{Role: domain.PromptRoleUser, Text: "one more question"},
					Meter:        meter,
				})
				lastEstimate = prepared.EstimatedTotalTokens
			}
			b.StopTimer()
			if lastEstimate > budget.HardInput {
				b.Fatalf("materialized estimate = %d tokens, exceeds hardInput budget %d at turns=%d", lastEstimate, budget.HardInput, turnCount)
			}
			if lastEstimate == 0 {
				b.Fatal("materialized estimate is zero; benchmark fixture produced nothing")
			}
			b.ReportMetric(float64(lastEstimate), "dispatched_tokens")
		})
	}
}

func mustBudget(b *testing.B, window, output uint32) Budget {
	b.Helper()
	budget, err := ComputeBudget(window, output, DefaultBudgetConfig)
	if err != nil {
		b.Fatal(err)
	}
	return budget
}
