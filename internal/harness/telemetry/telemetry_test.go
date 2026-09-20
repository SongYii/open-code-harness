package telemetry

import (
	"context"
	"strings"
	"testing"
)

func TestVocabularyRejectsContentAndMalformedMetadata(t *testing.T) {
	valid := Start{Kind: KindTurn, Attributes: []Attribute{String(KeySessionID, "session-1"), Int64(KeyAppendEventCount, 2)}}
	if err := ValidateStart(valid); err != nil {
		t.Fatalf("ValidateStart(valid) = %v", err)
	}
	for _, test := range []Start{
		{Kind: "arbitrary"},
		{Kind: KindTurn, Attributes: []Attribute{{Key: 250, Kind: ValueString, String: "x"}}},
		{Kind: KindTurn, Attributes: []Attribute{String(KeySessionID, "contains whitespace")}},
		{Kind: KindTurn, Attributes: []Attribute{String(KeySessionID, "secret=sk-test")}},
		{Kind: KindTurn, Attributes: []Attribute{String(KeySessionID, strings.Repeat("a", MaxValueBytes+1))}},
		{Kind: KindTurn, Attributes: []Attribute{String(KeySessionID, "one"), String(KeySessionID, "two")}},
	} {
		if err := ValidateStart(test); err == nil {
			t.Fatalf("ValidateStart(%#v) succeeded", test)
		}
	}
}

func TestSafeSpanClosesWithDroppedOutcomeWhenEndMetadataIsInvalid(t *testing.T) {
	recorder := &captureTracer{}
	_, span := SafeStart(recorder, context.Background(), Start{Kind: KindTurn})
	span.End(End{Outcome: OutcomeOK, Code: "unsafe code with spaces"})
	if recorder.end.Outcome != OutcomeDropped || recorder.end.Code != "invalid_telemetry_metadata" {
		t.Fatalf("end = %#v, want bounded dropped outcome", recorder.end)
	}
}

type captureTracer struct{ end End }

func (tracer *captureTracer) Start(ctx context.Context, _ Start) (context.Context, Span) {
	return ctx, captureSpan{tracer: tracer}
}

type captureSpan struct{ tracer *captureTracer }

func (span captureSpan) End(end End) { span.tracer.end = end }

func TestSafeStartIsNilSafeAndEndsOnce(t *testing.T) {
	ctx, span := SafeStart(nil, nil, Start{Kind: KindTurn})
	if ctx == nil || span == nil {
		t.Fatal("SafeStart returned nil")
	}
	span.End(End{Outcome: OutcomeOK})

	recorder := &countTracer{}
	_, span = SafeStart(recorder, context.Background(), Start{Kind: KindTurn})
	span.End(End{Outcome: OutcomeOK})
	span.End(End{Outcome: OutcomeFailed, Code: "second"})
	if recorder.ends != 1 {
		t.Fatalf("ends = %d, want 1", recorder.ends)
	}
}

type countTracer struct{ ends int }
type countSpan struct{ owner *countTracer }

func (tracer *countTracer) Start(ctx context.Context, _ Start) (context.Context, Span) {
	return ctx, countSpan{owner: tracer}
}
func (span countSpan) End(End) { span.owner.ends++ }
