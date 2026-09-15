package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

const overflowFixture = `{"error":{"type":"invalid_request_error","message":"This model's maximum context length is 100 tokens. However, you requested 110 tokens (90 in the messages, 20 in the completion). Please reduce the length of the messages or completion.","param":null,"code":"invalid_request_error"}}`

func TestMessagesOverflowClassification(t *testing.T) {
	short := `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long"},"request_id":"private-canary"}`
	cases := []struct {
		name, body string
		want       bool
	}{
		{"deepseek numeric", overflowFixture, true},
		{"exact byte limit", overflowFixture + strings.Repeat(" ", maxOverflowBodyBytes-len(overflowFixture)), true},
		{"messages short", short, true},
		{"messages numeric", strings.Replace(short, "prompt is too long", "prompt is too long: 110 tokens > 100 maximum", 1), true},
		{"other error type", strings.ReplaceAll(short, "invalid_request_error", "authentication_error"), false},
		{"generic token parameter", strings.Replace(short, "prompt is too long", "max_tokens must be positive", 1), false},
		{"quoted prompt", strings.Replace(short, "prompt is too long", "invalid input contains prompt is too long", 1), false},
		{"suffix canary", strings.Replace(short, "prompt is too long", "prompt is too long private-canary", 1), false},
		{"invented code", strings.Replace(overflowFixture, `"code":"invalid_request_error"`, `"code":"context_length_exceeded"`, 1), false},
		{"parameter error", strings.Replace(overflowFixture, `"param":null`, `"param":"max_tokens"`, 1), false},
		{"numeric code", strings.Replace(overflowFixture, `"code":"invalid_request_error"`, `"code":400`, 1), false},
		{"wrong top type", strings.Replace(short, `"type":"error"`, `"type":"message"`, 1), false},
		{"unknown field", strings.Replace(short, `"type":"error"`, `"type":"error","extra":"private-canary"`, 1), false},
		{"duplicate field", strings.Replace(short, `"type":"error"`, `"type":"message","type":"error"`, 1), false},
		{"duplicate escaped field", strings.Replace(short, `"type":"error"`, `"type":"message","\u0074ype":"error"`, 1), false},
		{"duplicate message", strings.Replace(short, `"message":"prompt`, `"message":"wrong","message":"prompt`, 1), false},
		{"lossy Unicode", strings.Replace(short, "private-canary", `\ud800`, 1), false},
		{"invalid utf8", strings.Replace(short, "private-canary", "\xff", 1), false},
		{"truncated", short[:len(short)-1], false},
		{"second JSON", short + `{}`, false},
		{"oversized complete prefix", short + strings.Repeat(" ", maxOverflowBodyBytes), false},
		{"no overflow", strings.Replace(overflowFixture, "110 tokens (90", "100 tokens (80", 1), false},
		{"inconsistent total", strings.Replace(overflowFixture, "90 in", "89 in", 1), false},
		{"output alone too big", strings.Replace(overflowFixture, "90 in the messages, 20", "10 in the messages, 100", 1), false},
		{"uint64 overflow", strings.Replace(overflowFixture, "110 tokens", "18446744073709551616 tokens", 1), false},
		{"negative count", strings.Replace(overflowFixture, "90 in", "-90 in", 1), false},
		{"numeric short equality", strings.Replace(short, "prompt is too long", "prompt is too long: 100 tokens > 100 maximum", 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := &countedBody{Reader: strings.NewReader(tc.body)}
			var calls atomic.Int32
			model := fixtureModel(t, func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: body}, nil
			})
			stream, err := model.Stream(context.Background(), engine.ModelRequest{Input: "synthetic"})
			var failure *engine.ProviderFailure
			var engineErr *engine.Error
			if stream != nil || !errors.As(err, &failure) || !errors.As(err, &engineErr) || engineErr.Code != engine.CodeModelStartup || failure.HTTPStatus != 400 || failure.Retryable || failure.Class != engine.FailureClassPermanent || calls.Load() != 1 || body.closes.Load() != 1 {
				t.Fatalf("HTTP startup/close/retry invariant: %v", err)
			}
			want := "provider_permanent"
			if tc.want {
				want = "context_overflow"
			}
			if failure.Code != want {
				t.Fatalf("code=%s want=%s", failure.Code, want)
			}
			encoded, marshalErr := json.Marshal(failure)
			if marshalErr != nil || strings.Contains(err.Error()+string(encoded), "private-canary") || failure.SafeMessage != "DeepSeek Messages request failed" && failure.SafeMessage != "DeepSeek Messages context window exceeded" {
				t.Fatal("vendor body escaped sanitized failure")
			}
		})
	}
}

