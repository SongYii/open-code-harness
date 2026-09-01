package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

const (
	adapterFamily                = "openai_compat"
	defaultUserAgent             = "open-code-harness"
	defaultIdleTimeout           = 60 * time.Second
	defaultResponseHeaderTimeout = 30 * time.Second
	defaultMaxRequestBytes       = 1 << 20
	minToolMaxRequestBytes       = 5 << 20
	defaultMaxSSELineBytes       = 256 << 10
	completionsPath              = "/chat/completions"
)

// APIKeySource supplies the bearer token. Implementations must not log it.
type APIKeySource interface {
	APIKey() (string, error)
}

// EnvAPIKey reads os.Getenv at Stream time, not New time.
type EnvAPIKey struct {
	Name string
}

func (source EnvAPIKey) APIKey() (string, error) {
	if source.Name == "" {
		return "", errMissingAPIKey
	}
	return os.Getenv(source.Name), nil
}

// StaticAPIKey is for tests only. The value is never logged.
type StaticAPIKey struct {
	Value string
}

func (source StaticAPIKey) APIKey() (string, error) {
	return source.Value, nil
}

type WireHints struct {
	IncludeUsage   bool
	MaxTokensField string
}

type Config struct {
	BaseURL               string
	ModelID               string
	APIKey                APIKeySource
	Profile               engine.CapabilityProfile
	Hints                 WireHints
	UserAgent             string
	HTTPClient            *http.Client
	IdleTimeout           time.Duration
	ResponseHeaderTimeout time.Duration
	MaxRequestBytes       int
	MaxSSELineBytes       int
	AllowInsecureLoopback bool
}

type Model struct {
	baseURL     string
	modelID     string
	endpointID  string
	apiKey      APIKeySource
	profile     engine.CapabilityProfile
	hints       WireHints
	userAgent   string
	client      *http.Client
	idleTimeout time.Duration
	maxRequest  int
	maxSSELine  int
}

var (
	_ engine.Model = (*Model)(nil)

	errMissingAPIKey = errors.New("missing api key")
	errInvalidConfig = errors.New("invalid openaicompat config")
)

func ProfileTextOnly(contextWindow, maxOutput uint32) engine.CapabilityProfile {
	return engine.CapabilityProfile{
		NativeTools:         engine.CapabilityUnsupported,
		Images:              engine.CapabilityUnsupported,
		StructuredOutput:    engine.CapabilityUnsupported,
		ReasoningFields:     engine.CapabilityUnsupported,
		PromptCache:         engine.CapabilityUnsupported,
		ContextWindowTokens: contextWindow,
		MaxOutputTokens:     maxOutput,
	}
}

func ProfileToolsSupported(contextWindow, maxOutput uint32) engine.CapabilityProfile {
	profile := ProfileTextOnly(contextWindow, maxOutput)
	profile.NativeTools = engine.CapabilitySupported
	return profile
}

func nativeToolsEnabled(profile engine.CapabilityProfile) bool {
	return profile.NativeTools == engine.CapabilitySupported || profile.NativeTools == engine.CapabilityRequired
}

func New(cfg Config) (*Model, error) {
	if cfg.APIKey == nil {
		return nil, errInvalidConfig
	}
	parsed, endpointID, err := parseBaseURL(cfg.BaseURL, cfg.AllowInsecureLoopback)
	if err != nil {
		return nil, err
	}
	if cfg.IdleTimeout < 0 || cfg.ResponseHeaderTimeout < 0 || cfg.MaxRequestBytes < 0 || cfg.MaxSSELineBytes < 0 {
		return nil, errInvalidConfig
	}
	if nativeToolsEnabled(cfg.Profile) && cfg.MaxRequestBytes > 0 && cfg.MaxRequestBytes < minToolMaxRequestBytes {
		return nil, errInvalidConfig
	}
	model := &Model{
		baseURL:     strings.TrimRight(parsed.String(), "/"),
		modelID:     cfg.ModelID,
		endpointID:  endpointID,
		apiKey:      cfg.APIKey,
		profile:     cfg.Profile,
		hints:       cfg.Hints,
		userAgent:   cfg.UserAgent,
		idleTimeout: cfg.IdleTimeout,
		maxRequest:  cfg.MaxRequestBytes,
		maxSSELine:  cfg.MaxSSELineBytes,
	}
	if model.userAgent == "" {
		model.userAgent = defaultUserAgent
	}
	if model.idleTimeout == 0 {
		model.idleTimeout = defaultIdleTimeout
	}
	if model.maxRequest == 0 {
		if nativeToolsEnabled(cfg.Profile) {
			model.maxRequest = minToolMaxRequestBytes
		} else {
			model.maxRequest = defaultMaxRequestBytes
		}
	}
	if model.maxSSELine == 0 {
		model.maxSSELine = defaultMaxSSELineBytes
	}
	headerTimeout := cfg.ResponseHeaderTimeout
	if headerTimeout == 0 {
		headerTimeout = defaultResponseHeaderTimeout
	}
	model.client = cloneHTTPClient(cfg.HTTPClient, headerTimeout)
	if err := model.Identity().Validate(); err != nil {
		return nil, err
	}
	return model, nil
}

