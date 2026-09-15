//go:build sdkprobe

package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

// Overlaid into the adapter's test package to reuse its exact fixtures. The
// original probe isolated SDK dependencies; the main module has since adopted it.
// These remain characterization tests, NOT a production
// completion gate. They intentionally expose cases the SDK accepts but OCH
// must reject. Run with the alternate module file in experiments/anthropic-sdk.
type sdkObservation struct {
	message sdk.Message
	stopped bool
	err     error
}

func observeSDK(stream *ssestream.Stream[sdk.MessageStreamEventUnion]) sdkObservation {
	var observed sdkObservation
	for stream.Next() {
		event := stream.Current()
		if err := observed.message.Accumulate(event); err != nil {
			observed.err = err
			break
		}
		if event.Type == "message_stop" {
			observed.stopped = true
			break
		}
	}
	observed.err = errors.Join(observed.err, stream.Err(), stream.Close())
	return observed
}

func observeSDKWire(wire string) sdkObservation {
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(wire)),
	}
	return observeSDK(ssestream.NewStream[sdk.MessageStreamEventUnion](ssestream.NewDecoder(response), nil))
}

func TestSDKProbeNativeFixtures(t *testing.T) {
	for name, wire := range nativeVariationFixtures() {
		t.Run(name, func(t *testing.T) {
			want, err := decodeMessage(strings.NewReader(wire))
			if err != nil {
				t.Fatal("prototype positive control failed")
			}
			got := observeSDKWire(wire)
			if got.err != nil || !got.stopped {
				t.Fatalf("SDK failed a normal fixture: %v, stopped=%v", got.err, got.stopped)
			}
			// A usage-only message_delta clears the SDK's top-level stop reason.
			// Pin that exception separately below; don't silently normalize it here.
			if name != "usage updates" && string(got.message.StopReason) != want.StopReason {
				t.Fatal("finish reason changed")
			}
			if got.message.Usage.InputTokens != int64(want.Usage.InputTokens) || got.message.Usage.OutputTokens != int64(want.Usage.OutputTokens) || got.message.Usage.CacheReadInputTokens != int64(want.Usage.CacheReadTokens) || got.message.Usage.CacheCreationInputTokens != int64(want.Usage.CacheCreationTokens) {
				t.Fatal("usage counters changed")
			}
			assertSDKContent(t, got.message, want)
		})
	}
}

func assertSDKContent(t *testing.T, got sdk.Message, want message) {
	t.Helper()
	actual, err := got.ToParam().MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(actual, &envelope) != nil || envelope.Role != "assistant" {
		t.Fatal("invalid replay envelope")
	}
	expected, err := want.replayContent()
	if err != nil {
		t.Fatal(err)
	}
	// Compare JSON values without float64 conversion. Whitespace/key order
	// are not replay claims, but array order, empty fields and numbers are.
	decode := func(data []byte) any {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil {
			t.Fatal("invalid replay JSON")
		}
		return value
	}
	if !reflect.DeepEqual(decode(envelope.Content), decode(expected)) {
		t.Fatalf("SDK replay differs from expected synthetic content: %s", envelope.Content)
	}
}

