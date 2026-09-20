//go:build livecomposition

package composition

import (
	"bytes"
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
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

// Set only by this test. An overlay adds HTTPClient: messagesLiveHTTPClient to
// the real assembly's existing anthropic.Config literal; no other behavior is
// changed and no production injection API is introduced for this experiment.
var messagesLiveHTTPClient *http.Client

func TestLiveMessagesComposition(t *testing.T) {
	runMessagesComposition(t, nil)
}

// A fixture transport is supplied only by the local rehearsal test. There is
// no environment flag that can silently substitute fixtures for live evidence.
func runMessagesComposition(t *testing.T, fixture http.RoundTripper) {
	offline := os.Getenv("OCH_MESSAGES_LIVE_INSPECT") == "1" || os.Getenv("OCH_MESSAGES_LIVE_VERIFY") == "1"
	if fixture == nil && !offline && os.Getenv("OCH_MESSAGES_LIVE_CONFIRM") != "I_UNDERSTAND" {
		t.Skip("explicit live consent required")
	}
	root := os.Getenv("OCH_MESSAGES_LIVE_ARTIFACTS")
	if !filepath.IsAbs(root) || !strings.HasPrefix(root, "/tmp/och-deepseek-live.") {
		t.Fatal("dedicated artifact directory required")
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || filepath.Dir(root) != "/tmp" {
		t.Fatal("private artifact root required")
	}
	key := []byte("offline-placeholder-not-a-key")
	if !offline && fixture == nil {
		info, err := os.Lstat("/tmp/och-deepseek-key")
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() < 1 || info.Size() > 4096 {
			t.Fatal("private credential unavailable")
		}
		key, err = os.ReadFile("/tmp/och-deepseek-key")
		if err != nil {
			t.Fatal("cannot load credential")
		}
	}
	defer clear(key)
	t.Setenv("OCH_MESSAGES_PROBE_KEY", strings.TrimSpace(string(key)))
	transport := &compositionLiveTransport{t: t, root: root, base: http.DefaultTransport.(*http.Transport).Clone(), offline: offline}
	if fixture != nil {
		transport.base = fixture
	}
	messagesLiveHTTPClient = &http.Client{Transport: transport, Timeout: 120 * time.Second}
	defer func() { messagesLiveHTTPClient = nil }()
	workspace := os.Getenv("OCH_MESSAGES_LIVE_RESUME_WORKSPACE")
	resume := workspace != ""
	if offline && !resume {
		t.Fatal("offline inspection requires existing workspace")
	}
	if resume {
		if filepath.Dir(workspace) != root || !strings.HasPrefix(filepath.Base(workspace), "workspace-") {
			t.Fatal("resume workspace outside dedicated root")
		}
	} else {
		workspace, err = os.MkdirTemp(root, "workspace-")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, "nonce.txt"), []byte("SYNTHETIC_NONCE_BLUE_731\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	config := Config{WorkspaceRoot: workspace, DatabasePath: filepath.Join(workspace, "..", filepath.Base(workspace)+".db"), RuntimeID: "messages-live-probe", Provider: Provider{AdapterKind: "deepseek-messages", BaseURL: "https://api.deepseek.com/anthropic", ModelID: "deepseek-flash", APIKeyEnv: "OCH_MESSAGES_PROBE_KEY", ContextWindow: 24576, MaxOutput: 4096, ReasoningEffort: "low"}, Policy: policy.ModeReadOnly, Limits: Limits{MaxSteps: 2, MaxToolCallsPerStep: 1, MaxAssistantBytes: 8192}, Context: Context{MaxSummaryChunks: 1, MaxOverflowCompactionsPerTurn: 1, TailPercent: 10}, AllowUnsandboxedExec: true}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	assembly, err := Open(ctx, config)
	if err != nil {
		t.Fatal("composition startup failed")
	}
	defer func() {
		if assembly != nil {
			assembly.Close()
		}
	}()
	var sessionID domain.SessionID
	if resume {
		listed, err := assembly.Service().ListSessions(ctx, application.ListSessionsRequest{WorkspaceRoot: workspace})
		if err != nil || len(listed.Sessions) != 1 {
			t.Fatal("resume requires one existing synthetic session")
		}
		sessionID = listed.Sessions[0].SessionID
	} else {
		created, err := assembly.Service().CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: workspace})
		if err != nil {
			t.Fatal("session creation failed")
		}
		sessionID = created.SessionID
	}
	if os.Getenv("OCH_MESSAGES_LIVE_INSPECT") == "1" {
		records, err := application.ReadWholeStreamPinned(ctx, assembly.Store(), sessionID, 256)
		if err != nil {
			t.Fatal("inspection failed")
		}
		for _, record := range records {
			switch e := record.Event.(type) {
			case domain.ContextCompactionFailed:
				t.Logf("compaction_failed code=%s reason=%s", e.Code, e.Message)
			case domain.ModelUsageRecorded:
				t.Logf("usage input=%d output=%d cache=%d finish=%s", e.InputTokens, e.OutputTokens, e.CachedInputTokens, e.FinishReason)
			case domain.ContextCompactionCompleted:
				t.Log("compaction_completed")
			}
		}
		return
	}
	if os.Getenv("OCH_MESSAGES_LIVE_VERIFY") == "1" {
		records, err := application.ReadWholeStreamPinned(ctx, assembly.Store(), sessionID, 256)
		if err != nil {
			t.Fatal("read canonical history failed")
		}
		var last domain.AssistantMessageCompleted
		var checkpoint uint64
		states, toolCalls, replayed := 0, 0, 0
		var usages []domain.ModelUsageRecorded
		known := map[string]bool{}
		for _, record := range records {
			switch e := record.Event.(type) {
			case domain.AssistantMessageCompleted:
				if e.ProviderState == nil || e.ProviderState.Protocol != domain.DeepSeekMessagesV1 {
					t.Fatal("native state missing")
				}
				encoded, _ := json.Marshal(e.ProviderState)
				known[string(encoded)] = true
				last = e
				states++
			case domain.ContextCompactionCompleted:
				checkpoint = record.Sequence
			case domain.ToolCallStarted:
				if e.Name != "read_file" {
					t.Fatal("unexpected tool")
				}
				toolCalls++
			case domain.ModelRequestRecorded:
				for _, message := range e.Messages {
					if message.ProviderState == nil {
						continue
					}
					encoded, _ := json.Marshal(message.ProviderState)
					if !known[string(encoded)] {
						t.Fatal("recorded request changed native state")
					}
					replayed++
				}
			case domain.ModelUsageRecorded:
				usages = append(usages, e)
			}
		}
		if checkpoint == 0 || states < 4 || toolCalls != 1 || replayed == 0 || !strings.Contains(last.Text, "SYNTHETIC_NONCE_BLUE_731") || !strings.Contains(strings.ToLower(last.Text), "blue") {
			t.Fatal("live artifact coverage incomplete")
		}
		lastAfterCheckpoint := false
		for _, record := range records {
			if e, ok := record.Event.(domain.AssistantMessageCompleted); ok && e.TurnID == last.TurnID && record.Sequence > checkpoint {
				lastAfterCheckpoint = true
			}
		}
		if !lastAfterCheckpoint {
			t.Fatal("no completion after summary")
		}
		var transcript bytes.Buffer
		if _, err := ExportSession(ctx, config.DatabasePath, sessionID, &transcript); err != nil {
			t.Fatal("display export failed")
		}
		for _, record := range records {
			if e, ok := record.Event.(domain.AssistantMessageCompleted); ok {
				for _, b := range e.ProviderState.MessagesContent {
					if b.Thinking != "" && bytes.Contains(transcript.Bytes(), []byte(b.Thinking)) || b.Signature != nil && *b.Signature != "" && bytes.Contains(transcript.Bytes(), []byte(*b.Signature)) {
						t.Fatal("hidden content in display export")
					}
				}
			}
		}
		if err := assembly.Close(); err != nil {
			t.Fatal("close before cold audit failed")
		}
		assembly = nil
		inspection, err := InspectEvaluationStore(ctx, config.DatabasePath, sessionID)
		if err != nil {
			t.Fatal("cold inspection failed")
		}
		auditDir := filepath.Join(root, filepath.Base(workspace)+"-audit")
		if _, err := ExportEvaluationEvidence(ctx, inspection, EvaluationExportDestinations{TranscriptPath: filepath.Join(root, filepath.Base(workspace)+"-transcript.jsonl"), AuditDirectory: auditDir}); err != nil {
			t.Fatal("audit export failed")
		}
		audit, err := VerifyAuditSnapshot(auditDir)
		if err != nil || len(audit.Sessions) != 1 || !reflect.DeepEqual(audit.Sessions[0].Events, records) {
			t.Fatal("audit reconstruction differs")
		}
		encoded, _ := json.Marshal(usages)
		if err := os.WriteFile(filepath.Join(root, "composition-usage.json"), encoded, 0600); err != nil {
			t.Fatal("usage artifact failed")
		}
		t.Logf("LIVE_ARTIFACTS=verified tools=%d states=%d recorded_replay_blocks=%d events=%d; not a pass of the nonempty-retained-wire gate", toolCalls, states, replayed, len(records))
		return
	}
	run := func(id, input string) application.RunTurnResult {
		t.Helper()
		result, err := assembly.Service().RunTurn(ctx, application.RunTurnRequest{SessionID: sessionID, RequestID: domain.RunTurnRequestID(id), Input: input, Sink: &testkit.RecordingSink{}})
		if err != nil || result.Status != domain.TurnStatusCompleted {
			t.Fatalf("live phase %s failed; status=%s error=%v", id, result.Status, err)
		}
		t.Logf("phase=%s completed", id)
		return result
	}
	if !resume {
		read := run("read", "Synthetic protocol test: use read_file exactly once to read nonce.txt, then repeat its nonce. Do not use any other tool, and do not inspect any other file.")
		if !strings.Contains(read.Text, "SYNTHETIC_NONCE_BLUE_731") {
			t.Fatal("read nonce missing")
		}
		if err := assembly.Close(); err != nil {
			t.Fatal("first close failed")
		}
		assembly, err = Open(ctx, config)
		if err != nil {
			t.Fatal("reopen failed")
		}
		restarted := run("restart", "Without tools, repeat the nonce from the previous tool result, then briefly acknowledge this synthetic restart check.")
		if !strings.Contains(restarted.Text, "SYNTHETIC_NONCE_BLUE_731") {
			t.Fatal("restarted nonce missing")
		}
		run("retain", "Without tools, remember that the synthetic project color is blue. Reply briefly.")
	}
	if !resume {
		run("pressure", messagesPressureInput("covered"))
	}
	// Leave a second turn in the protected tail so the first padding turn is
	// coverable. A tiny covered prefix cannot amortize checkpoint framing.
	if !resume {
		run("pressure-tail", messagesPressureInput("retained"))
	}
	beforeSummary, err := application.ReadWholeStreamPinned(ctx, assembly.Store(), sessionID, 256)
	if err != nil {
		t.Fatal("preflight history read failed")
	}
	compacted, err := compactMessagesReplayProbe(beforeSummary, func() (application.CompactSessionResult, error) {
		return assembly.Service().CompactSession(ctx, application.CompactSessionRequest{SessionID: sessionID, Strategy: domain.ContextStrategySummary, Focus: "Preserve the nonce and project color. Disposable fixture rows contain no facts and must be omitted. Use the required eight headings, at most one short sentence per heading, under 150 words total."})
	})
	if err != nil || !compacted.Ran {
		t.Fatalf("live summary did not commit: %v ran=%t", err, compacted.Ran)
	}
	t.Logf("summary committed through_sequence=%d", compacted.ThroughSequence)
	if err := assembly.Close(); err != nil {
		t.Fatal("second close failed")
	}
	assembly, err = Open(ctx, config)
	if err != nil {
		t.Fatal("post-summary reopen failed")
	}
	last := run("after-summary", "Without tools, repeat the nonce and project color preserved in the conversation. Reply briefly.")
	if !strings.Contains(last.Text, "SYNTHETIC_NONCE_BLUE_731") || !strings.Contains(strings.ToLower(last.Text), "blue") {
		t.Fatal("post-summary facts lost")
	}
	records, err := application.ReadWholeStreamPinned(ctx, assembly.Store(), sessionID, 256)
	if err != nil {
		t.Fatal("read canonical history failed")
	}
	var expected []*domain.ProviderState
	tools := 0
	states := 0
	for _, record := range records {
		switch e := record.Event.(type) {
		case domain.ToolCallStarted:
			if e.Name != "read_file" {
				t.Fatal("unexpected tool started")
			}
			tools++
		case domain.AssistantMessageCompleted:
			if e.ProviderState == nil || e.ProviderState.Protocol != domain.DeepSeekMessagesV1 {
				t.Fatal("missing native state")
			}
			states++
			if record.Sequence > compacted.ThroughSequence && e.TurnID != last.TurnID {
				expected = append(expected, e.ProviderState)
			}
		case domain.ModelUsageRecorded:
			if e.TurnID == last.TurnID && (e.InputTokens == 0 || e.CachedInputTokens > e.InputTokens) {
				t.Fatal("normalized live input usage invalid")
			}
		}
	}
	if tools != 1 || states < 4 {
		t.Fatal("tool/state coverage incomplete")
	}
	transport.mu.Lock()
	actual := append([]json.RawMessage(nil), transport.lastAssistant...)
	transport.mu.Unlock()
	if len(expected) == 0 || len(expected) != len(actual) {
		t.Fatalf("nonempty retained assistant gate: expected=%d outbound=%d", len(expected), len(actual))
	}
	for i, s := range expected {
		want, _ := json.Marshal(s.MessagesContent)
		if !bytes.Equal(want, actual[i]) {
			t.Fatal("native retained blocks changed across summary/reopen")
		}
	}
	var transcript bytes.Buffer
	if _, err := ExportSession(ctx, config.DatabasePath, sessionID, &transcript); err != nil {
		t.Fatal("transcript export failed")
	}
	for _, record := range records {
		if e, ok := record.Event.(domain.AssistantMessageCompleted); ok {
			for _, b := range e.ProviderState.MessagesContent {
				if b.Thinking != "" && bytes.Contains(transcript.Bytes(), []byte(b.Thinking)) {
					t.Fatal("hidden thinking in transcript")
				}
				if b.Signature != nil && *b.Signature != "" && bytes.Contains(transcript.Bytes(), []byte(*b.Signature)) {
					t.Fatal("signature in transcript")
				}
			}
		}
	}
	if err := assembly.Close(); err != nil {
		t.Fatal("final close failed")
	}
	assembly = nil
	inspection, err := InspectEvaluationStore(ctx, config.DatabasePath, sessionID)
	if err != nil {
		t.Fatal("inspection failed")
	}
	auditDir := filepath.Join(root, filepath.Base(workspace)+"-audit")
	if _, err := ExportEvaluationEvidence(ctx, inspection, EvaluationExportDestinations{TranscriptPath: filepath.Join(root, filepath.Base(workspace)+"-transcript.jsonl"), AuditDirectory: auditDir}); err != nil {
		t.Fatal("audit export failed")
	}
	audit, err := VerifyAuditSnapshot(auditDir)
	if err != nil || len(audit.Sessions) != 1 || !reflect.DeepEqual(audit.Sessions[0].Events, records) {
		t.Fatal("audit reconstruction differs")
	}
	mode := "LIVE_COMPOSITION"
	if fixture != nil {
		mode = "LOCAL_REHEARSAL"
	}
	t.Logf("%s=passed tools=%d completed_states=%d retained_states=%d canonical_events=%d", mode, tools, states, len(expected), len(records))
	var usage []domain.ModelUsageRecorded
	for _, record := range records {
		if e, ok := record.Event.(domain.ModelUsageRecorded); ok {
			usage = append(usage, e)
		}
	}
	f, err := os.OpenFile(filepath.Join(root, "composition-usage.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("usage artifact unavailable")
	}
	defer f.Close()
	if json.NewEncoder(f).Encode(usage) != nil {
		t.Fatal("usage artifact failed")
	}
}

func messagesPressureInput(label string) string {
	var out strings.Builder
	out.WriteString("Synthetic context fixture. Do not use tools. These numbered test rows are disposable and add no project facts. Do not copy or summarize the rows; reply only ACK. Preserve the earlier nonce and color.\n")
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&out, "fixture=%s row=%03d sample=%06d status=discard-after-test\n", label, i, (i*7919)%1000000)
	}
	return out.String()
}