func (m *Model) Identity() engine.RequestIdentity {
	if m == nil {
		return engine.RequestIdentity{}
	}
	return engine.RequestIdentity{
		AdapterFamily:  adapterFamily,
		ModelID:        m.modelID,
		EndpointID:     m.endpointID,
		Profile:        m.profile,
		IncludeUsage:   m.hints.IncludeUsage,
		MaxTokensField: m.hints.MaxTokensField,
	}
}

func (m *Model) Stream(ctx context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	if m == nil || m.client == nil {
		return nil, startupFailure(engine.FailureClassPermanent, "provider_permanent", 0, "", "invalid adapter")
	}
	if ctx == nil {
		return nil, &engine.Error{Code: engine.CodeInvalidRequest, Cause: errors.New("nil context")}
	}
	if err := ctx.Err(); err != nil {
		return nil, canceledError(err)
	}
	if m.profile.NativeTools == engine.CapabilityRequired && len(request.Tools) == 0 {
		return nil, startupFailure(engine.FailureClassPermanent, "provider_permanent", 0, "", "invalid request")
	}
	if request.MaxOutputTokens > m.profile.MaxOutputTokens {
		return nil, startupFailure(engine.FailureClassPermanent, "provider_permanent", 0, "", "invalid request")
	}
	key, err := m.apiKey.APIKey()
	if err != nil || strings.TrimSpace(key) == "" {
		return nil, startupFailure(engine.FailureClassAuth, "provider_auth", 0, "", "missing api key")
	}
	body, err := m.marshalRequest(request)
	if err != nil {
		return nil, startupFailure(engine.FailureClassPermanent, "provider_permanent", 0, "", "invalid request")
	}
	if len(body) > m.maxRequest {
		return nil, startupFailure(engine.FailureClassPermanent, "provider_permanent", 0, "", "request too large")
	}

	reqCtx, cancel := context.WithCancel(ctx)
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, m.baseURL+completionsPath, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, startupFailure(engine.FailureClassPermanent, "provider_permanent", 0, "", "invalid request")
	}
	httpReq.Header.Set("Authorization", "Bearer "+key)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", m.userAgent)
	// Purpose is attribution only: a non-secret header, never a change to
	// the JSON body a model reads (design §6.3).
	if request.Purpose != "" {
		httpReq.Header.Set("X-Och-Request-Purpose", string(request.Purpose))
	}

	started := time.Now()
	resp, err := m.client.Do(httpReq)
	if err != nil {
		cancel()
		return nil, classifyDoError(ctx, err)
	}
	requestID := providerRequestID(resp.Header)
	if classErr := classifyResponse(ctx, resp, requestID); classErr != nil {
		_ = resp.Body.Close()
		cancel()
		return nil, classErr
	}

	stream := newChatStream(reqCtx, resp.Body, cancel, started, requestID, m.idleTimeout, m.maxSSELine, m.profile.NativeTools)
	return stream, nil
}

func (m *Model) marshalRequest(request engine.ModelRequest) ([]byte, error) {
	messages, err := mapCompletionMessages(request)
	if err != nil {
		return nil, err
	}
	tools, err := mapCompletionTools(request.Tools)
	if err != nil {
		return nil, err
	}
	payload := completionRequest{
		Model:    m.modelID,
		Stream:   true,
		Messages: messages,
		Tools:    tools,
	}
	if m.hints.IncludeUsage {
		payload.StreamOptions = &completionStreamOptions{IncludeUsage: true}
	}
	// A positive per-request MaxOutputTokens overrides the route's own
	// statically configured maximum (design §6.3); Stream already
	// rejected a request.MaxOutputTokens exceeding the route maximum, so
	// this is always a legal narrowing, never a widening, of what the
	// route allows. Zero preserves this adapter's original behavior
	// exactly: every caller that predates this field still gets the
	// route's own configured value.
	maxOutputTokens := request.MaxOutputTokens
	if maxOutputTokens == 0 {
		maxOutputTokens = m.profile.MaxOutputTokens
	}
	if maxOutputTokens > 0 {
		tokens := maxOutputTokens
		switch m.hints.MaxTokensField {
		case "max_tokens":
			payload.MaxTokens = &tokens
		case "max_completion_tokens":
			payload.MaxCompletionTokens = &tokens
		}
	}
	return json.Marshal(payload)
}

