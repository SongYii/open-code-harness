package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// Config deliberately selects one supported protocol, not a generic Anthropic
// gateway. The SDK stays private and supplies no ambient credentials or retries.
type Config struct {
	BaseURL               string
	ModelID               string
	APIKey                string
	ContextWindow         uint32
	MaxOutput             uint32
	ReasoningEffort       engine.ReasoningEffort
	AllowInsecureLoopback bool
	HTTPClient            *http.Client // Transport injection for local conformance tests.
	IdleTimeout           time.Duration
}

type Model struct {
	client   sdk.Client
	identity engine.RequestIdentity
	idle     time.Duration
}

var errConfig = errors.New("anthropic: invalid DeepSeek Messages configuration")

func New(cfg Config) (*Model, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || !utf8.ValidString(cfg.BaseURL) || strings.TrimSpace(cfg.BaseURL) != cfg.BaseURL {
		return nil, errConfig
	}
	ip := net.ParseIP(u.Hostname())
	loopback := strings.EqualFold(u.Hostname(), "localhost") || ip != nil && ip.IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && cfg.AllowInsecureLoopback && loopback) || strings.TrimSpace(cfg.APIKey) == "" || strings.ContainsAny(cfg.APIKey, "\r\n") || cfg.MaxOutput == 0 || cfg.IdleTimeout < 0 || !supportedEffort(cfg.ReasoningEffort) {
		return nil, errConfig
	}
	endpoint := u.Host + strings.TrimRight(u.EscapedPath(), "/")
	identity := engine.RequestIdentity{
		AdapterFamily: domain.DeepSeekMessagesV1, ModelID: cfg.ModelID, EndpointID: endpoint,
		ThinkingMode: "enabled", ReasoningEffort: cfg.ReasoningEffort, MaxTokensField: "max_tokens",
		Profile: engine.CapabilityProfile{
			NativeTools: engine.CapabilitySupported, Images: engine.CapabilityUnsupported,
			StructuredOutput: engine.CapabilityUnsupported, ReasoningFields: engine.CapabilitySupported,
			PromptCache: engine.CapabilityUnsupported, ContextWindowTokens: cfg.ContextWindow, MaxOutputTokens: cfg.MaxOutput,
		},
	}
	if identity.Validate() != nil || len(endpoint) > 2048 || strings.ContainsAny(endpoint, "\r\n\t") {
		return nil, errConfig
	}
	idle := cfg.IdleTimeout
	if idle == 0 {
		idle = 60 * time.Second
	}
	client := sdk.NewClient(
		option.WithoutEnvironmentDefaults(), option.WithBaseURL(strings.TrimRight(u.String(), "/")+"/"),
		option.WithAPIKey(cfg.APIKey), option.WithMaxRetries(0), option.WithRequestTimeout(0),
		option.WithHTTPClient(newHTTPBoundary(cfg.HTTPClient)),
		option.WithHeader("User-Agent", "open-code-harness"), option.WithHeader("Accept", "text/event-stream"),
	)
	return &Model{client: client, identity: identity, idle: idle}, nil
}

func (m *Model) Identity() engine.RequestIdentity { return m.identity }

func supportedEffort(e engine.ReasoningEffort) bool {
	return e == "" || e == engine.ReasoningEffortLow || e == engine.ReasoningEffortHigh || e == engine.ReasoningEffortMax
}

func (m *Model) Stream(ctx context.Context, request engine.ModelRequest) (engine.ModelStream, error) {
	if ctx == nil {
		return nil, failure(engine.CodeInvalidRequest, 0)
	}
	if ctx.Err() != nil {
		return nil, canceled()
	}
	params, err := m.parameters(request)
	if err != nil {
		return nil, failure(engine.CodeInvalidRequest, 0)
	}
	// Let the SDK encode typed requests; check the final body before transport.
	params.SetExtraFields(map[string]any{"stream": true})
	body, err := json.Marshal(params)
	if err != nil || len(body) > 5<<20 {
		return nil, failure(engine.CodeInvalidRequest, 0)
	}
	reqCtx, cancel := context.WithCancel(ctx)
	started := time.Now()
	var response *http.Response
	opts := []option.RequestOption{}
	if request.Purpose != "" {
		opts = append(opts, option.WithHeader("X-Och-Request-Purpose", string(request.Purpose)))
	}
	// Raw response access is intentional: SDK SSE decoding would erase the raw
	// input needed to reject duplicate keys, lossy Unicode and repaired tools.
	err = m.client.Execute(reqCtx, http.MethodPost, "v1/messages", body, &response, opts...)
	if err != nil {
		cancel()
		if ctx.Err() != nil {
			return nil, canceled()
		}
		var status *statusError
		if errors.As(err, &status) {
			if status.contextOverflow {
				return nil, &engine.Error{Code: engine.CodeModelStartup, Cause: &engine.ProviderFailure{
					Class: engine.FailureClassPermanent, Code: "context_overflow", HTTPStatus: status.status,
					SafeMessage: "DeepSeek Messages context window exceeded",
				}}
			}
			return nil, failure(engine.CodeModelStartup, status.status)
		}
		return nil, failure(engine.CodeModelStartup, 0) // Never retain SDK request/body errors.
	}
	stream := newMessageStream(reqCtx, cancel, response.Body, m.identity, m.idle, started)
	if id := response.Header.Get("request-id"); identifier(id) {
		stream.stats.ProviderRequestID = id
	}
	return stream, nil
}

