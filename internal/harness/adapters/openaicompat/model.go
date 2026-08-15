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

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

const (
	adapterFamily                = "openai_compat"
	defaultUserAgent             = "open-code-harness"
	defaultIdleTimeout           = 60 * time.Second
	defaultResponseHeaderTimeout = 30 * time.Second
	defaultMaxRequestBytes       = 1 << 20
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

func New(cfg Config) (*Model, error) {
	if cfg.APIKey == nil {
		return nil, errInvalidConfig
	}
	if cfg.Profile.NativeTools == engine.CapabilityRequired {
		return nil, errInvalidConfig
	}
	parsed, endpointID, err := parseBaseURL(cfg.BaseURL, cfg.AllowInsecureLoopback)
	if err != nil {
		return nil, err
	}
	if cfg.IdleTimeout < 0 || cfg.ResponseHeaderTimeout < 0 || cfg.MaxRequestBytes < 0 || cfg.MaxSSELineBytes < 0 {
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
		model.maxRequest = defaultMaxRequestBytes
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
	key, err := m.apiKey.APIKey()
	if err != nil || strings.TrimSpace(key) == "" {
		return nil, startupFailure(engine.FailureClassAuth, "provider_auth", 0, "", "missing api key")
	}
	body, err := m.marshalRequest(request.Input)
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

	stream := newChatStream(reqCtx, resp.Body, cancel, started, requestID, m.idleTimeout, m.maxSSELine)
	return stream, nil
}

func (m *Model) marshalRequest(input string) ([]byte, error) {
	payload := completionRequest{
		Model:  m.modelID,
		Stream: true,
		Messages: []completionMessage{
			{Role: "user", Content: input},
		},
	}
	if m.hints.IncludeUsage {
		payload.StreamOptions = &completionStreamOptions{IncludeUsage: true}
	}
	if m.profile.MaxOutputTokens > 0 {
		tokens := m.profile.MaxOutputTokens
		switch m.hints.MaxTokensField {
		case "max_tokens":
			payload.MaxTokens = &tokens
		case "max_completion_tokens":
			payload.MaxCompletionTokens = &tokens
		}
	}
	return json.Marshal(payload)
}

type completionRequest struct {
	Model               string                   `json:"model"`
	Stream              bool                     `json:"stream"`
	Messages            []completionMessage      `json:"messages"`
	StreamOptions       *completionStreamOptions `json:"stream_options,omitempty"`
	MaxTokens           *uint32                  `json:"max_tokens,omitempty"`
	MaxCompletionTokens *uint32                  `json:"max_completion_tokens,omitempty"`
}

type completionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
