package openaicompat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestRunTurnHTTPSuccessRecordsRequestUsageAndReplay(t *testing.T) {
	transport := &countingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("x-request-id", "req-fixture")
		resp := sseResponse(http.StatusOK, loadSSE(t, "success.sse"), header)
		resp.Body = io.NopCloser(&delayedReader{rest: []byte(loadSSE(t, "success.sse")), delay: 2 * time.Millisecond})
		return resp, nil
	}}
	store := newMemoryStore(t)
	service, model := MustComposeHTTP(t, store, fixtureConfig(transport))
	identity := model.Identity()
	sessionID := createSession(t, service)
	sink := &testkit.RecordingSink{}
	const input = "inspect"

	result, err := service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: sessionID,
		RequestID: "request-success",
		Input:     input,
		Sink:      sink,
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if result.Status != domain.TurnStatusCompleted || result.Text != "Hello world" || !result.TerminalCommitted {
		t.Fatalf("RunTurn() result = %#v", result)
	}
	if got, want := eventTypes(result.Records), []string{
		domain.EventTurnStarted,
		domain.EventAssistantMessageStarted,
		domain.EventModelRequestRecorded,
		domain.EventModelUsageRecorded,
		domain.EventAssistantMessageCompleted,
		domain.EventTurnCompleted,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("result record types = %v, want %v", got, want)
	}

	recorded, ok := result.Records[2].Event.(domain.ModelRequestRecorded)
	if !ok {
		t.Fatalf("admission companion = %#v", result.Records[2].Event)
	}
	if recorded.AdapterFamily != identity.AdapterFamily || recorded.ModelID != identity.ModelID || recorded.EndpointID != identity.EndpointID {
		t.Fatalf("request identity = %#v, want %#v", recorded, identity)
	}
	if !recorded.IncludeUsage || recorded.MaxTokensField != identity.MaxTokensField || recorded.MaxTokensField != "max_tokens" {
		t.Fatalf("request hints = include=%t field=%q", recorded.IncludeUsage, recorded.MaxTokensField)
	}
	if recorded.ContextWindowTokens != 8192 || recorded.MaxOutputTokens != 4096 {
		t.Fatalf("request profile tokens = %#v", recorded)
	}
	if !reflect.DeepEqual(recorded.Messages, []domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: input}}) {
		t.Fatalf("request messages = %#v", recorded.Messages)
	}

	usage, ok := result.Records[3].Event.(domain.ModelUsageRecorded)
	if !ok || usage.InputTokens != 8 || usage.OutputTokens != 2 || usage.CachedInputTokens != 3 {
		t.Fatalf("usage tokens = %#v", result.Records[3].Event)
	}
	if usage.FinishReason != "stop" || usage.ProviderRequestID != "req-fixture" || usage.LatencyMs == 0 {
		t.Fatalf("usage metadata = %#v, want finish/request-id/latency", usage)
	}
	completed, ok := result.Records[4].Event.(domain.AssistantMessageCompleted)
	if !ok || completed.Text != "Hello world" {
		t.Fatalf("completed = %#v, want SSE delta text only", result.Records[4].Event)
	}

	runtime := sink.Delivered()
	if got := runtimeTypes(runtime); !reflect.DeepEqual(got, []engine.RuntimeEventType{
		engine.RuntimeModelStreamStarted,
		engine.RuntimeModelTextDelta,
		engine.RuntimeModelTextDelta,
		engine.RuntimeAppendCompleted,
		engine.RuntimeModelStreamCompleted,
	}) {
		t.Fatalf("runtime types = %v", got)
	}
	if runtime[1].Text != "Hello" || runtime[2].Text != " world" {
		t.Fatalf("runtime deltas = %q, %q", runtime[1].Text, runtime[2].Text)
	}

	reconstructed := reconstructRequest(t, store, sessionID, "request-success", input)
	if len(reconstructed.Records) != 6 || reconstructed.Status != domain.TurnStatusCompleted || reconstructed.Text != "Hello world" {
		t.Fatalf("reconstructed = %#v, want 6-event HTTP+usage success", reconstructed)
	}
	if got := eventTypes(reconstructed.Records); !reflect.DeepEqual(got, eventTypes(result.Records)) {
		t.Fatalf("reconstructed types = %v, want %v", got, eventTypes(result.Records))
	}

	assertReplayDoesNotStream(t, service, transport, sessionID, "request-success", input, result)
	assertNoSecrets(t, nil, result.Records)
}

