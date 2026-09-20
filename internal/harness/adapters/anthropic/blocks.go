package anthropic

import (
	"encoding/json"
	"strings"

	"github.com/SongYii/open-code-harness/internal/harness/redact"
)

const (
	maxBlocks         = 256
	maxContentBytes   = 1 << 20
	maxThinkingBytes  = 256 << 10 // Includes signatures and encrypted redactions.
	maxToolInputBytes = 32 << 10
)

// Profiles are explicit, never inferred from a model alias or endpoint URL.
type wireProfile uint8

const (
	nativeMessages wireProfile = iota // Decoder conformance only; not a Claude route.
	deepSeekMessages
)

// contentBlock preserves order and associations, including text after tools.
// Only the active variant's fields can be populated by the decoder. Nothing
// from this adapter-private representation is a durable Domain schema yet.
type contentBlock struct {
	Type             string
	Text             string
	Thinking         string
	Signature        string
	SignaturePresent bool
	Data             string
	ID               string
	Name             string
	Input            json.RawMessage
}

func (block contentBlock) MarshalJSON() ([]byte, error) {
	switch block.Type {
	case "text":
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{block.Type, block.Text})
	case "thinking":
		var signature *string
		if block.SignaturePresent || block.Signature != "" {
			signature = &block.Signature
		}
		return json.Marshal(struct {
			Type      string  `json:"type"`
			Thinking  string  `json:"thinking"`
			Signature *string `json:"signature,omitempty"`
		}{block.Type, block.Thinking, signature})
	case "redacted_thinking":
		return json.Marshal(struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}{block.Type, block.Data})
	case "tool_use":
		return json.Marshal(struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}{block.Type, block.ID, block.Name, block.Input})
	default:
		return nil, errProtocol
	}
}

type usage struct {
	InputTokens         uint64
	OutputTokens        uint64
	CacheReadTokens     uint64
	CacheCreationTokens uint64
}

type message struct {
	ID         string
	Model      string
	Content    []contentBlock
	StopReason string
	Usage      usage
}

// visibleText is explicitly not a replay representation. Thinking, signatures,
// encrypted content, and tool inputs cannot leak through this display helper.
func (message message) visibleText() string {
	var text strings.Builder
	for _, block := range message.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

func (message message) replayContent() ([]byte, error) {
	return json.Marshal(message.Content)
}

func (message message) validate() error {
	return message.validateFor(nativeMessages)
}

func (message message) validateFor(profile wireProfile) error {
	if len(message.Content) == 0 || len(message.Content) > maxBlocks {
		return errProtocol
	}
	total, hidden := 0, 0
	var thinking strings.Builder
	toolIDs := make(map[string]bool)
	for _, block := range message.Content {
		total += len(block.Text) + len(block.Thinking) + len(block.Signature) + len(block.Data) + len(block.Input) + len(block.ID) + len(block.Name)
		hidden += len(block.Thinking) + len(block.Signature) + len(block.Data)
		switch block.Type {
		case "text":
			if redact.Text(block.Text) != block.Text {
				return errProtocol // Reject; redaction would corrupt replay.
			}
		case "thinking":
			thinking.WriteString(block.Thinking)
			if profile == nativeMessages && block.Signature == "" || redact.Text(block.Thinking) != block.Thinking {
				return errProtocol
			}
		case "redacted_thinking":
			if profile == deepSeekMessages || block.Data == "" {
				return errProtocol
			}
		case "tool_use":
			if !identifier(block.ID) || !identifier(block.Name) || toolIDs[block.ID] || len(block.Input) > maxToolInputBytes {
				return errProtocol
			}
			toolIDs[block.ID] = true
			// Arguments must be an object; duplicate keys and lossy Unicode are
			// rejected recursively. Values are not decoded through float64.
			if !validReplayJSON(block.Input) || !strings.HasPrefix(strings.TrimSpace(string(block.Input)), "{") {
				return errProtocol
			}
		default:
			return errProtocol
		}
	}
	if total > maxContentBytes || hidden > maxThinkingBytes ||
		(message.StopReason == "tool_use") != (len(toolIDs) > 0) {
		return errProtocol
	}
	if message.StopReason != "end_turn" && message.StopReason != "tool_use" {
		return errProtocol
	}
	// Text blocks become one visible assistant item in the engine. Check that
	// projection too: splitting a secret across blocks must not evade rejection.
	visible := message.visibleText()
	if redact.Text(visible) != visible || redact.Text(thinking.String()) != thinking.String() {
		return errProtocol
	}
	return nil
}

func identifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-') {
			return false
		}
	}
	return true
}
