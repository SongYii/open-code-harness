package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

const (
	maxErrorBodyBytes    = 4 << 10
	maxSafeMessageRunes  = 256
	maxProviderRequestID = 128
	maxRetryAfter        = time.Hour
)

var (
	reAuthorization = regexp.MustCompile(`(?i)Authorization\s*[:=]\s*\S+`)
	reBearer        = regexp.MustCompile(`(?i)Bearer\s+\S+`)
	reSecretKey     = regexp.MustCompile(`sk-[A-Za-z0-9_\-]+`)
	reQueryKey      = regexp.MustCompile(`(?i)([?&]key=)[^&\s]+`)
)

var (
	quotaSubstrings = []string{
		"insufficient_quota",
		"quota_exceeded",
		"quota exhausted",
		"billing_not_active",
		"you exceeded your current quota",
		"exceed your quota",
	}
	quotaCodes = []string{
		"insufficient_quota",
		"quota_exceeded",
		"billing_not_active",
	}
	overflowSubstrings = []string{
		"context_length_exceeded",
		"context length",
		"maximum context",
		"max context length",
		"context window",
		"too many tokens",
		"prompt is too long",
		"token limit",
	}
	overflowCodes = []string{
		"context_length_exceeded",
		"max_tokens",
		"context_window_exceeded",
	}
)

func classifyDoError(ctx context.Context, err error) *engine.Error {
	if ctx != nil && ctx.Err() != nil {
		return canceledError(ctx.Err())
	}
	return startupFailure(engine.FailureClassTransient, "provider_transient", 0, "", "provider temporarily unavailable")
}

func classifyResponse(ctx context.Context, resp *http.Response, requestID string) *engine.Error {
	if ctx != nil && ctx.Err() != nil {
		return canceledError(ctx.Err())
	}
	if resp == nil {
		return startupFailure(engine.FailureClassTransient, "provider_transient", 0, requestID, "provider temporarily unavailable")
	}
	if resp.StatusCode == http.StatusOK {
		if !isEventStream(resp.Header.Get("Content-Type")) {
			return classifyNonStreamSuccess(resp, requestID)
		}
		return nil
	}
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return classifyNonStreamSuccess(resp, requestID)
	}
	return classifyStatus(resp, requestID)
}

func classifyNonStreamSuccess(resp *http.Response, requestID string) *engine.Error {
	_ = readErrorBody(resp.Body)
	return startupFailure(engine.FailureClassPermanent, "provider_permanent", resp.StatusCode, requestID, "provider rejected the request")
}

func classifyStatus(resp *http.Response, requestID string) *engine.Error {
	status := resp.StatusCode
	body := readErrorBody(resp.Body)
	failure := statusFailure(status)
	failure.HTTPStatus = status
	failure.RequestID = requestID
	failure.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	if status == http.StatusTooManyRequests && matchesQuota(body) {
		failure.Class = engine.FailureClassQuota
		failure.Retryable = false
		failure.Code = "provider_quota"
	} else if overflowStatus(status) && matchesOverflow(body) {
		failure.Class = engine.FailureClassPermanent
		failure.Retryable = false
		failure.Code = "context_overflow"
	}
	failure.SafeMessage = safeMessage(status, body)
	return &engine.Error{Code: engine.CodeModelStartup, Cause: failure}
}

func statusFailure(status int) *engine.ProviderFailure {
	switch {
	case status >= 300 && status <= 399:
		return &engine.ProviderFailure{Class: engine.FailureClassPermanent, Code: "provider_permanent"}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &engine.ProviderFailure{Class: engine.FailureClassAuth, Code: "provider_auth"}
	case status == http.StatusTooManyRequests:
		return &engine.ProviderFailure{Class: engine.FailureClassRateLimit, Retryable: true, Code: "provider_rate_limit"}
	case status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusInternalServerError || status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout || status == 529:
		return &engine.ProviderFailure{Class: engine.FailureClassTransient, Retryable: true, Code: "provider_transient"}
	case status == http.StatusBadRequest || status == http.StatusNotFound || status == http.StatusRequestEntityTooLarge || status == http.StatusUnprocessableEntity:
		return &engine.ProviderFailure{Class: engine.FailureClassPermanent, Code: "provider_permanent"}
	case status >= 400 && status <= 499:
		return &engine.ProviderFailure{Class: engine.FailureClassPermanent, Code: "provider_permanent"}
	case status >= 500 && status <= 599:
		return &engine.ProviderFailure{Class: engine.FailureClassTransient, Retryable: true, Code: "provider_transient"}
	default:
		return &engine.ProviderFailure{Class: engine.FailureClassPermanent, Code: "provider_permanent"}
	}
}