type overflowCountingReader struct {
	reader io.Reader
	bytes  int
}

func (r *overflowCountingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytes += n
	return n, err
}

func TestMessagesOverflowBodyLimits(t *testing.T) {
	for _, closeFailure := range []bool{false, true} {
		reader := &overflowCountingReader{reader: strings.NewReader(overflowFixture + strings.Repeat(" ", maxOverflowBodyBytes*2))}
		body := &countedBody{Reader: reader}
		if closeFailure {
			reader.reader = strings.NewReader(overflowFixture)
			body.closeErr = errors.New("private-close-canary")
		}
		if readContextOverflow(context.Background(), body) || body.closes.Load() != 1 {
			t.Fatal("oversize/close failure admitted or body closed twice")
		}
		if !closeFailure && reader.bytes != maxOverflowBodyBytes+1 {
			t.Fatalf("read %d bytes; want bounded overflow sentinel", reader.bytes)
		}
	}
	body := &countedBody{Reader: &overflowReadFailure{payload: []byte(overflowFixture)}}
	if readContextOverflow(context.Background(), body) || body.closes.Load() != 1 {
		t.Fatal("read failure after matching JSON was admitted")
	}
}

type overflowReadFailure struct{ payload []byte }

func (r *overflowReadFailure) Read(p []byte) (int, error) {
	return copy(p, r.payload), io.ErrUnexpectedEOF
}

func TestMessagesOverflowHTTPDeadlineAndCancellation(t *testing.T) {
	for _, cancelCaller := range []bool{false, true} {
		name := "absolute deadline"
		if cancelCaller {
			name = "caller cancellation"
		}
		t.Run(name, func(t *testing.T) {
			entered, released := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				r.Body.Close()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				_, _ = io.WriteString(w, overflowFixture)
				w.(http.Flusher).Flush() // Valid prefix is insufficient without EOF.
				close(entered)
				ticker := time.NewTicker(50 * time.Millisecond)
				defer ticker.Stop()
				defer close(released)
				for {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
						_, _ = io.WriteString(w, " ") // Drip cannot renew absolute deadline.
						w.(http.Flusher).Flush()
					}
				}
			}))
			defer server.Close()
			model, err := New(Config{BaseURL: server.URL, AllowInsecureLoopback: true, ModelID: "fixture-model", APIKey: "fixture-only", ContextWindow: 8192, MaxOutput: 1024})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				stream, err := model.Stream(ctx, engine.ModelRequest{Input: "synthetic"})
				if stream != nil {
					_ = stream.Close()
				}
				result <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("server not entered")
			}
			if cancelCaller {
				cancel()
			}
			select {
			case err := <-result:
				var failure *engine.ProviderFailure
				want := "provider_permanent"
				if cancelCaller {
					want = "provider_canceled"
				}
				if !errors.As(err, &failure) || failure.Code != want {
					t.Fatalf("incomplete error body classified: %v", err)
				}
			case <-time.After(overflowReadTimeout + 2*time.Second):
				t.Fatal("error body read exceeded absolute deadline")
			}
			select {
			case <-released:
			case <-time.After(2 * time.Second):
				t.Fatal("HTTP body/request not closed")
			}
		})
	}
}
