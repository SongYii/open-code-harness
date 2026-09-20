//go:build livecomposition

package composition

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// Runs exactly the live scenario and its nonempty wire/audit assertions, but
// supplies the only transport in process: no DNS, socket or credential access.
func TestMessagesCompositionLocalRehearsal(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "och-deepseek-live.fixture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	for _, name := range []string{"OCH_MESSAGES_LIVE_CONFIRM", "OCH_MESSAGES_LIVE_INSPECT", "OCH_MESSAGES_LIVE_VERIFY", "OCH_MESSAGES_LIVE_RESUME_WORKSPACE"} {
		t.Setenv(name, "")
	}
	t.Setenv("OCH_MESSAGES_LIVE_ARTIFACTS", root)
	fixture := &messagesRehearsalTransport{t: t}
	runMessagesComposition(t, fixture)
	if fixture.calls != 8 || fixture.summaries != 1 {
		t.Fatalf("fixture calls=%d summaries=%d; want 8 and 1", fixture.calls, fixture.summaries)
	}
	ledger, err := os.ReadFile(filepath.Join(root, "requests.jsonl"))
	if err != nil || bytes.Count(ledger, []byte("\n")) != 8 {
		t.Fatal("rehearsal bypassed the shared reservation guard")
	}
}

type messagesRehearsalTransport struct {
	t                *testing.T
	calls, summaries int
}

func (tr *messagesRehearsalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.calls++
	ledger, err := os.ReadFile(filepath.Join(os.Getenv("OCH_MESSAGES_LIVE_ARTIFACTS"), "requests.jsonl"))
	if err != nil || bytes.Count(ledger, []byte("\n")) != tr.calls {
		tr.t.Fatal("transport reached before its durable reservation")
	}
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		return nil, err
	}
	isSummary := r.Header.Get("X-Och-Request-Purpose") == "compaction"
	if isSummary != (tr.calls == 7) {
		tr.t.Fatal("summary at unexpected fixture phase")
	}
	text := "ACK"
	switch tr.calls {
	case 2, 3, 8:
		text = "SYNTHETIC_NONCE_BLUE_731; project color blue."
	case 4:
		text = "Project color blue remembered."
	case 7:
		tr.summaries++
		if bytes.Contains(body, []byte("fixture-private-thinking")) || bytes.Contains(body, []byte("fixture-signed-block")) {
			tr.t.Fatal("summary received hidden state")
		}
		text = "## Objective\nContinue the synthetic test.\n## User Constraints\nRead-only.\n## Established Facts\nNonce SYNTHETIC_NONCE_BLUE_731; color blue.\n## Work Completed\nRead nonce.txt and reopened.\n## Files and Commands\nnonce.txt.\n## Open Work\nRecall the facts.\n## Risks and Unknowns\nNone.\n## Continuation\nKeep the nonce and color."
	}
	if tr.calls > 8 {
		tr.t.Fatal("unexpected model request")
	}
	var stream strings.Builder
	frame := func(name string, fields map[string]any) {
		if fields == nil {
			fields = map[string]any{}
		}
		fields["type"] = name
		encoded, err := json.Marshal(fields)
		if err != nil {
			tr.t.Fatal(err)
		}
		fmt.Fprintf(&stream, "event: %s\ndata: %s\n\n", name, encoded)
	}
	frame("message_start", map[string]any{"message": map[string]any{"id": "msg_local", "type": "message", "role": "assistant", "model": "deepseek-flash", "content": []any{}, "usage": map[string]int{"input_tokens": 120, "cache_read_input_tokens": 256, "output_tokens": 1}}})
	frame("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "thinking", "thinking": ""}})
	frame("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": fmt.Sprintf("fixture-private-thinking-%d", tr.calls)}})
	frame("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "signature_delta", "signature": fmt.Sprintf("fixture-signed-block-%d", tr.calls)}})
	frame("content_block_stop", map[string]any{"index": 0})
	reason := "end_turn"
	if tr.calls == 1 {
		reason = "tool_use"
		frame("content_block_start", map[string]any{"index": 1, "content_block": map[string]any{"type": "tool_use", "id": "fixture_read", "name": "read_file", "input": map[string]any{}}})
		frame("content_block_delta", map[string]any{"index": 1, "delta": map[string]any{"type": "input_json_delta", "partial_json": `{"path":"nonce.txt"}`}})
	} else {
		frame("content_block_start", map[string]any{"index": 1, "content_block": map[string]any{"type": "text", "text": text}})
	}
	frame("content_block_stop", map[string]any{"index": 1})
	frame("message_delta", map[string]any{"delta": map[string]any{"stop_reason": reason}, "usage": map[string]int{"output_tokens": 20}})
	frame("message_stop", nil)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream.String()))}, nil
}

