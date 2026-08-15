package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

type scriptedTransport struct {
	roundTrip func(*http.Request) (*http.Response, error)
	requests  atomic.Int32
}

func (t *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests.Add(1)
	return t.roundTrip(req)
}

type closeTracker struct {
	io.ReadCloser
	closed *atomic.Int32
}

func (c *closeTracker) Close() error {
	c.closed.Add(1)
	return c.ReadCloser.Close()
}

type errAfterReader struct {
	data []byte
	err  error
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func validConfig(rt http.RoundTripper) Config {
	return Config{
		BaseURL: "https://api.example.com/v1",
		ModelID: "test-model",
		APIKey:  StaticAPIKey{Value: "test-key"},
		Profile: ProfileTextOnly(8192, 0),
		Hints:   WireHints{IncludeUsage: true},
		HTTPClient: &http.Client{
			Transport: rt,
		},
	}
}

func newTestModel(t *testing.T, cfg Config) *Model {
	t.Helper()
	model, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return model
}

func modelRequest() engine.ModelRequest {
	return engine.ModelRequest{
		SessionID: domain.SessionID("session-1"),
		TurnID:    domain.TurnID("turn-1"),
		ItemID:    domain.ItemID("item-1"),
		Input:     "hello",
	}
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

func collectStream(t *testing.T, stream engine.ModelStream) ([]engine.StreamEvent, error) {
	t.Helper()
	var events []engine.StreamEvent
	for {
		event, err := stream.Next(context.Background())
		if err != nil {
			return events, err
		}
		events = append(events, event)
		if event.Type == engine.StreamEventCompleted {
			return events, nil
		}
	}
}

func requireProviderFailure(t *testing.T, err error, code engine.ErrorCode, durable string) *engine.ProviderFailure {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s / %s", code, durable)
	}
	var engineErr *engine.Error
	if !errors.As(err, &engineErr) || engineErr.Code != code {
		t.Fatalf("error = %v, want engine code %s", err, code)
	}
	var failure *engine.ProviderFailure
	if !errors.As(err, &failure) || failure == nil || failure.Code != durable {
		t.Fatalf("error = %v, want ProviderFailure %s", err, durable)
	}
	assertNoSecrets(t, err, failure)
	return failure
}

func assertNoSecrets(t *testing.T, err error, failure *engine.ProviderFailure) {
	t.Helper()
	texts := []string{err.Error(), failure.Error(), failure.SafeMessage}
	for _, text := range texts {
		for _, part := range []string{"Authorization", "Bearer ", "sk-"} {
			if strings.Contains(text, part) {
				t.Fatalf("classified text leaked %q: %q", part, text)
			}
		}
	}
}

func decodeRequestBody(t *testing.T, req *http.Request) map[string]any {
	t.Helper()
	data, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