func (m *Model) parameters(request engine.ModelRequest) (sdk.MessageNewParams, error) {
	p := sdk.MessageNewParams{Model: sdk.Model(m.identity.ModelID), MaxTokens: int64(m.identity.Profile.MaxOutputTokens)}
	if request.MaxOutputTokens > m.identity.Profile.MaxOutputTokens || !supportedEffort(request.ReasoningEffort) {
		return p, errConfig
	}
	if request.MaxOutputTokens > 0 {
		p.MaxTokens = int64(request.MaxOutputTokens)
	}
	// DeepSeek documents budget_tokens as ignored, so do not invent a budget.
	enabled := param.Override[sdk.ThinkingConfigEnabledParam](json.RawMessage(`{"type":"enabled"}`))
	p.Thinking = sdk.ThinkingConfigParamUnion{OfEnabled: &enabled}
	effort := request.ReasoningEffort
	if effort == "" {
		effort = m.identity.ReasoningEffort
	}
	if effort != "" {
		p.OutputConfig = param.Override[sdk.OutputConfigParam](map[string]string{"effort": string(effort)})
	}
	messages := request.Messages
	if len(messages) == 0 {
		messages = []domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: request.Input}}
	}
	pending := map[string]string{}
	for _, msg := range messages {
		if !utf8.ValidString(msg.Text) || msg.Role != domain.PromptRoleAssistant && (msg.ProviderState != nil || len(msg.ToolCalls) > 0) || msg.Role != domain.PromptRoleTool && (msg.ToolCallID != "" || msg.Name != "") {
			return p, errConfig
		}
		if len(pending) > 0 && msg.Role != domain.PromptRoleTool {
			return p, errConfig
		}
		switch msg.Role {
		case domain.PromptRoleSystem:
			if len(p.Messages) != 0 {
				return p, errConfig
			}
			p.System = append(p.System, sdk.TextBlockParam{Text: msg.Text})
		case domain.PromptRoleUser:
			p.Messages = append(p.Messages, sdk.NewUserMessage(sdk.NewTextBlock(msg.Text)))
		case domain.PromptRoleAssistant:
			s := msg.ProviderState
			if s == nil || s.Protocol != domain.DeepSeekMessagesV1 || s.ModelID != m.identity.ModelID || s.EndpointID != m.identity.EndpointID || domain.ValidateProviderProjection(s, msg.Text, msg.ToolCalls) != nil {
				return p, errConfig
			}
			blocks := make([]sdk.ContentBlockParamUnion, 0, len(s.MessagesContent))
			for _, b := range s.MessagesContent {
				// Override only a validated, closed Domain block. ToParam would
				// invent signature:"" when the compatible provider omitted it.
				encoded, err := json.Marshal(b)
				if err != nil {
					return p, errConfig
				}
				blocks = append(blocks, param.Override[sdk.ContentBlockParamUnion](json.RawMessage(encoded)))
				if b.Type == "tool_use" {
					pending[b.ID] = b.Name
				}
			}
			p.Messages = append(p.Messages, sdk.NewAssistantMessage(blocks...))
		case domain.PromptRoleTool:
			if pending[msg.ToolCallID] == "" || pending[msg.ToolCallID] != msg.Name {
				return p, errConfig
			}
			delete(pending, msg.ToolCallID)
			block := sdk.NewToolResultBlock(msg.ToolCallID, msg.Text, false)
			if len(p.Messages) > 0 && p.Messages[len(p.Messages)-1].Role == sdk.MessageParamRoleUser {
				p.Messages[len(p.Messages)-1].Content = append(p.Messages[len(p.Messages)-1].Content, block)
			} else {
				p.Messages = append(p.Messages, sdk.NewUserMessage(block))
			}
		default:
			return p, errConfig
		}
	}
	if len(pending) != 0 || len(p.Messages) == 0 || p.Messages[len(p.Messages)-1].Role != sdk.MessageParamRoleUser {
		return p, errConfig
	}
	seen := map[string]bool{}
	for _, tool := range request.Tools {
		if !identifier(tool.Name) || seen[tool.Name] || !utf8.ValidString(tool.Description) || !validReplayJSON(tool.InputSchema) || !strings.HasPrefix(strings.TrimSpace(string(tool.InputSchema)), "{") {
			return p, errConfig
		}
		seen[tool.Name] = true
		p.Tools = append(p.Tools, sdk.ToolUnionParam{OfTool: &sdk.ToolParam{Name: tool.Name, Description: sdk.String(tool.Description), InputSchema: param.Override[sdk.ToolInputSchemaParam](tool.InputSchema)}})
	}
	return p, nil
}

var _ engine.Model = (*Model)(nil)
