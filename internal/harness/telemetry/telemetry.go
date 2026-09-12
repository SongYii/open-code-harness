// Package telemetry defines the metadata-only Harness trace vocabulary.
package telemetry

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sync"
)

const (
	MaxAttributes = 32
	MaxValueBytes = 256
)

type Kind string

const (
	KindTurn             Kind = "och.turn"
	KindContextPrepare   Kind = "och.context.prepare"
	KindContextCompact   Kind = "och.context.compact"
	KindModelRequest     Kind = "och.model.request"
	KindPolicyDecide     Kind = "och.policy.decide"
	KindApprovalWait     Kind = "och.approval.wait"
	KindToolExecute      Kind = "och.tool.execute"
	KindStoreAppend      Kind = "och.store.append"
	KindRuntimeReconcile Kind = "och.runtime.reconcile"
)

type Outcome string

const (
	OutcomeOK       Outcome = "ok"
	OutcomeDenied   Outcome = "denied"
	OutcomeCanceled Outcome = "canceled"
	OutcomeTimeout  Outcome = "timeout"
	OutcomeFailed   Outcome = "failed"
	OutcomeDropped  Outcome = "dropped"
)

type Key uint8

const (
	KeySessionID Key = iota + 1
	KeyTurnID
	KeyItemID
	KeyCallID
	KeyApprovalID
	KeyCompactionID
	KeyAppendID
	KeyTurnRole
	KeyTerminalStatus
	KeyModelPurpose
	KeyModelID
	KeyModelFinish
	KeyToolName
	KeyToolSource
	KeyToolRisk
	KeyPolicyEffect
	KeyPolicyRule
	KeyApprovalDecision
	KeyContextTrigger
	KeyContextStrategy
	KeyContextCompacted
	KeyContextEstimatedTokens
	KeyContextPrunedResults
	KeyContextSummaryChunks
	KeyContextCoveredEvents
	KeyContextCoveredTurns
	KeyUsageInputTokens
	KeyUsageOutputTokens
	KeyUsageCachedTokens
	KeyResultBytes
	KeyResultTruncated
	KeyAppendEventCount
	KeyAppendExpectedVersion
	KeyRuntimeCandidates
	KeyRuntimeRecovered
)

var keyNames = map[Key]string{
	KeySessionID:              "och.session.id",
	KeyTurnID:                 "och.turn.id",
	KeyItemID:                 "och.item.id",
	KeyCallID:                 "och.call.id",
	KeyApprovalID:             "och.approval.id",
	KeyCompactionID:           "och.context.compaction.id",
	KeyAppendID:               "och.append.id",
	KeyTurnRole:               "och.turn.role",
	KeyTerminalStatus:         "och.turn.status",
	KeyModelPurpose:           "och.model.purpose",
	KeyModelID:                "och.model.id",
	KeyModelFinish:            "och.model.finish",
	KeyToolName:               "och.tool.name",
	KeyToolSource:             "och.tool.source",
	KeyToolRisk:               "och.tool.risk",
	KeyPolicyEffect:           "och.policy.effect",
	KeyPolicyRule:             "och.policy.rule",
	KeyApprovalDecision:       "och.approval.decision",
	KeyContextTrigger:         "och.context.trigger",
	KeyContextStrategy:        "och.context.strategy",
	KeyContextCompacted:       "och.context.compacted",
	KeyContextEstimatedTokens: "och.context.estimated_tokens",
	KeyContextPrunedResults:   "och.context.pruned_results",
	KeyContextSummaryChunks:   "och.context.summary_chunks",
	KeyContextCoveredEvents:   "och.context.covered_events",
	KeyContextCoveredTurns:    "och.context.covered_turns",
	KeyUsageInputTokens:       "och.usage.input_tokens",
	KeyUsageOutputTokens:      "och.usage.output_tokens",
	KeyUsageCachedTokens:      "och.usage.cached_input_tokens",
	KeyResultBytes:            "och.result.bytes",
	KeyResultTruncated:        "och.result.truncated",
	KeyAppendEventCount:       "och.append.event_count",
	KeyAppendExpectedVersion:  "och.append.expected_version",
	KeyRuntimeCandidates:      "och.runtime.candidates",
	KeyRuntimeRecovered:       "och.runtime.recovered",
}

func (key Key) Name() string { return keyNames[key] }

type ValueKind uint8

const (
	ValueString ValueKind = iota + 1
	ValueInt64
	ValueBool
)

