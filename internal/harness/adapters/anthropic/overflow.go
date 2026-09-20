package anthropic

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"strconv"
	"time"
)

const maxOverflowBodyBytes = 4 << 10
const overflowReadTimeout = 2 * time.Second

// Error bodies are untrusted and may contain echoed prompts/credentials. Admit
// only a complete, small response before an absolute deadline; never truncate
// into a match, retain vendor text, or let SDK error decoding read the body.
// As with streaming, injected transports must unblock Read when Close is called.
func readContextOverflow(ctx context.Context, source io.ReadCloser) bool {
	body := &guardedBody{body: source} // Reuse once-only Close, not the idle reader.
	stopCancel := context.AfterFunc(ctx, func() { _ = body.Close() })
	expired := make(chan struct{})
	timer := time.AfterFunc(overflowReadTimeout, func() {
		_ = body.Close()
		close(expired)
	})
	data, readErr := io.ReadAll(io.LimitReader(source, maxOverflowBodyBytes+1))
	closeErr := body.Close()
	timedOut := !timer.Stop()
	if timedOut {
		<-expired
	}
	stopCancel()
	if ctx.Err() != nil || timedOut || readErr != nil || closeErr != nil || len(data) > maxOverflowBodyBytes {
		return false
	}
	return isContextOverflowBody(data)
}

// The numeric DeepSeek shape is a reported Messages response, not a promised
// vendor error code. Sources and deliberate exclusions are recorded in the
// provider replay contract. Full-string matching avoids classifying quoted
// prompt fragments, generic max_tokens errors, or gateway prose as overflow.
var deepSeekOverflowMessage = regexp.MustCompile(`^This model's maximum context length is ([0-9]+) tokens\. However, you requested ([0-9]+) tokens \(([0-9]+) in the messages, ([0-9]+) in the completion\)\. Please reduce the length of the messages or completion\.$`)
var messagesOverflowMessage = regexp.MustCompile(`^prompt is too long: ([0-9]+) tokens > ([0-9]+) maximum$`)

func isContextOverflowBody(data []byte) bool {
	if len(data) > maxOverflowBodyBytes {
		return false
	}
	root, err := object(data, "type", "error", "request_id")
	if err != nil {
		return false
	}
	if _, exists := root["type"]; exists {
		if value, err := stringField(root, "type"); err != nil || value != "error" {
			return false
		}
	}
	if _, exists := root["request_id"]; exists {
		if _, err := stringField(root, "request_id"); err != nil {
			return false
		}
	}
	fields, err := object(root["error"], "type", "message", "code", "param")
	if err != nil {
		return false
	}
	typ, err := stringField(fields, "type")
	if err != nil || typ != "invalid_request_error" {
		return false
	}
	if code, exists := fields["code"]; exists && !bytes.Equal(bytes.TrimSpace(code), []byte("null")) {
		if value, err := stringField(fields, "code"); err != nil || value != "invalid_request_error" {
			return false
		}
	}
	if param, exists := fields["param"]; exists && !bytes.Equal(bytes.TrimSpace(param), []byte("null")) {
		return false
	}
	message, err := stringField(fields, "message")
	if err != nil {
		return false
	}
	if message == "prompt is too long" {
		return true
	}
	if parts := messagesOverflowMessage.FindStringSubmatch(message); len(parts) != 0 {
		requested, ok := positiveTokenCount(parts[1])
		limit, limitOK := positiveTokenCount(parts[2])
		return ok && limitOK && requested > limit
	}
	parts := deepSeekOverflowMessage.FindStringSubmatch(message)
	if len(parts) == 0 {
		return false
	}
	limit, limitOK := positiveTokenCount(parts[1])
	requested, requestedOK := positiveTokenCount(parts[2])
	input, inputOK := positiveTokenCount(parts[3])
	output, outputOK := positiveTokenCount(parts[4])
	// Subtraction after requested > input cannot wrap. Output alone at/above
	// capacity cannot be fixed by compacting history, so do not trigger it.
	return limitOK && requestedOK && inputOK && outputOK && requested > limit && requested > input && requested-input == output && output < limit
}

func positiveTokenCount(value string) (uint64, bool) {
	n, err := strconv.ParseUint(value, 10, 64)
	return n, err == nil && n > 0
}
