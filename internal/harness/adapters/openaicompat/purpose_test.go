package openaicompat

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// TestStreamCompactionPurposeIsAttributionOnly is Task 8's own required
// case: a compaction-purpose request must produce only an attribution
// (header) difference from an equivalent conversation-purpose request,
// never a different message/tool shape in the JSON body a model reads.
func TestStreamCompactionPurposeIsAttributionOnly(t *testing.T) {
	capture := func(purpose engine.ModelRequestPurpose) (*http.Request, map[string]any) {
		var seen *http.Request
		transport := &scriptedTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
			seen = req
			return sseResponse(http.StatusOK, loadSSE(t, "success.sse"), nil), nil
		}}
		model := newTestModel(t, validConfig(transport))
		request := modelRequest()
		request.Purpose = purpose
		stream, err := model.Stream(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stream.Close() })
		return seen, decodeRequestBody(t, seen)
	}

	conversationReq, conversationBody := capture(engine.ModelRequestPurposeConversation)
	compactionReq, compactionBody := capture(engine.ModelRequestPurposeCompaction)

	if conversationReq.Header.Get("X-Och-Request-Purpose") != string(engine.ModelRequestPurposeConversation) {
		t.Fatalf("conversation header = %q, want %q", conversationReq.Header.Get("X-Och-Request-Purpose"), engine.ModelRequestPurposeConversation)
	}
	if compactionReq.Header.Get("X-Och-Request-Purpose") != string(engine.ModelRequestPurposeCompaction) {
		t.Fatalf("compaction header = %q, want %q", compactionReq.Header.Get("X-Och-Request-Purpose"), engine.ModelRequestPurposeCompaction)
	}

	// The JSON bodies must be identical -- Purpose never touches them.
	if !reflect.DeepEqual(conversationBody, compactionBody) {
		t.Fatalf("bodies differ: conversation=%#v compaction=%#v", conversationBody, compactionBody)
	}
}

// TestStreamMaxOutputTokensOverridesRouteDefault confirms a positive
// per-request MaxOutputTokens overrides the route's own statically
// configured maximum at the wire level.
func TestStreamMaxOutputTokensOverridesRouteDefault(t *testing.T) {
	var seen *http.Request
	transport := &scriptedTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
		seen = req
		return sseResponse(http.StatusOK, loadSSE(t, "success.sse"), nil), nil
	}}
	cfg := validConfig(transport)
	cfg.Hints = WireHints{MaxTokensField: "max_tokens"}
	cfg.Profile.MaxOutputTokens = 100
	model := newTestModel(t, cfg)

	request := modelRequest()
	request.MaxOutputTokens = 42
	stream, err := model.Stream(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	payload := decodeRequestBody(t, seen)
	got, ok := payload["max_tokens"].(float64)
	if !ok || uint32(got) != 42 {
		t.Fatalf("max_tokens = %#v, want 42 (the per-request override, not the route default of 100)", payload["max_tokens"])
	}
}

// TestStreamMaxOutputTokensZeroFallsBackToRouteDefault confirms the
// backward-compatible fallback: a caller that predates this field (leaves
// MaxOutputTokens at its zero value) gets exactly the route's own
// configured value, unchanged from this adapter's original behavior.
func TestStreamMaxOutputTokensZeroFallsBackToRouteDefault(t *testing.T) {
	var seen *http.Request
	transport := &scriptedTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
		seen = req
		return sseResponse(http.StatusOK, loadSSE(t, "success.sse"), nil), nil
	}}
	cfg := validConfig(transport)
	cfg.Hints = WireHints{MaxTokensField: "max_tokens"}
	cfg.Profile.MaxOutputTokens = 100
	model := newTestModel(t, cfg)

	stream, err := model.Stream(context.Background(), modelRequest()) // MaxOutputTokens left at zero
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	payload := decodeRequestBody(t, seen)
	got, ok := payload["max_tokens"].(float64)
	if !ok || uint32(got) != 100 {
		t.Fatalf("max_tokens = %#v, want 100 (the route default)", payload["max_tokens"])
	}
}

func TestStreamRejectsMaxOutputTokensExceedingRouteMaximum(t *testing.T) {
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		t.Fatal("a request exceeding the route maximum must not send HTTP")
		return nil, nil
	}}
	cfg := validConfig(transport)
	cfg.Profile.MaxOutputTokens = 100
	model := newTestModel(t, cfg)

	request := modelRequest()
	request.MaxOutputTokens = 101
	_, err := model.Stream(context.Background(), request)
	requireProviderFailure(t, err, engine.CodeModelStartup, "provider_permanent")
}

// TestStreamQualityJudgePurposeIsAttributionOnly holds the evaluation
// quality judge to the same rule as every other purpose: it changes the
// attribution header and nothing a model reads. A judge request must not
// be able to acquire request-shape capabilities a conversation request
// does not have.
func TestStreamQualityJudgePurposeIsAttributionOnly(t *testing.T) {
	capture := func(purpose engine.ModelRequestPurpose) (*http.Request, map[string]any) {
		var seen *http.Request
		transport := &scriptedTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
			seen = req
			return sseResponse(http.StatusOK, loadSSE(t, "success.sse"), nil), nil
		}}
		model := newTestModel(t, validConfig(transport))
		request := modelRequest()
		request.Purpose = purpose
		stream, err := model.Stream(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stream.Close() })
		return seen, decodeRequestBody(t, seen)
	}

	conversationReq, conversationBody := capture(engine.ModelRequestPurposeConversation)
	judgeReq, judgeBody := capture(engine.ModelRequestPurposeQualityJudge)

	if got := judgeReq.Header.Get("X-Och-Request-Purpose"); got != string(engine.ModelRequestPurposeQualityJudge) {
		t.Fatalf("quality judge header = %q, want %q", got, engine.ModelRequestPurposeQualityJudge)
	}
	if conversationReq.Header.Get("X-Och-Request-Purpose") != string(engine.ModelRequestPurposeConversation) {
		t.Fatalf("conversation header = %q, want %q",
			conversationReq.Header.Get("X-Och-Request-Purpose"), engine.ModelRequestPurposeConversation)
	}
	if !reflect.DeepEqual(conversationBody, judgeBody) {
		t.Fatalf("bodies differ: conversation=%#v quality_judge=%#v", conversationBody, judgeBody)
	}
}
