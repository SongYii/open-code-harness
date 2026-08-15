package engine

import (
	"errors"
	"strings"
	"unicode/utf8"
)

type CapabilityTriState string

const (
	CapabilityUnsupported CapabilityTriState = "unsupported"
	CapabilitySupported   CapabilityTriState = "supported"
	CapabilityRequired    CapabilityTriState = "required"
)

// CapabilityProfile is the provider-neutral declaration of what one
// configured model route accepts. Zero token fields mean unknown, not zero.
type CapabilityProfile struct {
	NativeTools         CapabilityTriState
	Images              CapabilityTriState
	StructuredOutput    CapabilityTriState
	ReasoningFields     CapabilityTriState
	PromptCache         CapabilityTriState
	ContextWindowTokens uint32
	MaxOutputTokens     uint32
}

// RequestIdentity is composition-time identity copied into Application
// so the request envelope can be logged without importing an adapter.
type RequestIdentity struct {
	AdapterFamily  string
	ModelID        string
	EndpointID     string
	Profile        CapabilityProfile
	IncludeUsage   bool
	MaxTokensField string
}

func (identity RequestIdentity) Validate() error {
	if !validStableCode(identity.AdapterFamily) {
		return errInvalidRequestIdentity
	}
	if err := validRequestModelID(identity.ModelID); err != nil {
		return err
	}
	if err := validRequestEndpointID(identity.EndpointID); err != nil {
		return err
	}
	if !validCapabilityTriState(identity.Profile.NativeTools) ||
		!validCapabilityTriState(identity.Profile.Images) ||
		!validCapabilityTriState(identity.Profile.StructuredOutput) ||
		!validCapabilityTriState(identity.Profile.ReasoningFields) ||
		!validCapabilityTriState(identity.Profile.PromptCache) {
		return errInvalidRequestIdentity
	}
	switch identity.MaxTokensField {
	case "", "max_tokens", "max_completion_tokens":
		return nil
	default:
		return errInvalidRequestIdentity
	}
}

func validCapabilityTriState(value CapabilityTriState) bool {
	switch value {
	case CapabilityUnsupported, CapabilitySupported, CapabilityRequired:
		return true
	default:
		return false
	}
}

func validRequestModelID(modelID string) error {
	if modelID == "" || !utf8.ValidString(modelID) || strings.TrimSpace(modelID) != modelID || len(modelID) > 256 {
		return errInvalidRequestIdentity
	}
	return nil
}

func validRequestEndpointID(endpointID string) error {
	// EndpointID is host[:port][/path-prefix]; credentials, query, padding, and a trailing slash never belong here.
	if endpointID == "" || !utf8.ValidString(endpointID) || strings.TrimSpace(endpointID) != endpointID || strings.HasSuffix(endpointID, "/") || strings.ContainsAny(endpointID, "@?#") {
		return errInvalidRequestIdentity
	}
	return nil
}

var errInvalidRequestIdentity = errors.New("invalid request identity")
