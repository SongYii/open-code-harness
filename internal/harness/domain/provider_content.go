package domain

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// ProviderContentBlock is a closed replay union, not an SDK object or extension
// bag. Signature distinguishes absent from genuinely empty; Input retains JSON
// numbers without a float64 round trip. Only DeepSeekMessagesV1 uses it.
type ProviderContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature *string         `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

func (b ProviderContentBlock) MarshalJSON() ([]byte, error) {
	switch b.Type {
	case "text":
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{b.Type, b.Text})
	case "thinking":
		return json.Marshal(struct {
			Type      string  `json:"type"`
			Thinking  string  `json:"thinking"`
			Signature *string `json:"signature,omitempty"`
		}{b.Type, b.Thinking, b.Signature})
	case "tool_use":
		return json.Marshal(struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}{b.Type, b.ID, b.Name, b.Input})
	default:
		return nil, invalidEventError("unsupported provider content block")
	}
}

func validateMessagesContent(blocks []ProviderContentBlock) error {
	if len(blocks) == 0 || len(blocks) > 256 {
		return invalidEventError("invalid Messages block count")
	}
	total, hidden := 0, 0
	seen := map[string]bool{}
	for _, b := range blocks {
		if !utf8.ValidString(b.Text) || !utf8.ValidString(b.Thinking) || b.Signature != nil && !utf8.ValidString(*b.Signature) {
			return invalidEventError("invalid Messages text")
		}
		total += len(b.Text) + len(b.Thinking) + len(b.ID) + len(b.Name) + len(b.Input)
		hidden += len(b.Thinking)
		if b.Signature != nil {
			total += len(*b.Signature)
			hidden += len(*b.Signature)
		}
		switch b.Type {
		case "text":
			if b.Thinking != "" || b.Signature != nil || b.ID != "" || b.Name != "" || b.Input != nil {
				return invalidEventError("invalid text block")
			}
		case "thinking":
			if b.Text != "" || b.ID != "" || b.Name != "" || b.Input != nil {
				return invalidEventError("invalid thinking block")
			}
		case "tool_use":
			if b.Text != "" || b.Thinking != "" || b.Signature != nil || !replayIdentifier(b.ID) || !replayIdentifier(b.Name) || seen[b.ID] || len(b.Input) > 32<<10 || !validReplayObject(b.Input) {
				return invalidEventError("invalid tool use block")
			}
			seen[b.ID] = true
		default:
			return invalidEventError("unsupported Messages block")
		}
	}
	if total > 1<<20 || hidden > MaxProviderReasoningBytes {
		return invalidEventError("Messages content exceeds limit")
	}
	return nil
}

// ValidateProviderProjection prevents the replay blocks and visible completion
// from becoming two disagreeing histories. Legacy reasoning has no projection.
func ValidateProviderProjection(state *ProviderState, text string, calls []ToolCallOffer) error {
	return validateProviderProjection(state, text, calls, CodeInvalidEvent)
}

func validateProviderProjection(state *ProviderState, text string, calls []ToolCallOffer, code ErrorCode) error {
	if err := validateProviderState(state, code); err != nil {
		return err
	}
	if state == nil || state.Protocol != DeepSeekMessagesV1 {
		return nil
	}
	var visible strings.Builder
	index := 0
	for _, b := range state.MessagesContent {
		if b.Type == "text" {
			visible.WriteString(b.Text)
		}
		if b.Type == "tool_use" {
			if index >= len(calls) || calls[index].ID != b.ID || calls[index].Name != b.Name || !equalReplayJSON(b.Input, []byte(calls[index].Arguments)) {
				return domainError(code, "provider tool projection mismatch")
			}
			index++
		}
	}
	if visible.String() != text || index != len(calls) {
		return domainError(code, "provider completion projection mismatch")
	}
	return nil
}

func equalReplayJSON(a, b []byte) bool {
	if !validReplayObject(b) {
		return false
	}
	ad, bd := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	ad.UseNumber()
	bd.UseNumber()
	var av, bv any
	return ad.Decode(&av) == nil && bd.Decode(&bv) == nil && reflect.DeepEqual(av, bv)
}

func replayIdentifier(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validateContentBlockJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return invalidEventError("invalid Messages block")
	}
	var typ string
	if json.Unmarshal(fields["type"], &typ) != nil {
		return invalidEventError("invalid Messages type")
	}
	var required, optional []string
	switch typ {
	case "text":
		required = []string{"type", "text"}
	case "thinking":
		required, optional = []string{"type", "thinking"}, []string{"signature"}
	case "tool_use":
		required = []string{"type", "id", "name", "input"}
	default:
		return invalidEventError("unsupported Messages type")
	}
	if err := validateJSONObjectKeys(raw, required, optional); err != nil {
		return err
	}
	for key, value := range fields {
		if key == "input" {
			if !validReplayObject(value) {
				return invalidEventError("invalid Messages input")
			}
			continue
		}
		value = bytes.TrimSpace(value)
		if len(value) == 0 || value[0] != '"' {
			return invalidEventError("Messages field must be a string")
		}
	}
	return nil
}

func validReplayObject(raw []byte) bool {
	if !isJSONObject(raw) || !json.Valid(raw) || validateStrictJSONStrings(raw) != nil {
		return false
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var consume func(int) bool
	consume = func(depth int) bool {
		if depth > 64 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] {
					return false
				}
				seen[name] = true
				if !consume(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim('}')
		case json.Delim('['):
			for d.More() {
				if !consume(depth + 1) {
					return false
				}
			}
			end, err := d.Token()
			return err == nil && end == json.Delim(']')
		default:
			_, delim := token.(json.Delim)
			return !delim
		}
	}
	if !consume(0) {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}