func TestRunTurnHTTP401PersistsProviderAuth(t *testing.T) {
	transport := &countingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return jsonError(http.StatusUnauthorized, `{"error":{"message":"Invalid Authorization: Bearer sk-secret"}}`), nil
	}}
	store := newMemoryStore(t)
	service, _ := MustComposeHTTP(t, store, fixtureConfig(transport))
	sessionID := createSession(t, service)

	result, err := runTurn(t, service, sessionID, "request-401", "inspect")
	assertApplicationError(t, err, application.CategoryModel, string(engine.CodeModelStartup), true)
	var failure *engine.ProviderFailure
	if !errors.As(err, &failure) || failure.Code != "provider_auth" {
		t.Fatalf("error = %v, want ProviderFailure provider_auth", err)
	}
	assertFailedTerminal(t, result, "provider_auth", "provider rejected credentials")
	if got, want := eventTypes(result.Records), []string{
		domain.EventTurnStarted,
		domain.EventAssistantMessageStarted,
		domain.EventModelRequestRecorded,
		domain.EventAssistantMessageFailed,
		domain.EventTurnFailed,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("result record types = %v, want 5-event HTTP without usage %v", got, want)
	}

	reconstructed := reconstructRequest(t, store, sessionID, "request-401", "inspect")
	if len(reconstructed.Records) != 5 || reconstructed.Status != domain.TurnStatusFailed {
		t.Fatalf("reconstructed = %#v, want 5-event failed HTTP shape", reconstructed)
	}
	assertReplayDoesNotStream(t, service, transport, sessionID, "request-401", "inspect", result)
	assertNoSecrets(t, err, result.Records)
}

func TestRunTurnHTTP429QuotaVersusRateLimit(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		durable string
		message string
	}{
		{name: "rate limit", body: `{"error":{"message":"slow down","type":"rate_limit_exceeded"}}`, durable: "provider_rate_limit", message: "provider rate limited"},
		{name: "quota substring", body: `{"error":{"message":"You exceeded your current quota"}}`, durable: "provider_quota", message: "provider quota exhausted"},
		{name: "quota code", body: `{"error":{"code":"insufficient_quota","message":"pay"}}`, durable: "provider_quota", message: "provider quota exhausted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &countingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
				return jsonError(http.StatusTooManyRequests, test.body), nil
			}}
			store := newMemoryStore(t)
			service, _ := MustComposeHTTP(t, store, fixtureConfig(transport))
			sessionID := createSession(t, service)
			result, err := runTurn(t, service, sessionID, "request-429", "inspect")
			assertApplicationError(t, err, application.CategoryModel, string(engine.CodeModelStartup), true)
			assertFailedTerminal(t, result, test.durable, test.message)
			if transport.requests.Load() != 1 {
				t.Fatalf("streams = %d, want 1", transport.requests.Load())
			}
		})
	}
}

func TestRunTurnHTTPCancelWinsWithoutSecondStream(t *testing.T) {
	started := make(chan struct{})
	transport := &countingTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
		pr, pw := io.Pipe()
		go func() {
			<-req.Context().Done()
			_ = pw.CloseWithError(req.Context().Err())
		}()
		close(started)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       pr,
			Request:    req,
		}, nil
	}}
	store := newMemoryStore(t)
	service, _ := MustComposeHTTP(t, store, fixtureConfig(transport))
	sessionID := createSession(t, service)
	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		result application.RunTurnResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := service.RunTurn(ctx, application.RunTurnRequest{
			SessionID: sessionID,
			RequestID: "request-cancel",
			Input:     "inspect",
			Sink:      &testkit.RecordingSink{},
		})
		done <- outcome{result, err}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("Stream was not entered")
	}
	cancel()
	var got outcome
	select {
	case got = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunTurn did not return after cancel")
	}
	assertApplicationError(t, got.err, application.CategoryCanceled, "canceled", true)
	if got.result.Status != domain.TurnStatusInterrupted || !got.result.TerminalCommitted {
		t.Fatalf("result = %#v", got.result)
	}
	interrupted, ok := itemTerminal(got.result.Records).(domain.AssistantMessageInterrupted)
	if !ok || interrupted.Code != domain.InterruptionCallerCanceled {
		t.Fatalf("item terminal = %#v", itemTerminal(got.result.Records))
	}
	if transport.requests.Load() != 1 {
		t.Fatalf("streams = %d, want 1", transport.requests.Load())
	}
	assertReplayDoesNotStream(t, service, transport, sessionID, "request-cancel", "inspect", got.result)
}