func mapCompletionMessages(request engine.ModelRequest) ([]completionMessage, error) {
	if len(request.Messages) == 0 {
		return []completionMessage{{Role: "user", Content: request.Input}}, nil
	}
	messages := make([]completionMessage, 0, len(request.Messages))
	for _, message := range request.Messages {
		mapped, err := mapCompletionMessage(message)
		if err != nil {
			return nil, err
		}
		messages = append(messages, mapped)
	}
	return messages, nil
}

func mapCompletionMessage(message domain.ModelPromptMessage) (completionMessage, error) {
	switch message.Role {
	case domain.PromptRoleUser, domain.PromptRoleSystem:
		return completionMessage{Role: message.Role, Content: message.Text}, nil
	case domain.PromptRoleAssistant:
		mapped := completionMessage{Role: message.Role, Content: message.Text}
		if len(message.ToolCalls) > 0 {
			mapped.ToolCalls = mapAssistantToolCalls(message.ToolCalls)
		}
		return mapped, nil
	case domain.PromptRoleTool:
		return completionMessage{
			Role:       message.Role,
			Content:    message.Text,
			ToolCallID: message.ToolCallID,
			Name:       message.Name,
		}, nil
	default:
		return completionMessage{}, errInvalidConfig
	}
}

func mapAssistantToolCalls(offers []domain.ToolCallOffer) []completionToolCall {
	calls := make([]completionToolCall, 0, len(offers))
	for _, offer := range offers {
		calls = append(calls, completionToolCall{
			ID:   offer.ID,
			Type: "function",
			Function: completionToolCallFunction{
				Name:      offer.Name,
				Arguments: offer.Arguments,
			},
		})
	}
	return calls
}

func mapCompletionTools(schemas []domain.ToolSchema) ([]completionTool, error) {
	if len(schemas) == 0 {
		return nil, nil
	}
	tools := make([]completionTool, 0, len(schemas))
	for _, schema := range schemas {
		parameters, err := toolParameters(schema.InputSchema)
		if err != nil {
			return nil, err
		}
		tools = append(tools, completionTool{
			Type: "function",
			Function: completionToolFunction{
				Name:        schema.Name,
				Description: schema.Description,
				Parameters:  parameters,
			},
		})
	}
	return tools, nil
}

func toolParameters(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(trimmed) || trimmed[0] != '{' {
		return nil, errInvalidConfig
	}
	copied := append(json.RawMessage(nil), trimmed...)
	return copied, nil
}

type completionRequest struct {
	Model               string                   `json:"model"`
	Stream              bool                     `json:"stream"`
	Messages            []completionMessage      `json:"messages"`
	Tools               []completionTool         `json:"tools,omitempty"`
	StreamOptions       *completionStreamOptions `json:"stream_options,omitempty"`
	MaxTokens           *uint32                  `json:"max_tokens,omitempty"`
	MaxCompletionTokens *uint32                  `json:"max_completion_tokens,omitempty"`
}

type completionMessage struct {
	Role       string               `json:"role"`
	Content    string               `json:"content"`
	ToolCalls  []completionToolCall `json:"tool_calls,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
	Name       string               `json:"name,omitempty"`
}

type completionTool struct {
	Type     string                 `json:"type"`
	Function completionToolFunction `json:"function"`
}

type completionToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type completionToolCall struct {
	ID       string                     `json:"id"`
	Type     string                     `json:"type"`
	Function completionToolCallFunction `json:"function"`
}

type completionToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type completionStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

func parseBaseURL(raw string, allowInsecureLoopback bool) (*url.URL, string, error) {
	if raw == "" || !utf8.ValidString(raw) {
		return nil, "", errInvalidConfig
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Opaque != "" {
		return nil, "", errInvalidConfig
	}
	if parsed.User != nil {
		return nil, "", errInvalidConfig
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !allowInsecureLoopback || !isLoopbackHost(parsed.Hostname()) {
			return nil, "", errInvalidConfig
		}
	default:
		return nil, "", errInvalidConfig
	}
	cleaned := *parsed
	cleaned.User = nil
	cleaned.RawQuery = ""
	cleaned.ForceQuery = false
	cleaned.Fragment = ""
	cleaned.RawFragment = ""
	return &cleaned, endpointIDFromURL(&cleaned), nil
}

func endpointIDFromURL(parsed *url.URL) string {
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path == "" {
		return parsed.Host
	}
	return parsed.Host + path
}