// SelectCutPoint retains the newest Turn whole. A failed/open newest Turn
// cannot supply this probe's positive completed-assistant replay evidence.
// Reject it before compaction instead of paying for a vacuous wire comparison.
func messagesReplayTail(records []domain.RecordedEvent) (domain.TurnID, error) {
	var latest domain.TurnID
	completed, native := false, false
	for _, record := range records {
		switch e := record.Event.(type) {
		case domain.TurnStarted:
			latest, completed, native = e.TurnID, false, false
		case domain.AssistantMessageCompleted:
			if e.TurnID == latest && e.ProviderState != nil && e.ProviderState.Protocol == domain.DeepSeekMessagesV1 && len(e.ProviderState.MessagesContent) > 0 {
				native = true
			}
		case domain.TurnCompleted:
			if e.TurnID == latest {
				completed = true
			}
		case domain.TurnFailed:
			if e.TurnID == latest {
				completed = false
			}
		}
	}
	if latest == "" || !completed || !native {
		return "", errors.New("replay preflight: newest turn must be completed with native assistant blocks; no summary sent")
	}
	return latest, nil
}

// Both the live path and callback-spy tests use this ordering gate. No summary
// callback runs for a failed/open tail; no continuation follows a covered tail.
func compactMessagesReplayProbe(records []domain.RecordedEvent, compact func() (application.CompactSessionResult, error)) (application.CompactSessionResult, error) {
	tail, err := messagesReplayTail(records)
	if err != nil {
		return application.CompactSessionResult{}, err
	}
	result, err := compact()
	if err != nil {
		return result, err
	}
	if !result.Ran {
		return result, errors.New("summary did not run; continuation not sent")
	}
	for _, record := range records {
		if e, ok := record.Event.(domain.AssistantMessageCompleted); ok && e.TurnID == tail && record.Sequence > result.ThroughSequence {
			return result, nil
		}
	}
	return result, errors.New("summary covered required native tail; continuation not sent")
}