func overflowStatus(status int) bool {
	return status == http.StatusBadRequest || status == http.StatusRequestEntityTooLarge || status == http.StatusUnprocessableEntity
}

func matchesQuota(body []byte) bool {
	return matchClosedTokens(body, quotaSubstrings, quotaCodes)
}

func matchesOverflow(body []byte) bool {
	return matchClosedTokens(body, overflowSubstrings, overflowCodes)
}

func matchClosedTokens(body []byte, substrings, codes []string) bool {
	lower := strings.ToLower(string(body))
	for _, token := range substrings {
		if strings.Contains(lower, token) {
			return true
		}
	}
	code, typ := vendorErrorTokens(body)
	for _, wanted := range codes {
		if code == wanted || typ == wanted {
			return true
		}
	}
	return false
}

func vendorErrorTokens(body []byte) (code, typ string) {
	var parsed struct {
		Error struct {
			Code any    `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &parsed) != nil {
		return "", ""
	}
	if text, ok := parsed.Error.Code.(string); ok {
		code = strings.ToLower(text)
	}
	return code, strings.ToLower(parsed.Error.Type)
}

func readErrorBody(body io.Reader) []byte {
	if body == nil {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes+1))
	if len(data) > maxErrorBodyBytes {
		return data[:maxErrorBodyBytes]
	}
	return data
}

func isEventStream(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return strings.EqualFold(media, "text/event-stream")
}

func providerRequestID(header http.Header) string {
	if header == nil {
		return ""
	}
	for _, name := range []string{"x-request-id", "x-ds-request-id", "openai-request-id"} {
		value := strings.TrimSpace(header.Get(name))
		if value == "" {
			continue
		}
		if len(value) > maxProviderRequestID {
			value = value[:maxProviderRequestID]
		}
		return value
	}
	return ""
}

func parseRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return clampRetryAfter(time.Duration(seconds) * time.Second)
	}
	when, err := time.Parse(time.RFC1123, raw)
	if err != nil {
		return 0
	}
	return clampRetryAfter(time.Until(when))
}

func clampRetryAfter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	if d > maxRetryAfter {
		return maxRetryAfter
	}
	return d
}

func safeMessage(status int, body []byte) string {
	if looksLikeHTML(body) {
		return truncateRunes("http_" + strconv.Itoa(status))
	}
	text := redactSecrets(string(bytes.TrimSpace(body)))
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return truncateRunes("http_" + strconv.Itoa(status))
	}
	return truncateRunes(text)
}

func looksLikeHTML(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	lower := bytes.ToLower(trimmed)
	limit := lower
	if len(limit) > 256 {
		limit = limit[:256]
	}
	return bytes.HasPrefix(limit, []byte("<!doctype")) || bytes.HasPrefix(limit, []byte("<html")) || bytes.Contains(limit, []byte("<html"))
}

func redactSecrets(text string) string {
	text = reAuthorization.ReplaceAllString(text, "")
	text = reBearer.ReplaceAllString(text, "")
	text = reSecretKey.ReplaceAllString(text, "")
	text = reQueryKey.ReplaceAllString(text, "$1")
	return text
}

func truncateRunes(text string) string {
	if utf8.RuneCountInString(text) <= maxSafeMessageRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxSafeMessageRunes])
}

func canceledError(cause error) *engine.Error {
	if cause == nil {
		cause = context.Canceled
	}
	return &engine.Error{Code: engine.CodeCanceled, Cause: cause}
}

func startupFailure(class engine.FailureClass, code string, status int, requestID, message string) *engine.Error {
	return &engine.Error{
		Code: engine.CodeModelStartup,
		Cause: &engine.ProviderFailure{
			Class:       class,
			Retryable:   class == engine.FailureClassTransient || class == engine.FailureClassRateLimit,
			Code:        code,
			HTTPStatus:  status,
			RequestID:   requestID,
			SafeMessage: redactSecrets(message),
		},
	}
}

func capabilityMismatch(requestID string) *engine.Error {
	return streamFailure(engine.CodeInvalidStream, engine.FailureClassPermanent, "capability_mismatch", http.StatusOK, requestID, "provider returned an unsupported capability")
}

func streamFailure(engineCode engine.ErrorCode, class engine.FailureClass, code string, status int, requestID, message string) *engine.Error {
	return &engine.Error{
		Code: engineCode,
		Cause: &engine.ProviderFailure{
			Class:       class,
			Retryable:   class == engine.FailureClassTransient || class == engine.FailureClassRateLimit,
			Code:        code,
			HTTPStatus:  status,
			RequestID:   requestID,
			SafeMessage: redactSecrets(message),
		},
	}
}

func isCanceled(err error) bool {
	return err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
}