func TestRunTurnHTTPEmptyCompletion(t *testing.T) {
	transport := &countingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return sseResponse(http.StatusOK, loadSSE(t, "empty.sse"), nil), nil
	}}
	store := newMemoryStore(t)
	service, _ := MustComposeHTTP(t, store, fixtureConfig(transport))
	sessionID := createSession(t, service)
	result, err := runTurn(t, service, sessionID, "request-empty", "inspect")
	assertApplicationError(t, err, application.CategoryModel, string(engine.CodeInvalidStream), true)
	assertFailedTerminal(t, result, "empty_response", "provider returned an empty completion")
}

func TestRunTurnHTTPReasoningIsolation(t *testing.T) {
	transport := &countingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return sseResponse(http.StatusOK, loadSSE(t, "reasoning.sse"), nil), nil
	}}
	store := newMemoryStore(t)
	service, _ := MustComposeHTTP(t, store, fixtureConfig(transport))
	sessionID := createSession(t, service)
	result, err := runTurn(t, service, sessionID, "request-reasoning", "inspect")
	if err != nil || result.Status != domain.TurnStatusCompleted || result.Text != "Visible" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	completed, ok := itemTerminal(result.Records).(domain.AssistantMessageCompleted)
	if !ok || completed.Text != "Visible" {
		t.Fatalf("completed = %#v", itemTerminal(result.Records))
	}
	for _, record := range result.Records {
		canonical, marshalErr := domain.MarshalRecordedEvent(record)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		text := string(canonical) + fmt.Sprintf("%#v", record.Event)
		for _, leaked := range []string{"secret", "hidden", "nope"} {
			if strings.Contains(text, leaked) {
				t.Fatalf("reasoning leaked %q into %s", leaked, canonical)
			}
		}
	}
}

func TestRunTurnHTTPSecretRedaction(t *testing.T) {
	const vendorBody = `{"error":{"message":"Invalid Authorization: Bearer sk-secret key=sk-also"}}`
	transport := &countingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return jsonError(http.StatusUnauthorized, vendorBody), nil
	}}
	store := newMemoryStore(t)
	service, _ := MustComposeHTTP(t, store, fixtureConfig(transport))
	sessionID := createSession(t, service)
	result, err := runTurn(t, service, sessionID, "request-redact", "inspect")
	assertApplicationError(t, err, application.CategoryModel, string(engine.CodeModelStartup), true)
	assertFailedTerminal(t, result, "provider_auth", "provider rejected credentials")

	records, readErr := application.ReadWholeStreamPinned(context.Background(), store, sessionID, 256)
	if readErr != nil {
		t.Fatal(readErr)
	}
	assertNoSecrets(t, err, records, vendorBody)
	if !strings.Contains(vendorBody, "Authorization") || !strings.Contains(vendorBody, "Bearer ") || !strings.Contains(vendorBody, "sk-") {
		t.Fatal("fixture input no longer contains secrets to redact")
	}
}