func TestMessagesReplayTailPreflight(t *testing.T) {
	state := &domain.ProviderState{Protocol: domain.DeepSeekMessagesV1, MessagesContent: []domain.ProviderContentBlock{{Type: "text", Text: "fixture"}}}
	completed := []domain.Event{domain.TurnStarted{TurnID: "old"}, domain.AssistantMessageCompleted{TurnID: "old", ProviderState: state}, domain.TurnCompleted{TurnID: "old"}}
	for _, tc := range []struct {
		name  string
		extra []domain.Event
		want  domain.TurnID
	}{
		{"completed native control", nil, "old"},
		{"new open turn", []domain.Event{domain.TurnStarted{TurnID: "new"}}, ""},
		{"new failed turn", []domain.Event{domain.TurnStarted{TurnID: "new"}, domain.TurnFailed{TurnID: "new"}}, ""},
		{"failed after assistant", []domain.Event{domain.TurnStarted{TurnID: "new"}, domain.AssistantMessageCompleted{TurnID: "new", ProviderState: state}, domain.TurnFailed{TurnID: "new"}}, ""},
		{"completed without native state", []domain.Event{domain.TurnStarted{TurnID: "new"}, domain.AssistantMessageCompleted{TurnID: "new"}, domain.TurnCompleted{TurnID: "new"}}, ""},
		{"new completed native control", []domain.Event{domain.TurnStarted{TurnID: "new"}, domain.AssistantMessageCompleted{TurnID: "new", ProviderState: state}, domain.TurnCompleted{TurnID: "new"}}, "new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var records []domain.RecordedEvent
			for i, event := range append(append([]domain.Event(nil), completed...), tc.extra...) {
				records = append(records, domain.RecordedEvent{Sequence: uint64(i + 1), Event: event})
			}
			got, err := messagesReplayTail(records)
			if got != tc.want || (err == nil) != (tc.want != "") {
				t.Fatalf("tail=%q error=%v; want %q", got, err, tc.want)
			}
			calls := 0
			_, err = compactMessagesReplayProbe(records, func() (application.CompactSessionResult, error) {
				calls++
				return application.CompactSessionResult{Ran: true}, nil
			})
			wantCalls := 0
			if tc.want != "" {
				wantCalls = 1
			}
			if calls != wantCalls || (err == nil) != (tc.want != "") {
				t.Fatalf("summary callbacks=%d error=%v; want callbacks=%d", calls, err, wantCalls)
			}
		})
	}
	if _, err := messagesReplayTail(nil); err == nil {
		t.Fatal("empty history accepted")
	}
}

func TestMessagesCompactionMustLeaveReplayTail(t *testing.T) {
	state := &domain.ProviderState{Protocol: domain.DeepSeekMessagesV1, MessagesContent: []domain.ProviderContentBlock{{Type: "text", Text: "fixture"}}}
	records := []domain.RecordedEvent{{Sequence: 1, Event: domain.TurnStarted{TurnID: "tail"}}, {Sequence: 2, Event: domain.AssistantMessageCompleted{TurnID: "tail", ProviderState: state}}, {Sequence: 3, Event: domain.TurnCompleted{TurnID: "tail"}}}
	for _, tc := range []struct {
		name   string
		result application.CompactSessionResult
		ok     bool
	}{
		{"retained control", application.CompactSessionResult{Ran: true, ThroughSequence: 0}, true},
		{"no compaction", application.CompactSessionResult{}, false},
		{"covered assistant", application.CompactSessionResult{Ran: true, ThroughSequence: 3}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compactMessagesReplayProbe(records, func() (application.CompactSessionResult, error) { return tc.result, nil })
			if (err == nil) != tc.ok {
				t.Fatalf("error=%v; want success=%t", err, tc.ok)
			}
		})
	}
}
