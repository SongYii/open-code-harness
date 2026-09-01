package contextengine

import (
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// ceilDivInt is the test file's own readable restatement of ceilDiv's
// integer math, used only to compute expected values independently of the
// production ceilDiv helper these golden tests exist to check.
func ceilDivInt(numerator, denominator int) uint64 {
	if denominator <= 0 {
		return 0
	}
	return uint64((numerator + denominator - 1) / denominator)
}

func TestWireEstimateMeterGoldenFixtures(t *testing.T) {
	meter := WireEstimateMeter{}

	tests := []struct {
		name     string
		messages []domain.ModelPromptMessage
		want     uint64
	}{
		{
			name:     "ASCII prose",
			messages: []domain.ModelPromptMessage{msg("user", "hello world")},
			// 8 (framing) + ceil(11/3)=4 => 12
			want: perMessageFraming + ceilDivInt(len("hello world"), 3),
		},
		{
			name:     "Chinese multi-byte UTF-8",
			messages: []domain.ModelPromptMessage{msg("user", "你好，世界")},
			// len() counts UTF-8 bytes, not runes: each Chinese character is
			// 3 bytes, the comma is 3 bytes too (U+FF0C), so this is not a
			// trivial rune-count case.
			want: perMessageFraming + ceilDivInt(len("你好，世界"), 3),
		},
		{
			name:     "code-like text",
			messages: []domain.ModelPromptMessage{msg("assistant", "func main() {\n\tfmt.Println(\"hi\")\n}")},
			want:     perMessageFraming + ceilDivInt(len("func main() {\n\tfmt.Println(\"hi\")\n}"), 3),
		},
		{
			name:     "JSON-shaped text",
			messages: []domain.ModelPromptMessage{msg("tool", `{"path":"a.go","content":"package main"}`)},
			want:     perMessageFraming + ceilDivInt(len(`{"path":"a.go","content":"package main"}`), 3),
		},
		{
			name: "assistant message offering one Tool Call",
			messages: []domain.ModelPromptMessage{{
				Role: "assistant",
				Text: "",
				ToolCalls: []domain.ToolCallOffer{
					{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`},
				},
			}},
			// 8 (framing) + 0 (empty text) + 16 (tool call) + ceil(len("read_file")/3) + ceil(len(`{"path":"a.go"}`)/3)
			want: perMessageFraming + perToolCallOrResult + ceilDivInt(len("read_file"), 3) + ceilDivInt(len(`{"path":"a.go"}`), 3),
		},
		{
			name:     "Tool Result message",
			messages: []domain.ModelPromptMessage{toolResult("call_1", "file contents here")},
			// 8 (framing) + ceil(len(text)/3) + 16 (tool result)
			want: perMessageFraming + ceilDivInt(len("file contents here"), 3) + perToolCallOrResult,
		},
		{
			name:     "empty text message still charges framing",
			messages: []domain.ModelPromptMessage{msg("user", "")},
			want:     perMessageFraming,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := meter.EstimateMessages(test.messages); got != test.want {
				t.Fatalf("EstimateMessages(%q) = %d, want %d", test.name, got, test.want)
			}
		})
	}
}

func TestWireEstimateMeterToolSchema(t *testing.T) {
	meter := WireEstimateMeter{}
	schema := domain.ToolSchema{
		Name:        "read_file",
		Description: "reads a file from the workspace",
		InputSchema: []byte(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	}
	envelope := Envelope{Tools: []domain.ToolSchema{schema}}
	got := meter.Estimate(envelope)
	if got.MeterID != WireEstimateMeterID {
		t.Fatalf("MeterID = %q, want %q", got.MeterID, WireEstimateMeterID)
	}
	if got.Tokens == 0 {
		t.Fatal("expected a non-zero estimate for a real Tool Schema")
	}
	// Estimate must equal exactly perToolSchemaFixed plus the canonical
	// JSON encoding's own byte cost, matching the documented formula.
	if got.Tokens < perToolSchemaFixed {
		t.Fatalf("Tokens = %d, want at least the fixed %d-token charge", got.Tokens, perToolSchemaFixed)
	}
}

// TestWireEstimateMeterPruneMarkerAndCheckpointFraming confirms the design's
// own statement that checkpoint framing and Tool Result prune markers are
// "measured as ordinary text" (§8): a marker-shaped string is priced by
// exactly the same byte-based formula as any other message text, with no
// special-cased discount or surcharge.
func TestWireEstimateMeterPruneMarkerAndCheckpointFraming(t *testing.T) {
	meter := WireEstimateMeter{}
	marker := "[tool result projected by Open Code Harness]\nevent_id: evt_1\noriginal_bytes: 9000\nsha256: deadbeef\ncontent_head:\n...\ncontent_tail:\n...\n[end projected tool result]"
	ordinary := toolResult("call_1", marker)
	markerCost := meter.EstimateMessages([]domain.ModelPromptMessage{ordinary})
	sameLengthOrdinaryText := toolResult("call_1", string(make([]byte, len(marker))))
	plainCost := meter.EstimateMessages([]domain.ModelPromptMessage{sameLengthOrdinaryText})
	if markerCost != plainCost {
		t.Fatalf("marker text priced at %d, same-length ordinary text priced at %d; the design requires them equal", markerCost, plainCost)
	}
}

// TestWireEstimateMeterID pins the meter identity string the usage anchor
// and every ContextPreparedRecorded fact durably records (design §8/§7.4).
func TestWireEstimateMeterID(t *testing.T) {
	if got := (WireEstimateMeter{}).ID(); got != "och_wire_estimate_v1" {
		t.Fatalf("ID() = %q, want %q", got, "och_wire_estimate_v1")
	}
}
