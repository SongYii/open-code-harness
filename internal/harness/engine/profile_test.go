package engine

import (
	"strings"
	"testing"
)

func TestRequestIdentityValidateAcceptsCompositionIdentity(t *testing.T) {
	identity := validRequestIdentity()
	if err := identity.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRequestIdentityValidateRejectsMalformedFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RequestIdentity)
	}{
		{name: "empty adapter family", mutate: func(id *RequestIdentity) { id.AdapterFamily = "" }},
		{name: "adapter family upper case", mutate: func(id *RequestIdentity) { id.AdapterFamily = "OpenAI" }},
		{name: "adapter family hyphen", mutate: func(id *RequestIdentity) { id.AdapterFamily = "openai-compat" }},
		{name: "empty model id", mutate: func(id *RequestIdentity) { id.ModelID = "" }},
		{name: "padded model id", mutate: func(id *RequestIdentity) { id.ModelID = " model" }},
		{name: "long model id", mutate: func(id *RequestIdentity) { id.ModelID = strings.Repeat("m", 257) }},
		{name: "invalid utf8 model id", mutate: func(id *RequestIdentity) { id.ModelID = string([]byte{0xff}) }},
		{name: "empty endpoint", mutate: func(id *RequestIdentity) { id.EndpointID = "" }},
		{name: "endpoint userinfo", mutate: func(id *RequestIdentity) { id.EndpointID = "user:pass@api.example.com" }},
		{name: "endpoint query", mutate: func(id *RequestIdentity) { id.EndpointID = "api.example.com?key=1" }},
		{name: "endpoint fragment", mutate: func(id *RequestIdentity) { id.EndpointID = "api.example.com#frag" }},
		{name: "empty native tools", mutate: func(id *RequestIdentity) { id.Profile.NativeTools = "" }},
		{name: "unknown tri-state", mutate: func(id *RequestIdentity) { id.Profile.Images = "unknown" }},
		{name: "invalid max tokens field", mutate: func(id *RequestIdentity) { id.MaxTokensField = "tokens" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity := validRequestIdentity()
			test.mutate(&identity)
			if err := identity.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want invalid request identity")
			}
		})
	}
}

func validRequestIdentity() RequestIdentity {
	return RequestIdentity{
		AdapterFamily: "openai_compat",
		ModelID:       "test-model",
		EndpointID:    "api.example.com/v1",
		Profile: CapabilityProfile{
			NativeTools:      CapabilityUnsupported,
			Images:           CapabilityUnsupported,
			StructuredOutput: CapabilityUnsupported,
			ReasoningFields:  CapabilityUnsupported,
			PromptCache:      CapabilityUnsupported,
		},
		IncludeUsage:   true,
		MaxTokensField: "max_tokens",
	}
}
