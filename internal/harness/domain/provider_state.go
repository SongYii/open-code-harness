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

// MaxProviderReasoningBytes bounds one completed assistant's protocol payload.
// It is independent of the visible assistant output limit. Never truncate it.
const MaxProviderReasoningBytes = 256 << 10

// ProviderState belongs to exactly one completed assistant message. It is
// durable, sensitive protocol input, not display text or summarizer input.
// A non-nil state with empty ReasoningContent differs from absent state.
type ProviderState struct {
	Protocol         string `json:"protocol"`
	ModelID          string `json:"modelID"`
	EndpointID       string `json:"endpointID"`
	ReasoningContent string `json:"reasoningContent"`
}

func ValidateProviderState(state *ProviderState) error {
	return validateProviderState(state, CodeInvalidEvent)
}

func validateProviderState(state *ProviderState, code ErrorCode) error {
	if state == nil {
		return nil
	}
	if state.Protocol != DeepSeekThinkingV1 ||
		state.ModelID == "" || len(state.ModelID) > 256 || !utf8.ValidString(state.ModelID) || strings.TrimSpace(state.ModelID) != state.ModelID ||
		state.EndpointID == "" || len(state.EndpointID) > 2048 || !utf8.ValidString(state.EndpointID) || strings.TrimSpace(state.EndpointID) != state.EndpointID || strings.ContainsAny(state.EndpointID, "@?#\r\n\t") || strings.HasSuffix(state.EndpointID, "/") ||
		len(state.ReasoningContent) > MaxProviderReasoningBytes || !utf8.ValidString(state.ReasoningContent) {
		return domainError(code, "invalid provider protocol state")
	}
	return nil
}

func CloneProviderState(state *ProviderState) *ProviderState {
	if state == nil {
		return nil
	}
	copy := *state // All fields are immutable strings; no shared mutable storage.
	return &copy
}

func validateProviderStateJSON(data []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return invalidEventError("invalid provider state payload")
	}
	if raw, ok := object["providerState"]; ok {
		if err := validateStrictJSONObject(raw, "protocol", "modelID", "endpointID", "reasoningContent"); err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return invalidEventError("invalid provider state payload")
		}
		for _, value := range fields {
			value = bytes.TrimSpace(value)
			if len(value) == 0 || value[0] != '"' {
				return invalidEventError("provider state fields must be strings")
			}
		}
	}
	return nil
}