func TestRunTurnHTTPReadFileThenCompletes(t *testing.T) {
	const fileBody = "fixture file body"
	var bodies []map[string]any
	transport := &countingTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
		payload := decodeExternalRequestBody(t, req)
		bodies = append(bodies, payload)
		header := make(http.Header)
		header.Set("x-request-id", fmt.Sprintf("req-tool-%d", len(bodies)))
		if len(bodies) == 1 {
			return sseResponse(http.StatusOK, loadSSE(t, "tools_read_file.sse"), header), nil
		}
		return sseResponse(http.StatusOK, loadSSE(t, "success.sse"), header), nil
	}}
	store := newMemoryStore(t)
	fs := testkit.NewMemFS("/workspace")
	fs.AddFile("README.md", []byte(fileBody))
	service, _ := MustComposeHTTPTools(t, store, fixtureToolsConfig(transport), fs)
	sessionID := createSession(t, service)

	result, err := runTurn(t, service, sessionID, "request-tools", "inspect")
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	if result.Status != domain.TurnStatusCompleted || result.Text != "Hello world" || !result.TerminalCommitted {
		t.Fatalf("RunTurn() result = %#v", result)
	}
	if transport.requests.Load() != 2 {
		t.Fatalf("streams = %d, want 2", transport.requests.Load())
	}
	if len(bodies) != 2 {
		t.Fatalf("captured bodies = %d", len(bodies))
	}
	assertSentReadFileTool(t, bodies[0])
	assertSecondStreamSeesToolMessage(t, bodies[1], fileBody)

	usageReasons := usageFinishReasons(result.Records)
	if len(usageReasons) < 2 || usageReasons[0] != domain.FinishReasonToolCalls || usageReasons[len(usageReasons)-1] != "stop" {
		t.Fatalf("usage finishReason = %v, want tool_calls then stop", usageReasons)
	}

	second, replayErr := runTurn(t, service, sessionID, "request-tools", "inspect")
	if replayErr != nil || second.Text != result.Text || transport.requests.Load() != 2 {
		t.Fatalf("replay streamed again: result=%#v err=%v streams=%d", second, replayErr, transport.requests.Load())
	}
}

func TestRunTurnHTTPProfileTextOnlyToolCallsLeaveFinishReasonEmpty(t *testing.T) {
	transport := &countingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return sseResponse(http.StatusOK, loadSSE(t, "tool_calls.sse"), nil), nil
	}}
	store := newMemoryStore(t)
	service, _ := MustComposeHTTP(t, store, fixtureConfig(transport))
	sessionID := createSession(t, service)
	result, err := runTurn(t, service, sessionID, "request-mismatch", "inspect")
	assertApplicationError(t, err, application.CategoryModel, string(engine.CodeInvalidStream), true)
	assertFailedTerminal(t, result, "capability_mismatch", "provider returned an unsupported capability")
	for _, record := range result.Records {
		if usage, ok := record.Event.(domain.ModelUsageRecorded); ok && usage.FinishReason != "" {
			t.Fatalf("mismatch usage finishReason = %q, want empty", usage.FinishReason)
		}
	}
}

func assertSentReadFileTool(t *testing.T, payload map[string]any) {
	t.Helper()
	if _, ok := payload["tool_choice"]; ok {
		t.Fatalf("tool_choice present: %#v", payload)
	}
	toolsRaw, ok := payload["tools"].([]any)
	if !ok || len(toolsRaw) == 0 {
		t.Fatalf("tools = %#v", payload["tools"])
	}
	found := false
	for _, raw := range toolsRaw {
		tool, _ := raw.(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		if tool["type"] == "function" && fn["name"] == tools.NameReadFile {
			found = true
		}
	}
	if !found {
		t.Fatalf("read_file not sent: %#v", toolsRaw)
	}
}

func assertSecondStreamSeesToolMessage(t *testing.T, payload map[string]any, wantBody string) {
	t.Helper()
	messages, ok := payload["messages"].([]any)
	if !ok {
		t.Fatalf("messages = %#v", payload["messages"])
	}
	var sawTool bool
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		if message["role"] == "tool" && message["tool_call_id"] == "call_read" && message["content"] == wantBody {
			sawTool = true
		}
	}
	if !sawTool {
		t.Fatalf("second stream missing tool message %q: %#v", wantBody, messages)
	}
}

func usageFinishReasons(records []domain.RecordedEvent) []string {
	var reasons []string
	for _, record := range records {
		if usage, ok := record.Event.(domain.ModelUsageRecorded); ok {
			reasons = append(reasons, usage.FinishReason)
		}
	}
	return reasons
}

func decodeExternalRequestBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	data, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	copied := append([]byte(nil), data...)
	var payload map[string]any
	if err := json.Unmarshal(copied, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestRunTurnHTTPFindCommandRequestPreventsSecondStream(t *testing.T) {
	transport := &countingTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("x-request-id", "req-replay")
		return sseResponse(http.StatusOK, loadSSE(t, "success.sse"), header), nil
	}}
	store := newMemoryStore(t)
	service, _ := MustComposeHTTP(t, store, fixtureConfig(transport))
	sessionID := createSession(t, service)
	first, err := runTurn(t, service, sessionID, "request-replay", "inspect")
	if err != nil || first.Status != domain.TurnStatusCompleted {
		t.Fatalf("first = %#v, err = %v", first, err)
	}
	lookup := findRequest(t, store, sessionID, "request-replay", "inspect")
	if lookup.Kind != application.CommandRequestLookupFound || lookup.Record == nil {
		t.Fatalf("FindCommandRequest = %#v, want found", lookup)
	}
	assertReplayDoesNotStream(t, service, transport, sessionID, "request-replay", "inspect", first)
}

type delayedReader struct {
	rest  []byte
	delay time.Duration
}

func (reader *delayedReader) Read(p []byte) (int, error) {
	if reader.delay > 0 {
		time.Sleep(reader.delay)
		reader.delay = 0
	}
	if len(reader.rest) == 0 {
		return 0, io.EOF
	}
	n := copy(p, reader.rest)
	reader.rest = reader.rest[n:]
	return n, nil
}

type countingTransport struct {
	roundTrip func(*http.Request) (*http.Response, error)
	requests  atomic.Int32
}

func (transport *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	transport.requests.Add(1)
	return transport.roundTrip(req)
}

func runTurn(t *testing.T, service *application.Service, sessionID domain.SessionID, requestID, input string) (application.RunTurnResult, error) {
	t.Helper()
	return service.RunTurn(context.Background(), application.RunTurnRequest{
		SessionID: sessionID,
		RequestID: domain.RunTurnRequestID(requestID),
		Input:     input,
		Sink:      &testkit.RecordingSink{},
	})
}

