package openaicompat

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

func TestClassifyHTTPErrors(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		retry     string
		durable   string
		class     engine.FailureClass
		retryable bool
		after     time.Duration
	}{
		{name: "401", status: 401, body: `{"error":{"message":"Invalid Authorization: Bearer sk-secret"}}`, durable: "provider_auth", class: engine.FailureClassAuth},
		{name: "403", status: 403, body: `{"error":{"message":"forbidden"}}`, durable: "provider_auth", class: engine.FailureClassAuth},
		{name: "429 rate limit", status: 429, body: `{"error":{"message":"slow down","type":"rate_limit_exceeded"}}`, retry: "2", durable: "provider_rate_limit", class: engine.FailureClassRateLimit, retryable: true, after: 2 * time.Second},
		{name: "429 quota substring", status: 429, body: `{"error":{"message":"You exceeded your current quota"}}`, durable: "provider_quota", class: engine.FailureClassQuota},
		{name: "429 quota code", status: 429, body: `{"error":{"code":"insufficient_quota","message":"pay"}}`, durable: "provider_quota", class: engine.FailureClassQuota},
		{name: "400 overflow", status: 400, body: `{"error":{"code":"context_length_exceeded","message":"too long"}}`, durable: "context_overflow", class: engine.FailureClassPermanent},
		{name: "413 overflow substring", status: 413, body: `{"error":{"message":"prompt is too long"}}`, durable: "context_overflow", class: engine.FailureClassPermanent},
		{name: "400 permanent", status: 400, body: `{"error":{"message":"bad request"}}`, durable: "provider_permanent", class: engine.FailureClassPermanent},
		{name: "302 redirect", status: 302, body: `<html><a href="/">moved</a></html>`, durable: "provider_permanent", class: engine.FailureClassPermanent},
		{name: "500 transient", status: 500, body: `{"error":{"message":"oops"}}`, durable: "provider_transient", class: engine.FailureClassTransient, retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header := make(http.Header)
			if test.retry != "" {
				header.Set("Retry-After", test.retry)
			}
			header.Set("x-ds-request-id", "ds-1")
			transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: test.status,
					Header:     header,
					Body:       io.NopCloser(strings.NewReader(test.body)),
				}, nil
			}}
			model := newTestModel(t, validConfig(transport))
			_, err := model.Stream(context.Background(), modelRequest())
			failure := requireProviderFailure(t, err, engine.CodeModelStartup, test.durable)
			if failure.Class != test.class || failure.Retryable != test.retryable || failure.HTTPStatus != test.status {
				t.Fatalf("failure = %#v", failure)
			}
			if test.after != 0 && failure.RetryAfter != test.after {
				t.Fatalf("RetryAfter = %v, want %v", failure.RetryAfter, test.after)
			}
			if failure.RequestID != "ds-1" {
				t.Fatalf("RequestID = %q", failure.RequestID)
			}
		})
	}
}

func TestCheckRedirectThreeXXIsPermanent(t *testing.T) {
	transport := &scriptedTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://evil.example/steal"}},
			Body:       io.NopCloser(strings.NewReader("moved")),
			Request:    req,
		}, nil
	}}
	model := newTestModel(t, validConfig(transport))
	_, err := model.Stream(context.Background(), modelRequest())
	failure := requireProviderFailure(t, err, engine.CodeModelStartup, "provider_permanent")
	if failure.Class == engine.FailureClassTransient || failure.Retryable {
		t.Fatalf("3xx classified as transient: %#v", failure)
	}
	if transport.requests.Load() != 1 {
		t.Fatalf("followed redirect: requests=%d", transport.requests.Load())
	}
}

func TestRetryAfterRFC1123AndCap(t *testing.T) {
	when := time.Now().UTC().Add(90 * time.Minute).Format(time.RFC1123)
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Retry-After": []string{when}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"later"}}`)),
		}, nil
	}}
	model := newTestModel(t, validConfig(transport))
	_, err := model.Stream(context.Background(), modelRequest())
	failure := requireProviderFailure(t, err, engine.CodeModelStartup, "provider_rate_limit")
	if failure.RetryAfter != time.Hour {
		t.Fatalf("RetryAfter = %v, want 1h cap", failure.RetryAfter)
	}
}

func TestRedactionOnClassifiedMessages(t *testing.T) {
	body := `{"error":{"message":"Invalid Authorization: Bearer sk-secret key=sk-also"}}`
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}}
	model := newTestModel(t, validConfig(transport))
	_, err := model.Stream(context.Background(), modelRequest())
	failure := requireProviderFailure(t, err, engine.CodeModelStartup, "provider_auth")
	// The header/parameter labels ("Authorization", "key") are expected to
	// survive: internal/harness/redact.Text preserves a matched prefix and
	// replaces only the secret value with a [redacted] marker, so a reader
	// can tell "a secret was here and removed" apart from "this field was
	// legitimately empty" (secret redaction design §5). Only the actual
	// secret values must be gone.
	for _, leaked := range []string{"sk-secret", "sk-also"} {
		if strings.Contains(failure.SafeMessage, leaked) {
			t.Fatalf("SafeMessage leaked secret value %q: %q", leaked, failure.SafeMessage)
		}
	}
	if !strings.Contains(failure.SafeMessage, "[redacted]") {
		t.Fatalf("SafeMessage = %q, want a [redacted] marker where the secret value was", failure.SafeMessage)
	}
}

func TestHTMLErrorBodyUsesStatusFallback(t *testing.T) {
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader("<html><body>cloudflare</body></html>")),
		}, nil
	}}
	model := newTestModel(t, validConfig(transport))
	_, err := model.Stream(context.Background(), modelRequest())
	failure := requireProviderFailure(t, err, engine.CodeModelStartup, "provider_transient")
	if failure.SafeMessage != "http_502" {
		t.Fatalf("SafeMessage = %q, want http_502", failure.SafeMessage)
	}
}

func TestDialFailureIsTransientStartup(t *testing.T) {
	transport := &scriptedTransport{roundTrip: func(*http.Request) (*http.Response, error) {
		return nil, io.EOF
	}}
	model := newTestModel(t, validConfig(transport))
	_, err := model.Stream(context.Background(), modelRequest())
	requireProviderFailure(t, err, engine.CodeModelStartup, "provider_transient")
	if errors.Is(err, io.EOF) {
		t.Fatalf("startup dial error unwraps to io.EOF: %v", err)
	}
}
