package domain

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// DeepSeekThinkingV1 is an internal, versioned replay contract, not a generic
// provider extension bag. New protocols require their own validation contract.
const DeepSeekThinkingV1 = "deepseek_thinking_v1"

// DeepSeekMessagesV1 is the experimental DeepSeek Anthropic-compatible route.
// It does not assert Claude signature or request-prefix-binding semantics.
const DeepSeekMessagesV1 = "deepseek_messages_v1"

// MaxProviderReasoningBytes bounds one completed assistant's protocol payload.
// It is independent of the visible assistant output limit. Never truncate it.
const MaxProviderReasoningBytes = 256 << 10

// ProviderState belongs to exactly one completed assistant message. It is
// durable, sensitive protocol input, not display text or summarizer input.
// A non-nil state with empty ReasoningContent differs from absent state.
type ProviderState struct {
	Protocol         string                 `json:"protocol"`
	ModelID          string                 `json:"modelID"`
	EndpointID       string                 `json:"endpointID"`
	ReasoningContent string                 `json:"reasoningContent"`
	MessagesContent  []ProviderContentBlock `json:"messagesContent,omitempty"`
}

func ValidateProviderState(state *ProviderState) error {
	return validateProviderState(state, CodeInvalidEvent)
}

func validateProviderState(state *ProviderState, code ErrorCode) error {
	if state == nil {
		return nil
	}
	if state.Protocol != DeepSeekThinkingV1 && state.Protocol != DeepSeekMessagesV1 ||
		state.ModelID == "" || len(state.ModelID) > 256 || !utf8.ValidString(state.ModelID) || strings.TrimSpace(state.ModelID) != state.ModelID ||
		state.EndpointID == "" || len(state.EndpointID) > 2048 || !utf8.ValidString(state.EndpointID) || strings.TrimSpace(state.EndpointID) != state.EndpointID || strings.ContainsAny(state.EndpointID, "@?#\r\n\t") || strings.HasSuffix(state.EndpointID, "/") ||
		len(state.ReasoningContent) > MaxProviderReasoningBytes || !utf8.ValidString(state.ReasoningContent) {
		return domainError(code, "invalid provider protocol state")
	}
	if state.Protocol == DeepSeekThinkingV1 && state.MessagesContent != nil || state.Protocol == DeepSeekMessagesV1 && (state.ReasoningContent != "" || validateMessagesContent(state.MessagesContent) != nil) {
		return domainError(code, "invalid provider protocol content")
	}
	return nil
}

func CloneProviderState(state *ProviderState) *ProviderState {
	if state == nil {
		return nil
	}
	copy := *state
	if state.MessagesContent != nil {
		copy.MessagesContent = append([]ProviderContentBlock{}, state.MessagesContent...)
		for i := range copy.MessagesContent {
			block := &copy.MessagesContent[i]
			block.Input = append(json.RawMessage(nil), block.Input...)
			if block.Signature != nil {
				signature := *block.Signature
				block.Signature = &signature
			}
		}
	}
	return &copy
}

func validateProviderStateJSON(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return invalidEventError("invalid provider state payload")
	}
	if raw, ok := object["providerState"]; ok {
		if err := validateJSONObjectKeys(raw, []string{"protocol", "modelID", "endpointID", "reasoningContent"}, []string{"messagesContent"}); err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return invalidEventError("invalid provider state payload")
		}
		for name, value := range fields {
			if name == "messagesContent" {
				var blocks []json.RawMessage
				if json.Unmarshal(value, &blocks) != nil || len(blocks) == 0 || len(blocks) > 256 {
					return invalidEventError("invalid Messages content")
				}
				for _, block := range blocks {
					if err := validateContentBlockJSON(block); err != nil {
						return err
					}
				}
				continue
			}
			value = bytes.TrimSpace(value)
			if len(value) == 0 || value[0] != '"' {
				return invalidEventError("provider state fields must be strings")
			}
		}
	}
	return nil
}
