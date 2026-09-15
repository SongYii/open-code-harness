package anthropic

import (
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// Check status and content type BEFORE the SDK reads an unbounded error body or
// treats a redirect as a successful empty stream. Only 400 JSON receives the
// bounded overflow check; no error body is handed to the SDK or retained.
type httpBoundary struct{ client *http.Client }
type statusError struct {
	status          int
	contextOverflow bool
}

func (*statusError) Error() string { return "anthropic: rejected HTTP response" }

func newHTTPBoundary(source *http.Client) *httpBoundary {
	client := &http.Client{}
	if source != nil {
		*client = *source
	}
	if client.Transport == nil {
		client.Transport = &http.Transport{
			Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second, MaxResponseHeaderBytes: 64 << 10,
		}
	} else if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
		cloned := transport.Clone()
		cloned.ResponseHeaderTimeout = 30 * time.Second
		cloned.MaxResponseHeaderBytes = 64 << 10
		client.Transport = cloned
	}
	client.Jar = nil // No ambient session cookies on provider requests.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &httpBoundary{client: client}
}

func (b *httpBoundary) Do(req *http.Request) (*http.Response, error) {
	response, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	typ, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || parseErr != nil || typ != "text/event-stream" {
		overflow := false
		if response.StatusCode == http.StatusBadRequest && parseErr == nil && typ == "application/json" {
			overflow = readContextOverflow(req.Context(), response.Body)
		} else {
			_ = response.Body.Close()
		}
		return nil, &statusError{status: response.StatusCode, contextOverflow: overflow}
	}
	return response, nil
}

func failure(code engine.ErrorCode, status int) error {
	f := &engine.ProviderFailure{Class: engine.FailureClassPermanent, Code: "provider_permanent", HTTPStatus: status, SafeMessage: "DeepSeek Messages request failed"}
	switch {
	case status == 401 || status == 403:
		f.Class, f.Code = engine.FailureClassAuth, "provider_auth"
	case status == 429:
		f.Class, f.Code, f.Retryable = engine.FailureClassRateLimit, "provider_rate_limit", true
	case status >= 500 || status == 408 || status == 0 && code == engine.CodeModelStartup:
		f.Class, f.Code, f.Retryable = engine.FailureClassTransient, "provider_transient", true
	}
	return &engine.Error{Code: code, Cause: f}
}

func canceled() error {
	return &engine.Error{Code: engine.CodeCanceled, Cause: &engine.ProviderFailure{Class: engine.FailureClassCanceled, Code: "provider_canceled", SafeMessage: "request canceled"}}
}