type Attribute struct {
	Key    Key
	Kind   ValueKind
	String string
	Int64  int64
	Bool   bool
}

func String(key Key, value string) Attribute {
	return Attribute{Key: key, Kind: ValueString, String: value}
}

func Int64(key Key, value int64) Attribute {
	return Attribute{Key: key, Kind: ValueInt64, Int64: value}
}

func Uint64(key Key, value uint64) Attribute {
	if value > math.MaxInt64 {
		return Attribute{}
	}
	return Int64(key, int64(value))
}

func Bool(key Key, value bool) Attribute {
	return Attribute{Key: key, Kind: ValueBool, Bool: value}
}

type Start struct {
	Kind       Kind
	Attributes []Attribute
}

type End struct {
	Outcome    Outcome
	Code       string
	Attributes []Attribute
}

type Tracer interface {
	Start(context.Context, Start) (context.Context, Span)
}

type Span interface {
	End(End)
}

var safeString = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)

func ValidateStart(start Start) error {
	if !validKind(start.Kind) {
		return fmt.Errorf("telemetry: invalid span kind")
	}
	return validateAttributes(start.Attributes)
}

func ValidateEnd(end End) error {
	if !validOutcome(end.Outcome) {
		return fmt.Errorf("telemetry: invalid outcome")
	}
	if end.Code != "" && !safeToken(end.Code) {
		return fmt.Errorf("telemetry: invalid error code")
	}
	return validateAttributes(end.Attributes)
}

func validKind(kind Kind) bool {
	switch kind {
	case KindTurn, KindContextPrepare, KindContextCompact, KindModelRequest,
		KindPolicyDecide, KindApprovalWait, KindToolExecute, KindStoreAppend,
		KindRuntimeReconcile:
		return true
	default:
		return false
	}
}

func validOutcome(outcome Outcome) bool {
	switch outcome {
	case OutcomeOK, OutcomeDenied, OutcomeCanceled, OutcomeTimeout,
		OutcomeFailed, OutcomeDropped:
		return true
	default:
		return false
	}
}

func validateAttributes(attributes []Attribute) error {
	if len(attributes) > MaxAttributes {
		return fmt.Errorf("telemetry: too many attributes")
	}
	seen := make(map[Key]struct{}, len(attributes))
	for _, attribute := range attributes {
		if attribute.Key.Name() == "" {
			return fmt.Errorf("telemetry: unknown attribute key")
		}
		if _, exists := seen[attribute.Key]; exists {
			return fmt.Errorf("telemetry: duplicate attribute")
		}
		seen[attribute.Key] = struct{}{}
		switch attribute.Kind {
		case ValueString:
			if !safeToken(attribute.String) {
				return fmt.Errorf("telemetry: unsafe string attribute")
			}
		case ValueInt64:
			if attribute.Int64 < 0 {
				return fmt.Errorf("telemetry: negative numeric attribute")
			}
		case ValueBool:
		default:
			return fmt.Errorf("telemetry: invalid attribute kind")
		}
	}
	return nil
}

func safeToken(value string) bool {
	return len(value) > 0 && len(value) <= MaxValueBytes && safeString.MatchString(value)
}

func IsSafeString(value string) bool { return safeToken(value) }

type noopTracer struct{}
type noopSpan struct{}

func (noopTracer) Start(ctx context.Context, _ Start) (context.Context, Span) {
	return ctx, noopSpan{}
}
func (noopSpan) End(End) {}

var defaultNoop Tracer = noopTracer{}

func Noop() Tracer { return defaultNoop }

func SafeStart(tracer Tracer, ctx context.Context, start Start) (context.Context, Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	if tracer == nil || ValidateStart(start) != nil {
		return ctx, noopSpan{}
	}
	child, span := tracer.Start(ctx, cloneStart(start))
	if child == nil || span == nil {
		return ctx, noopSpan{}
	}
	return child, &safeSpan{span: span}
}

type safeSpan struct {
	once sync.Once
	span Span
}

func (span *safeSpan) End(end End) {
	if span == nil {
		return
	}
	span.once.Do(func() {
		if ValidateEnd(end) != nil {
			end = End{Outcome: OutcomeDropped, Code: "invalid_telemetry_metadata"}
		}
		span.span.End(cloneEnd(end))
	})
}

func cloneStart(start Start) Start {
	start.Attributes = append([]Attribute(nil), start.Attributes...)
	return start
}

func cloneEnd(end End) End {
	end.Attributes = append([]Attribute(nil), end.Attributes...)
	return end
}