func TestSDKProbeIndexedOpenBlocks(t *testing.T) {
	wire := start() +
		blockStart(0, `{"type":"thinking","thinking":""}`) +
		blockStart(1, `{"type":"text","text":""}`) +
		blockStart(2, `{"type":"tool_use","id":"tool_1","name":"read_file","input":{}}`) +
		delta(1, "text_delta", "text", "visible") +
		delta(2, "input_json_delta", "partial_json", `{"n":9007199254740993}`) +
		delta(0, "signature_delta", "signature", "fixture-signature") +
		blockStop(1) + blockStop(2) + blockStop(0) + stop("tool_use")
	got := observeSDKWire(wire)
	if got.err != nil || !got.stopped || got.message.StopReason != "tool_use" {
		t.Fatalf("SDK failed indexed open blocks: %v", got.err)
	}
	assertSDKContent(t, got.message, message{Content: []contentBlock{
		{Type: "thinking", Thinking: "", Signature: "fixture-signature"},
		{Type: "text", Text: "visible"},
		{Type: "tool_use", ID: "tool_1", Name: "read_file", Input: json.RawMessage(`{"n":9007199254740993}`)},
	}})
	// The production replacement must now accept the same indexed grammar.
	strict, err := decodeMessage(strings.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	assertSDKContent(t, got.message, strict)
}

func TestSDKProbeHostileFixtureMatrix(t *testing.T) {
	// Run every existing hostile fixture through the SDK as well as the strict
	// prototype, including cases not selected for detailed assertions below.
	for name, wire := range rejectedFixtures() {
		t.Run(name, func(t *testing.T) {
			assertRejected(t, wire)
			got := observeSDKWire(wire)
			t.Logf("SDK error=%t, message_stop=%t, content_blocks=%d", got.err != nil, got.stopped, len(got.message.Content))
		})
	}
}

func TestSDKProbeMissingTerminalIsNotAnSDKError(t *testing.T) {
	wire := strings.TrimSuffix(start()+textBlock(0, "partial")+stop("end_turn"), frame("message_stop", ""))
	got := observeSDKWire(wire)
	if got.err != nil || got.stopped || len(got.message.Content) != 1 {
		t.Fatal("SDK EOF behavior changed; revisit explicit completion admission")
	}
	assertRejected(t, wire)
}

func TestSDKProbeTruncatedToolInputIsRepaired(t *testing.T) {
	wire := start() + toolBlock(0, "tool_1", `{"path":`) + stop("tool_use")
	got := observeSDKWire(wire)
	if got.err != nil || !got.stopped || len(got.message.Content) != 1 || string(got.message.Content[0].Input) != "{}" {
		t.Fatal("SDK truncation behavior changed; revisit pre-accumulation validation")
	}
	assertRejected(t, wire)
}

func TestSDKProbeDuplicateFieldsAreNotRejected(t *testing.T) {
	wire := rejectedFixtures()["duplicate fields"]
	got := observeSDKWire(wire)
	if got.err != nil || !got.stopped {
		t.Fatal("SDK duplicate-key behavior changed; revisit raw JSON validation")
	}
	assertRejected(t, wire)
}

func TestSDKProbeUsageOnlyDeltaClearsStopReason(t *testing.T) {
	got := observeSDKWire(nativeVariationFixtures()["usage updates"])
	if got.err != nil || !got.stopped || got.message.StopReason != "" || got.message.Usage.OutputTokens != 18 {
		t.Fatal("SDK usage-only delta behavior changed; revisit completion state tracking")
	}
}

type probeTransport func(*http.Request) (*http.Response, error)

func (transport probeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type probeBody struct {
	io.Reader
	closes int
}

func (body *probeBody) Close() error { body.closes++; return nil }

func probeClient(transport http.RoundTripper) sdk.Client {
	return sdk.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL("https://api.deepseek.com/anthropic"),
		option.WithAPIKey("fixture-only-not-a-credential"),
		option.WithMaxRetries(0),
		option.WithHTTPClient(&http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}),
	)
}

func probeParams() sdk.MessageNewParams {
	return sdk.MessageNewParams{
		Model: "deepseek-flash", MaxTokens: 4096,
		Messages: []sdk.MessageParam{sdk.NewUserMessage(sdk.NewTextBlock("local fixture"))},
	}
}

func TestSDKProbeHTTPUsesExplicitRouteAndIgnoresAmbientCredentials(t *testing.T) {
	// All requests end at the injected transport, even if the isolation guard
	// regresses. No socket or external model call is possible in this test.
	t.Setenv("ANTHROPIC_BASE_URL", "https://ambient.invalid")
	// Force the ambient bearer branch: an explicit API key would overwrite
	// an ambient API key and mask removal of WithoutEnvironmentDefaults.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "ambient-fixture-token")
	t.Setenv("ANTHROPIC_PROFILE", "nonexistent-fixture-profile")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Fixture-Ambient: forbidden")
	body := &probeBody{Reader: strings.NewReader(start() + textBlock(0, "ok") + stop("end_turn"))}
	calls := 0
	client := probeClient(probeTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPost || request.URL.String() != "https://api.deepseek.com/anthropic/v1/messages" || request.Header.Get("x-api-key") != "fixture-only-not-a-credential" || request.Header.Get("Authorization") != "" || request.Header.Get("X-Fixture-Ambient") != "" {
			t.Fatal("SDK inherited ambient configuration or changed native route")
		}
		var payload struct {
			Model     string `json:"model"`
			Stream    bool   `json:"stream"`
			MaxTokens int    `json:"max_tokens"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil || payload.Model != "deepseek-flash" || !payload.Stream || payload.MaxTokens != 4096 {
			t.Fatal("SDK native request fields differ")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body, Request: request}, nil
	}))
	got := observeSDK(client.Messages.NewStreaming(context.Background(), probeParams()))
	if got.err != nil || !got.stopped || calls != 1 || body.closes != 1 {
		t.Fatalf("request/close behavior differs: err=%v calls=%d closes=%d", got.err, calls, body.closes)
	}
}

func TestSDKProbeRetriesRedirectsAndErrorsStayUnderCallerControl(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			client := probeClient(probeTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if status == http.StatusTemporaryRedirect && calls > 1 {
					return nil, errors.New("fixture prevented follow-up redirect")
				}
				return &http.Response{
					StatusCode: status, Request: request,
					Header: http.Header{"Content-Type": {"application/json"}, "Location": {"https://redirect.invalid"}},
					Body:   io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"api_error","message":"sensitive-fixture-body"}}`)),
				}, nil
			}))
			got := observeSDK(client.Messages.NewStreaming(context.Background(), probeParams()))
			if calls != 1 {
				t.Fatalf("SDK retried or followed redirect: calls=%d", calls)
			}
			if status == http.StatusTemporaryRedirect {
				// The redirect-safe HTTP client stops the second request, but
				// SDK streaming treats the 307 body as an empty, error-free
				// stream. OCH must reject status/content-type before decoding.
				if got.err != nil || got.stopped || len(got.message.Content) != 0 {
					t.Fatal("SDK redirect behavior changed; revisit HTTP response admission")
				}
				return
			}
			if got.err == nil {
				t.Fatal("SDK accepted a provider error")
			}
			var apiError *sdk.Error
			if !errors.As(got.err, &apiError) || apiError.Request == nil || apiError.Response == nil || !strings.Contains(got.err.Error(), "sensitive-fixture-body") {
				t.Fatal("SDK error shape changed; revisit safe error projection")
			}
		})
	}
}