type compositionLiveTransport struct {
	t             *testing.T
	root          string
	base          http.RoundTripper
	mu            sync.Mutex
	lastAssistant []json.RawMessage
	offline       bool
}

func (tr *compositionLiveTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if tr.offline {
		return nil, errors.New("offline inspection forbids HTTP")
	}
	if r.Method != "POST" || r.URL.Scheme != "https" || r.URL.Host != "api.deepseek.com" || r.URL.Path != "/anthropic/v1/messages" {
		return nil, errors.New("live route cap")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (32<<10)+1))
	r.Body.Close()
	if err != nil || len(body) > 32<<10 {
		return nil, errors.New("live byte cap")
	}
	var request struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Stream    bool   `json:"stream"`
		Messages  []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &request) != nil || request.Model != "deepseek-flash" || !request.Stream || request.MaxTokens < 1 || request.MaxTokens > 4096 {
		return nil, errors.New("live model/output cap")
	}
	f, err := os.OpenFile(filepath.Join(tr.root, "requests.jsonl"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("live ledger unavailable")
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX) != nil {
		return nil, errors.New("ledger lock failed")
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	prior, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil || len(prior) >= 64<<10 {
		return nil, errors.New("ledger invalid")
	}
	count := bytes.Count(prior, []byte("\n"))
	if count >= 12 {
		return nil, errors.New("live total request cap")
	}
	entry, _ := json.Marshal(map[string]any{"attempt": count + 1, "requestBytes": len(body), "maxOutputTokens": request.MaxTokens, "at": time.Now().UTC()})
	if _, err = f.Write(append(entry, '\n')); err != nil || f.Sync() != nil {
		return nil, errors.New("reservation failed")
	}
	tr.mu.Lock()
	tr.lastAssistant = nil
	for _, m := range request.Messages {
		if m.Role == "assistant" {
			tr.lastAssistant = append(tr.lastAssistant, append(json.RawMessage(nil), m.Content...))
		}
	}
	tr.mu.Unlock()
	tr.t.Logf("reserved_request=%d bytes=%d max_output=%d", count+1, len(body), request.MaxTokens)
	r.Body = io.NopCloser(bytes.NewReader(body))
	response, err := tr.base.RoundTrip(r)
	if err == nil {
		tr.t.Logf("http_status=%d", response.StatusCode)
		if response.StatusCode == http.StatusOK {
			capture, captureErr := os.OpenFile(filepath.Join(tr.root, fmt.Sprintf("response-%02d.sse", count+1)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if captureErr != nil {
				response.Body.Close()
				return nil, errors.New("private capture unavailable")
			}
			response.Body = &compositionCaptureBody{ReadCloser: response.Body, file: capture}
		}
	}
	return response, err
}

// Private diagnostic evidence, never test output. Keep capture bounded even if
// the provider ignores max_tokens; the production decoder keeps its own bounds.
type compositionCaptureBody struct {
	io.ReadCloser
	file *os.File
	mu   sync.Mutex
	n    int
	once sync.Once
	err  error
}

func (b *compositionCaptureBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	defer b.mu.Unlock()
	if keep := min(n, (1<<20)-b.n); keep > 0 {
		if _, writeErr := b.file.Write(p[:keep]); writeErr != nil {
			return n, errors.New("private capture write failed")
		}
		b.n += keep
	}
	return n, err
}

func (b *compositionCaptureBody) Close() error {
	b.once.Do(func() {
		b.err = b.ReadCloser.Close()
		b.mu.Lock()
		defer b.mu.Unlock()
		b.err = errors.Join(b.err, b.file.Close())
	})
	return b.err
}
