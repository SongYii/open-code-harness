package contextengine

import (
	"encoding/json"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// Envelope is the complete provider-neutral request surface a Meter prices:
// the message history plus the Tool Schemas offered alongside it (design
// §8). Checkpoint framing text and Tool Result prune markers are ordinary
// domain.ModelPromptMessage text by the time they reach a Meter — this
// package's checkpoint/tool_result logic (implementation plan Tasks 4-5)
// produces them as such, not as a distinct Envelope field.
type Envelope struct {
	Messages []domain.ModelPromptMessage
	Tools    []domain.ToolSchema
}

// Estimate is a Meter's token count for one Envelope, together with the
// identity of the meter that produced it. Meter identity travels with
// every estimate so a caller can tell whether two estimates are directly
// comparable (design §8: "Meter identity is durable evidence so estimates
// are not compared across algorithms as though they were identical").
type Estimate struct {
	Tokens  uint64
	MeterID string
}

// Meter estimates the token cost of a complete request Envelope. The
// default implementation (WireEstimateMeter) is conservative and
// deterministic (CE-04); a route-specific exact meter may implement this
// interface later behind the same port, as long as it carries contract
// tests for every message/tool shape WireEstimateMeter already covers.
type Meter interface {
	// ID names this meter for Estimate.MeterID and for the usage-anchor
	// identity match in EvaluateUsageAnchor.
	ID() string
	// Estimate prices a complete Envelope.
	Estimate(Envelope) Estimate
	// EstimateMessages prices a bare message slice — checkpoint tail
	// pricing, tool-result-projection sizing, and the usage anchor's
	// signedSurfaceDelta all need this without also carrying a Tools list.
	EstimateMessages(messages []domain.ModelPromptMessage) uint64
}

// WireEstimateMeterID identifies the design's default deterministic meter,
// och_wire_estimate_v1 (design §8).
const WireEstimateMeterID = "och_wire_estimate_v1"

// perMessageFraming, perToolCallOrResult, and perToolSchemaFraming are
// och_wire_estimate_v1's fixed per-unit token charges (design §8): 8 tokens
// per message, 16 additional tokens per Tool Call or Tool Result, and 16
// tokens plus the Tool Schema's own canonical-JSON byte cost per schema.
const (
	perMessageFraming   = 8
	perToolCallOrResult = 16
	perToolSchemaFixed  = 16
)

// WireEstimateMeter implements och_wire_estimate_v1: a model-neutral,
// deterministic token estimator that deliberately overprices typical ASCII
// prose (design §8) rather than depend on any one vendor's tokenizer.
type WireEstimateMeter struct{}

func (WireEstimateMeter) ID() string { return WireEstimateMeterID }

func (m WireEstimateMeter) Estimate(envelope Envelope) Estimate {
	total := m.EstimateMessages(envelope.Messages)
	for _, schema := range envelope.Tools {
		total += m.estimateToolSchema(schema)
	}
	return Estimate{Tokens: total, MeterID: WireEstimateMeterID}
}

// EstimateMessages prices each message's fixed framing, its text payload,
// and — per message — 16 tokens for every offered Tool Call plus the
// call's own name/arguments JSON payload, and a further 16 tokens if the
// message is itself a Tool Result (ToolCallID set).
func (WireEstimateMeter) EstimateMessages(messages []domain.ModelPromptMessage) uint64 {
	var total uint64
	for _, message := range messages {
		total += perMessageFraming
		total += textTokens(message.Text)
		if message.ToolCallID != "" {
			total += perToolCallOrResult
		}
		for _, call := range message.ToolCalls {
			total += perToolCallOrResult
			total += textTokens(call.Name)
			total += textTokens(call.Arguments)
		}
	}
	return total
}

func (WireEstimateMeter) estimateToolSchema(schema domain.ToolSchema) uint64 {
	encoded, err := json.Marshal(schema)
	if err != nil {
		// domain.ToolSchema's fields (string, string, json.RawMessage) are
		// always marshalable; a failure here would mean InputSchema itself
		// is not valid JSON, which the domain layer's own validation
		// already rejects before a ToolSchema is ever constructed. Treat
		// it as a zero-byte payload rather than panicking a pure estimator.
		return perToolSchemaFixed
	}
	return perToolSchemaFixed + ceilDiv(uint64(len(encoded)), 3)
}

// textTokens is och_wire_estimate_v1's "text/JSON payload" rule:
// ceil(UTF-8 bytes / 3). It counts bytes, not runes — utf8.RuneCountInString
// would undercount multi-byte text relative to the design's own stated
// formula.
func textTokens(text string) uint64 {
	if text == "" {
		return 0
	}
	return ceilDiv(uint64(len(text)), 3)
}