func TestSDKProbeToolContinuationPreservesBlocks(t *testing.T) {
	firstWire := start() + thinkingBlock(0, "fixture reasoning", "signature-one") +
		toolBlock(1, "tool_1", `{"n":9007199254740993}`) +
		thinkingBlock(2, "", "signature-two") + toolBlock(3, "tool_2", `{}`) + stop("tool_use")
	expected, err := decodeMessage(strings.NewReader(firstWire))
	if err != nil {
		t.Fatal("positive control failed")
	}
	var bodies []*probeBody
	calls := 0
	client := probeClient(probeTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		var payload struct {
			Messages []struct {
				Role    string                       `json:"role"`
				Content []map[string]json.RawMessage `json:"content"`
			} `json:"messages"`
			Thinking struct {
				Type string `json:"type"`
			} `json:"thinking"`
		}
		if json.NewDecoder(request.Body).Decode(&payload) != nil || payload.Thinking.Type != "enabled" {
			t.Fatal("native thinking configuration changed")
		}
		wire := firstWire
		if calls == 2 {
			if len(payload.Messages) != 3 || payload.Messages[1].Role != "assistant" || payload.Messages[2].Role != "user" {
				t.Fatal("tool results are not in the immediately following user message")
			}
			blocks := payload.Messages[1].Content
			if len(blocks) != 4 || string(blocks[0]["signature"]) != `"signature-one"` || string(blocks[1]["input"]) != `{"n":9007199254740993}` || string(blocks[2]["thinking"]) != `""` || string(blocks[2]["signature"]) != `"signature-two"` {
				t.Fatal("signed block associations or exact tool input changed on HTTP replay")
			}
			results := payload.Messages[2].Content
			if len(results) != 2 || string(results[0]["type"]) != `"tool_result"` || string(results[0]["tool_use_id"]) != `"tool_1"` || string(results[1]["tool_use_id"]) != `"tool_2"` {
				t.Fatal("parallel tool results were not grouped in call order")
			}
			wire = start() + textBlock(0, "done") + stop("end_turn")
		}
		body := &probeBody{Reader: strings.NewReader(wire)}
		bodies = append(bodies, body)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body, Request: request}, nil
	}))
	params := probeParams()
	params.Thinking = sdk.ThinkingConfigParamUnion{OfEnabled: &sdk.ThinkingConfigEnabledParam{BudgetTokens: 1024}}
	first := observeSDK(client.Messages.NewStreaming(context.Background(), params))
	if first.err != nil || !first.stopped {
		t.Fatalf("first fixture request failed: %v", first.err)
	}
	assertSDKContent(t, first.message, expected)
	params.Messages = append(params.Messages, first.message.ToParam(), sdk.NewUserMessage(
		sdk.NewToolResultBlock("tool_1", "fixture result one", false),
		sdk.NewToolResultBlock("tool_2", "fixture result two", false),
	))
	second := observeSDK(client.Messages.NewStreaming(context.Background(), params))
	if second.err != nil || !second.stopped || second.message.StopReason != "end_turn" || calls != 2 {
		t.Fatal("tool continuation did not complete in two local requests")
	}
	for _, body := range bodies {
		if body.closes != 1 {
			t.Fatal("SDK response body was not closed exactly once")
		}
	}
}

func TestSDKProbeRedactedContentRoundTrip(t *testing.T) {
	// Native shape only. DeepSeek's documentation explicitly does not support
	// this block, so this is not part of the selected gateway compatibility claim.
	wire := start() + blockStart(0, `{"type":"redacted_thinking","data":"opaque-fixture"}`) + blockStop(0) + textBlock(1, "ok") + stop("end_turn")
	want, err := decodeMessage(strings.NewReader(wire))
	if err != nil {
		t.Fatal("positive control failed")
	}
	got := observeSDKWire(wire)
	if got.err != nil || !got.stopped {
		t.Fatal("SDK failed redacted-thinking fixture")
	}
	assertSDKContent(t, got.message, want)
}