func findRequest(t *testing.T, store application.EventStore, sessionID domain.SessionID, requestID, input string) application.CommandRequestLookup {
	t.Helper()
	digest, err := application.DigestRunTurnRequestV1(sessionID, input)
	if err != nil {
		t.Fatal(err)
	}
	lookup, err := store.FindCommandRequest(context.Background(), application.FindCommandRequestRequest{
		RunTurnRequestID: domain.RunTurnRequestID(requestID),
		SessionID:        sessionID,
		RequestDigest:    digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	return lookup
}

func reconstructRequest(t *testing.T, store application.EventStore, sessionID domain.SessionID, requestID, input string) application.RunTurnResult {
	t.Helper()
	lookup := findRequest(t, store, sessionID, requestID, input)
	if lookup.Kind != application.CommandRequestLookupFound || lookup.Record == nil {
		t.Fatalf("FindCommandRequest = %#v, want found", lookup)
	}
	records, err := application.ReadWholeStreamPinned(context.Background(), store, sessionID, 256)
	if err != nil {
		t.Fatal(err)
	}
	result, err := application.ReconstructRequestResult(*lookup.Record, records)
	if err != nil {
		t.Fatalf("ReconstructRequestResult() error = %v", err)
	}
	return result
}

func assertReplayDoesNotStream(t *testing.T, service *application.Service, transport *countingTransport, sessionID domain.SessionID, requestID, input string, first application.RunTurnResult) {
	t.Helper()
	if transport.requests.Load() != 1 {
		t.Fatalf("first turn streams = %d, want 1", transport.requests.Load())
	}
	second, err := runTurn(t, service, sessionID, requestID, input)
	if first.Status == domain.TurnStatusCompleted {
		if err != nil {
			t.Fatalf("replay error = %v", err)
		}
	} else if err == nil {
		t.Fatal("replay error = nil, want durable terminal error")
	}
	if second.Status != first.Status || second.Text != first.Text || second.TerminalCommitted != first.TerminalCommitted {
		t.Fatalf("replay result = %#v, want %#v", second, first)
	}
	if transport.requests.Load() != 1 {
		t.Fatalf("FindCommandRequest allowed a second Stream: requests=%d", transport.requests.Load())
	}
}

func assertFailedTerminal(t *testing.T, result application.RunTurnResult, code, message string) {
	t.Helper()
	if result.Status != domain.TurnStatusFailed || result.Text != "" || !result.TerminalCommitted {
		t.Fatalf("result = %#v", result)
	}
	failed, ok := itemTerminal(result.Records).(domain.AssistantMessageFailed)
	if !ok || failed.Code != code || failed.Message != message {
		t.Fatalf("item terminal = %#v, want %s / %s", itemTerminal(result.Records), code, message)
	}
	turn, ok := result.Records[len(result.Records)-1].Event.(domain.TurnFailed)
	if !ok || turn.Code != code || turn.Message != message {
		t.Fatalf("turn terminal = %#v", result.Records[len(result.Records)-1].Event)
	}
}

func assertApplicationError(t *testing.T, err error, category application.ErrorCategory, code string, terminal bool) {
	t.Helper()
	var appErr *application.Error
	if !errors.As(err, &appErr) || appErr == nil {
		t.Fatalf("error = %v, want *application.Error", err)
	}
	if appErr.Category != category || appErr.Code != code || appErr.TerminalCommitted != terminal {
		t.Fatalf("application error = %#v, want %s/%s terminal=%t", appErr, category, code, terminal)
	}
}

func assertNoSecrets(t *testing.T, err error, records []domain.RecordedEvent, excludedInputs ...string) {
	t.Helper()
	texts := make([]string, 0, 8)
	if err != nil {
		texts = append(texts, unwrapErrorTexts(err)...)
		var failure *engine.ProviderFailure
		if errors.As(err, &failure) && failure != nil {
			texts = append(texts, failure.SafeMessage, failure.RequestID, failure.Error())
		}
	}
	for _, record := range records {
		canonical, marshalErr := domain.MarshalRecordedEvent(record)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		texts = append(texts, string(canonical), fmt.Sprintf("%#v", record.Event))
	}
	for _, text := range texts {
		for _, input := range excludedInputs {
			if text == input {
				t.Fatalf("compared output to the secret input itself")
			}
		}
		for _, part := range []string{"Authorization", "Bearer ", "sk-"} {
			if strings.Contains(text, part) {
				t.Fatalf("classified or persisted text leaked %q: %q", part, text)
			}
		}
	}
}

func unwrapErrorTexts(err error) []string {
	seen := make(map[error]struct{})
	var texts []string
	var walk func(error)
	walk = func(err error) {
		if err == nil {
			return
		}
		if _, ok := seen[err]; ok {
			return
		}
		seen[err] = struct{}{}
		texts = append(texts, err.Error())
		switch unwrapped := err.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range unwrapped.Unwrap() {
				walk(child)
			}
		case interface{ Unwrap() error }:
			walk(unwrapped.Unwrap())
		}
	}
	walk(err)
	return texts
}

func itemTerminal(records []domain.RecordedEvent) domain.Event {
	for _, record := range records {
		switch event := record.Event.(type) {
		case domain.AssistantMessageCompleted, domain.AssistantMessageFailed, domain.AssistantMessageInterrupted:
			return event
		}
	}
	return nil
}

func eventTypes(records []domain.RecordedEvent) []string {
	types := make([]string, len(records))
	for index, record := range records {
		types[index] = record.Event.EventType()
	}
	return types
}

func runtimeTypes(events []engine.RuntimeEvent) []engine.RuntimeEventType {
	types := make([]engine.RuntimeEventType, len(events))
	for index, event := range events {
		types[index] = event.Type
	}
	return types
}

func loadSSE(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "sse", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func sseResponse(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	if header.Get("Content-Type") == "" {
		header.Set("Content-Type", "text/event-stream")
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func jsonError(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
