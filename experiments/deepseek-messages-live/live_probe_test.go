//go:build liveprobe

package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// Explicit opt-in only. No real credentials, raw thinking, provider body or
// request headers are written to test logs. The persistent pre-send ledger
// counts failed/unknown requests too, across sequential diagnostic reruns.
func TestLiveDeepSeekMessages(t *testing.T) {
	if os.Getenv("OCH_MESSAGES_LIVE_CONFIRM") != "I_UNDERSTAND" {
		t.Skip("explicit live consent required")
	}
	root := os.Getenv("OCH_MESSAGES_LIVE_ARTIFACTS")
	if !filepath.IsAbs(root) || !strings.HasPrefix(root, "/tmp/och-deepseek-live.") {
		t.Fatal("dedicated artifact directory required")
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0700 || filepath.Dir(root) != "/tmp" {
		t.Fatal("private artifact root required")
	}
	keyInfo, err := os.Lstat("/tmp/och-deepseek-key")
	if err != nil || !keyInfo.Mode().IsRegular() || keyInfo.Mode().Perm() != 0600 || keyInfo.Size() < 1 || keyInfo.Size() > 4096 {
		t.Fatal("private credential file unavailable")
	}
	key, err := os.ReadFile("/tmp/och-deepseek-key")
	if err != nil {
		t.Fatal("cannot load credential")
	}
	defer clear(key)
	transport := &liveBoundedTransport{base: http.DefaultTransport.(*http.Transport).Clone(), ledger: filepath.Join(root, "requests.jsonl"), t: t}
	model, err := New(Config{BaseURL: "https://api.deepseek.com/anthropic", ModelID: "deepseek-flash", APIKey: strings.TrimSpace(string(key)), ContextWindow: 32768, MaxOutput: 4096, ReasoningEffort: engine.ReasoningEffortLow, HTTPClient: &http.Client{Transport: transport, Timeout: 120 * time.Second}})
	if err != nil {
		t.Fatal("model construction failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	request := engine.ModelRequest{Input: "This is a synthetic protocol conformance test. Call probe_echo exactly once with nonce och-messages-20260914. Do not answer before receiving its result.", Tools: []domain.ToolSchema{{Name: "probe_echo", Description: "Returns a synthetic nonce for protocol validation. Call this once.", InputSchema: json.RawMessage(`{"type":"object","properties":{"nonce":{"type":"string"}},"required":["nonce"],"additionalProperties":false}`)}}}
	first := liveCompletion(t, ctx, model, request, transport)
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].Name != "probe_echo" {
		t.Fatal("model did not make the requested single tool call")
	}
	var args struct {
		Nonce string `json:"nonce"`
	}
	if json.Unmarshal([]byte(first.ToolCalls[0].Arguments), &args) != nil || args.Nonce != "och-messages-20260914" {
		t.Fatal("synthetic tool arguments changed")
	}
	request.Messages = []domain.ModelPromptMessage{{Role: "user", Text: request.Input}, {Role: "assistant", Text: first.Text, ToolCalls: first.ToolCalls, ProviderState: first.ProviderState}, {Role: "tool", Name: "probe_echo", ToolCallID: first.ToolCalls[0].ID, Text: "Synthetic result: nonce verified; reply briefly with PROBE_OK and do not call more tools."}}
	second := liveCompletion(t, ctx, model, request, transport)
	if len(second.ToolCalls) != 0 || !strings.Contains(second.Text, "PROBE_OK") {
		t.Fatal("tool continuation did not finish as requested")
	}
	t.Log("LIVE_TOOL_CONTINUATION=passed")
}

type liveResult struct {
	Text          string
	ToolCalls     []domain.ToolCallOffer
	ProviderState *domain.ProviderState
}

// Offline-only: examine private captured framing without printing content.
func TestInspectCapturedMessages(t *testing.T) {
	path := os.Getenv("OCH_MESSAGES_CAPTURE")
	if path == "" {
		t.Skip("no capture selected")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal("capture unavailable")
	}
	defer f.Close()
	wire, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil || len(wire) > 1<<20 {
		t.Fatal("capture bound")
	}
	tr := &liveBoundedTransport{t: t}
	tr.wire.Write(wire)
	tr.diagnose()
	result, err := decodeMessageFor(bytes.NewReader(wire), deepSeekMessages)
	if err != nil {
		t.Fatal("captured decoder rejected")
	}
	t.Logf("usage uncached=%d read=%d creation=%d output=%d model_matches=%t", result.Usage.InputTokens, result.Usage.CacheReadTokens, result.Usage.CacheCreationTokens, result.Usage.OutputTokens, result.Model == "deepseek-flash")
}

func liveCompletion(t *testing.T, ctx context.Context, m *Model, request engine.ModelRequest, transport *liveBoundedTransport) liveResult {
	t.Helper()
	s, err := m.Stream(ctx, request)
	if err != nil {
		var pf *engine.ProviderFailure
		if errors.As(err, &pf) {
			t.Logf("safe_failure class=%s status=%d", pf.Class, pf.HTTPStatus)
		}
		t.Fatal("live request startup rejected")
	}
	defer s.Close()
	var out liveResult
	for {
		event, err := s.Next(ctx)
		if err != nil {
			transport.diagnose()
			t.Fatal("live stream rejected before completion")
		}
		switch event.Type {
		case engine.StreamEventTextDelta:
			out.Text += event.Text
		case engine.StreamEventToolCall:
			out.ToolCalls = append(out.ToolCalls, domain.ToolCallOffer{ID: event.ToolCall.ID, Name: event.ToolCall.Name, Arguments: event.ToolCall.Arguments})
		case engine.StreamEventCompleted:
			out.ProviderState = event.ProviderState
			if domain.ValidateProviderProjection(out.ProviderState, out.Text, out.ToolCalls) != nil {
				t.Fatal("live result projection invalid")
			}
			kinds := []string{}
			signatures := 0
			for _, b := range out.ProviderState.MessagesContent {
				kinds = append(kinds, b.Type)
				if b.Signature != nil {
					signatures++
				}
			}
			t.Logf("completed blocks=%v signatures=%d input=%d output=%d cached=%d", kinds, signatures, event.Usage.InputTokens, event.Usage.OutputTokens, event.Usage.CachedInputTokens)
			transport.recordUsage(event.Usage)
			return out
		}
	}
}

type liveBoundedTransport struct {
	base   http.RoundTripper
	ledger string
	t      *testing.T
	mu     sync.Mutex
	wire   bytes.Buffer
}

func (tr *liveBoundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != "api.deepseek.com" || r.URL.Path != "/anthropic/v1/messages" || r.Method != "POST" {
		return nil, errors.New("live route cap")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (32<<10)+1))
	r.Body.Close()
	if err != nil || len(body) > 32<<10 {
		return nil, errors.New("live request byte cap")
	}
	var fields struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Stream    bool   `json:"stream"`
	}
	if json.Unmarshal(body, &fields) != nil || fields.Model != "deepseek-flash" || fields.MaxTokens < 1 || fields.MaxTokens > 4096 || !fields.Stream {
		return nil, errors.New("live model/output cap")
	}
	f, err := os.OpenFile(tr.ledger, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, errors.New("live ledger unavailable")
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX) != nil {
		return nil, errors.New("live ledger lock failed")
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	prior, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil || len(prior) >= 64<<10 {
		return nil, errors.New("live ledger invalid")
	}
	count := bytes.Count(prior, []byte("\n"))
	if count >= 12 {
		return nil, errors.New("live total request cap reached")
	}
	entry, _ := json.Marshal(map[string]any{"attempt": count + 1, "requestBytes": len(body), "maxOutputTokens": fields.MaxTokens, "at": time.Now().UTC()})
	if _, err = f.Write(append(entry, '\n')); err != nil || f.Sync() != nil {
		return nil, errors.New("live reservation failed")
	}
	tr.t.Logf("reserved_request=%d bytes=%d max_output=%d", count+1, len(body), fields.MaxTokens)
	r.Body = io.NopCloser(bytes.NewReader(body))
	tr.mu.Lock()
	tr.wire.Reset()
	tr.mu.Unlock()
	response, err := tr.base.RoundTrip(r)
	if err == nil {
		tr.t.Logf("http_status=%d", response.StatusCode)
		response.Body = &liveCaptureBody{ReadCloser: response.Body, transport: tr}
	}
	return response, err
}

type liveCaptureBody struct {
	io.ReadCloser
	transport *liveBoundedTransport
}

func (b *liveCaptureBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.transport.mu.Lock()
	if b.transport.wire.Len() < 1<<20 {
		b.transport.wire.Write(p[:min(n, (1<<20)-b.transport.wire.Len())])
	}
	b.transport.mu.Unlock()
	return n, err
}

// Structural diagnostics only; even synthetic thinking stays out of logs.
func (tr *liveBoundedTransport) diagnose() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	scanner := bufio.NewScanner(bytes.NewReader(tr.wire.Bytes()))
	scanner.Buffer(make([]byte, 4096), maxSSELineBytes)
	state := accumulator{profile: deepSeekMessages}
	name := ""
	var data strings.Builder
	events := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			name = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			data.WriteString(strings.TrimPrefix(line, "data: "))
			data.WriteByte('\n')
		}
		if line == "" && data.Len() > 0 {
			events++
			payload := []byte(data.String())
			if name == "message_delta" || name == "message_start" {
				var meta struct {
					Delta struct {
						StopReason string `json:"stop_reason"`
					} `json:"delta"`
					Usage struct {
						Input    uint64 `json:"input_tokens"`
						Output   uint64 `json:"output_tokens"`
						Read     uint64 `json:"cache_read_input_tokens"`
						Creation uint64 `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				}
				if json.Unmarshal(payload, &meta) == nil && name == "message_delta" {
					t := tr.t
					t.Logf("terminal reason=%s input=%d output=%d read=%d creation=%d", safeLiveName(meta.Delta.StopReason), meta.Usage.Input, meta.Usage.Output, meta.Usage.Read, meta.Usage.Creation)
				}
			}
			if err := state.accept(name, payload); err != nil {
				tr.t.Logf("rejected_event_number=%d event=%q shape=%s", events, safeLiveName(name), liveShape(payload))
				return
			}
			name = ""
			data.Reset()
		}
	}
	tr.t.Logf("parsed_events=%d complete=%t blocks=%d wire_bytes=%d", events, state.complete, len(state.blocks), tr.wire.Len())
}
func safeLiveName(s string) string {
	if len(s) > 64 {
		return "other"
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c == '_') {
			return "other"
		}
	}
	return s
}
func liveShape(raw []byte) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return "invalid-json"
	}
	parts := []string{}
	for k, v := range m {
		kind := "scalar"
		if len(v) > 0 && v[0] == '{' {
			kind = liveShape(v)
		}
		parts = append(parts, fmt.Sprintf("%s:%s", safeLiveName(k), kind))
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ",") + "}"
}
func (tr *liveBoundedTransport) recordUsage(u *engine.TokenUsage) {
	f, err := os.OpenFile(filepath.Join(filepath.Dir(tr.ledger), "usage.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		tr.t.Fatal("cannot record usage")
	}
	defer f.Close()
	if json.NewEncoder(f).Encode(u) != nil {
		tr.t.Fatal("cannot encode usage")
	}
}
