package transcript

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/memory"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

var (
	exportNow       = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	exportAuthority = application.WriterAuthority{RuntimeID: "runtime-transcript", FencingToken: 1}
)

func TestWriteSessionUnknownSessionWritesNothing(t *testing.T) {
	t.Parallel()

	store := newExportStore(t)
	var buf bytes.Buffer
	_, err := WriteSession(context.Background(), store, "session-missing", exportNow, &buf)
	if !IsCode(err, CodeSessionNotFound) {
		t.Fatalf("WriteSession() error = %v, want code %q", err, CodeSessionNotFound)
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes after session_not_found", buf.Len())
	}
}

func TestWriteSessionInvalidSessionIDWritesNothing(t *testing.T) {
	t.Parallel()

	store := newExportStore(t)
	var buf bytes.Buffer
	_, err := WriteSession(context.Background(), store, " session-1", exportNow, &buf)
	if !IsCode(err, CodeInvalidSessionID) {
		t.Fatalf("WriteSession() error = %v, want code %q", err, CodeInvalidSessionID)
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes after invalid_session_id", buf.Len())
	}
}

func TestWriteSessionSnapshotBits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		events  []domain.Event
		open    bool
		running bool
	}{
		{
			name:    "idle completed session",
			events:  idleTurnEvents(),
			open:    true,
			running: false,
		},
		{
			name: "in-flight turn",
			events: []domain.Event{
				domain.SessionCreated{WorkspaceRoot: "/workspace"},
				domain.TurnStarted{TurnID: "turn-1", Input: "inspect"},
			},
			open:    true,
			running: true,
		},
		{
			name: "closed session",
			events: []domain.Event{
				domain.SessionCreated{WorkspaceRoot: "/workspace"},
				domain.SessionClosed{},
			},
			open:    false,
			running: false,
		},
		{
			name: "deleted session",
			events: []domain.Event{
				domain.SessionCreated{WorkspaceRoot: "/workspace"},
				domain.SessionDeleted{},
			},
			open:    false,
			running: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			store := newExportStore(t)
			sessionID := domain.SessionID("session-1")
			appendEvents(t, store, sessionID, test.events...)

			var buf bytes.Buffer
			result, err := WriteSession(context.Background(), store, sessionID, exportNow, &buf)
			if err != nil {
				t.Fatalf("WriteSession() error = %v", err)
			}
			if result.Open != test.open || result.Running != test.running {
				t.Fatalf("result open/running = %t/%t, want %t/%t", result.Open, result.Running, test.open, test.running)
			}
			snapshot, complete, _ := mustAcceptExport(t, buf.Bytes())
			if snapshot.Open != test.open || snapshot.Running != test.running {
				t.Fatalf("snapshot open/running = %t/%t, want %t/%t", snapshot.Open, snapshot.Running, test.open, test.running)
			}
			if complete.Open != test.open || complete.Running != test.running {
				t.Fatalf("complete open/running = %t/%t, want %t/%t", complete.Open, complete.Running, test.open, test.running)
			}
		})
	}
}

func TestWriteSessionCompleteTrailerAndFactLines(t *testing.T) {
	t.Parallel()

	store := newExportStore(t)
	sessionID := domain.SessionID("session-1")
	events := twoStepHistory(true)
	appendEvents(t, store, sessionID, events...)

	var buf bytes.Buffer
	result, err := WriteSession(context.Background(), store, sessionID, exportNow, &buf)
	if err != nil {
		t.Fatalf("WriteSession() error = %v", err)
	}
	snapshot, complete, facts := mustAcceptExport(t, buf.Bytes())
	if result.HeadSequence != snapshot.HeadSequence || result.FactLines != complete.FactLines {
		t.Fatalf("result = %+v, snapshot head = %d, complete facts = %d", result, snapshot.HeadSequence, complete.FactLines)
	}
	if complete.HeadSequence != snapshot.HeadSequence {
		t.Fatalf("complete.headSequence = %d, snapshot.headSequence = %d", complete.HeadSequence, snapshot.HeadSequence)
	}
	if complete.FactLines != uint64(len(facts)) {
		t.Fatalf("complete.factLines = %d, intervening = %d", complete.FactLines, len(facts))
	}
	if result.HeadSequence != uint64(len(events)) {
		t.Fatalf("headSequence = %d, want %d", result.HeadSequence, len(events))
	}

	var usage int
	for _, fact := range facts {
		if fact.Type == domain.EventModelRequestRecorded || fact.Type == domain.EventPolicyDecisionRecorded {
			t.Fatalf("omitted type emitted: %s", fact.Type)
		}
		if fact.Type == domain.EventModelUsageRecorded {
			usage++
		}
	}
	if usage != 1 {
		t.Fatalf("usage lines = %d, want 1", usage)
	}
}

// TestWriteSessionExportsCompactionFacts proves a Session containing a
// Context Engine compaction exports successfully end to end: before this
// task's ProjectRecord/factPayloadKeys additions, the codec's own
// default-case CodeUnsupportedEventType would have failed the whole export
// the moment it reached a context.compaction.* event -- so this is a real
// regression guard, not just new-feature coverage.
func TestWriteSessionExportsCompactionFacts(t *testing.T) {
	t.Parallel()

	store := newExportStore(t)
	sessionID := domain.SessionID("session-1")
	appendEvents(t, store, sessionID,
		domain.SessionCreated{WorkspaceRoot: "/workspace"},
		domain.TurnStarted{TurnID: "turn-1", Input: "hi"},
		domain.TurnCompleted{TurnID: "turn-1"},
		domain.ContextCompactionStarted{
			ID: "compaction-1", Trigger: domain.ContextTriggerManual, Strategy: domain.ContextStrategySummary,
			BaseSourceHead: 3, SourceSchema: "och_source_v1", MeterID: "och_wire_estimate_v1",
		},
		domain.ContextCompactionCompleted{
			ID: "compaction-1",
			Checkpoint: domain.ContextCheckpointRecord{
				ID: "checkpoint-1", Kind: domain.ContextCheckpointKindRollingSummary, SourceSchema: "och_source_v1",
				ThroughSequence: 3, SourceDigestHex: strings.Repeat("0", 64), Summary: "a summary",
			},
		},
	)

	var buf bytes.Buffer
	result, err := WriteSession(context.Background(), store, sessionID, exportNow, &buf)
	if err != nil {
		t.Fatalf("WriteSession() error = %v", err)
	}
	_, complete, facts := mustAcceptExport(t, buf.Bytes())
	if result.HeadSequence != 5 || complete.FactLines != uint64(len(facts)) {
		t.Fatalf("result = %+v, complete = %+v, facts = %d", result, complete, len(facts))
	}
	var sawStarted, sawCompleted bool
	for _, fact := range facts {
		switch fact.Type {
		case domain.EventContextCompactionStarted:
			sawStarted = true
		case domain.EventContextCompactionCompleted:
			sawCompleted = true
			var payload contextCompactionCompletedPayload
			if err := json.Unmarshal(fact.Payload, &payload); err != nil {
				t.Fatalf("unmarshal completed payload: %v", err)
			}
			if payload.Checkpoint.Summary != "a summary" {
				t.Fatalf("checkpoint summary = %q, want %q (transcript must carry the summary text)", payload.Checkpoint.Summary, "a summary")
			}
		}
	}
	if !sawStarted || !sawCompleted {
		t.Fatalf("facts = %+v, want both context.compaction.started and .completed", facts)
	}
}

// TestWriteSessionDeletedSessionExportsDeletionFact proves that logical
// deletion is append-only evidence: the session.deleted fact survives export
// with an empty payload, the snapshot/complete envelopes agree it is neither
// open nor running, and the trailer still completes normally.
func TestWriteSessionDeletedSessionExportsDeletionFact(t *testing.T) {
	t.Parallel()

	store := newExportStore(t)
	sessionID := domain.SessionID("session-1")
	appendEvents(t, store, sessionID,
		domain.SessionCreated{WorkspaceRoot: "/workspace"},
		domain.SessionDeleted{},
	)

	var buf bytes.Buffer
	result, err := WriteSession(context.Background(), store, sessionID, exportNow, &buf)
	if err != nil {
		t.Fatalf("WriteSession() error = %v", err)
	}
	if result.Open || result.Running {
		t.Fatalf("result open/running = %t/%t, want false/false for a deleted session", result.Open, result.Running)
	}
	if result.FactLines != 2 {
		t.Fatalf("result.FactLines = %d, want 2 (session.created, session.deleted)", result.FactLines)
	}
	snapshot, complete, facts := mustAcceptExport(t, buf.Bytes())
	if snapshot.Open || snapshot.Running || complete.Open || complete.Running {
		t.Fatalf("snapshot/complete open/running = %t/%t, %t/%t, want all false", snapshot.Open, snapshot.Running, complete.Open, complete.Running)
	}
	if complete.FactLines != 2 {
		t.Fatalf("complete.FactLines = %d, want 2", complete.FactLines)
	}
	if len(facts) != 2 || facts[1].Type != domain.EventSessionDeleted || string(facts[1].Payload) != "{}" {
		t.Fatalf("facts = %+v, want a trailing session.deleted fact with an empty payload", facts)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"type":"transcript.complete"`)) {
		t.Fatalf("export = %s, missing a complete trailer", buf.Bytes())
	}
}

func TestWriteSessionOmitsUsageWhenAbsent(t *testing.T) {
	t.Parallel()

	store := newExportStore(t)
	sessionID := domain.SessionID("session-1")
	appendEvents(t, store, sessionID, idleTurnEvents()...)

	var buf bytes.Buffer
	if _, err := WriteSession(context.Background(), store, sessionID, exportNow, &buf); err != nil {
		t.Fatalf("WriteSession() error = %v", err)
	}
	_, _, facts := mustAcceptExport(t, buf.Bytes())
	for _, fact := range facts {
		if fact.Type == domain.EventModelUsageRecorded {
			t.Fatal("usage line emitted without model.usage.recorded")
		}
	}
}

func TestWriteSessionStepRefAlignment(t *testing.T) {
	t.Parallel()

	store := newExportStore(t)
	sessionID := domain.SessionID("session-1")
	appendEvents(t, store, sessionID, twoStepHistory(false)...)

	var buf bytes.Buffer
	if _, err := WriteSession(context.Background(), store, sessionID, exportNow, &buf); err != nil {
		t.Fatalf("WriteSession() error = %v", err)
	}
	_, _, facts := mustAcceptExport(t, buf.Bytes())

	var started []toolStepPayload
	var terminals []toolStepPayload
	for _, fact := range facts {
		var payload toolStepPayload
		if err := json.Unmarshal(fact.Payload, &payload); err != nil {
			t.Fatalf("payload unmarshal: %v", err)
		}
		switch fact.Type {
		case domain.EventToolCallStarted:
			started = append(started, payload)
		case domain.EventToolCallCompleted, domain.EventToolCallFailed, domain.EventToolCallInterrupted:
			terminals = append(terminals, payload)
		}
	}
	if len(started) != 2 || len(terminals) != 2 {
		t.Fatalf("tool started = %d, terminals = %d, want 2/2", len(started), len(terminals))
	}
	for i, want := range []uint32{1, 2} {
		if started[i].StepIndex != want || started[i].StepRef != fmt.Sprintf("turn-1/%d", want) {
			t.Fatalf("started[%d] = %+v, want stepIndex %d", i, started[i], want)
		}
		if terminals[i].StepIndex != want || terminals[i].StepRef != started[i].StepRef {
			t.Fatalf("terminal[%d] = %+v, want started %+v", i, terminals[i], started[i])
		}
	}
}

func TestWriteSessionDoublePinnedIgnoresLaterAppend(t *testing.T) {
	t.Parallel()

	store := newExportStore(t)
	sessionID := domain.SessionID("session-1")
	appendEvents(t, store, sessionID, domain.SessionCreated{WorkspaceRoot: "/workspace"})

	probe := &appendAfterFirstRead{
		t:         t,
		store:     store,
		sessionID: sessionID,
		extra:     domain.TurnStarted{TurnID: "turn-late", Input: "too late"},
	}
	var buf bytes.Buffer
	result, err := WriteSession(context.Background(), probe, sessionID, exportNow, &buf)
	if err != nil {
		t.Fatalf("WriteSession() error = %v", err)
	}
	if result.HeadSequence != 1 || result.FactLines != 1 {
		t.Fatalf("result = %+v, want pinned head 1 with one fact", result)
	}
	_, _, facts := mustAcceptExport(t, buf.Bytes())
	if len(facts) != 1 || facts[0].Type != domain.EventSessionCreated {
		t.Fatalf("facts = %+v, want only session.created", facts)
	}
	if !probe.sawPinnedHead {
		t.Fatal("second pass did not pin HeadVersion")
	}

	page, err := store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: sessionID, Limit: 256})
	if err != nil {
		t.Fatal(err)
	}
	if page.HeadVersion != 2 {
		t.Fatalf("live head = %d, want 2 after late append", page.HeadVersion)
	}
}

func TestWriteSessionReadsPagesOf256(t *testing.T) {
	t.Parallel()

	events := []domain.Event{domain.SessionCreated{WorkspaceRoot: "/workspace"}}
	for i := 0; i < 128; i++ {
		turnID := domain.TurnID(fmt.Sprintf("turn-%d", i+1))
		events = append(events, domain.TurnStarted{TurnID: turnID, Input: "go"}, domain.TurnCompleted{TurnID: turnID})
	}
	sessionID := domain.SessionID("session-pages")
	reader := &recordingReader{inner: &staticReader{records: recordedEvents(sessionID, events...)}}

	var buf bytes.Buffer
	result, err := WriteSession(context.Background(), reader, sessionID, exportNow, &buf)
	if err != nil {
		t.Fatalf("WriteSession() error = %v", err)
	}
	if result.HeadSequence != uint64(len(events)) {
		t.Fatalf("headSequence = %d, want %d", result.HeadSequence, len(events))
	}
	mustAcceptExport(t, buf.Bytes())
	if len(reader.limits) < 2 {
		t.Fatalf("ReadStream calls = %d, want pagination", len(reader.limits))
	}
	for i, limit := range reader.limits {
		if limit != 256 {
			t.Fatalf("ReadStream[%d] limit = %d, want 256", i, limit)
		}
	}
	if reader.limits[0] != streamPageLimit {
		t.Fatalf("page limit constant = %d", streamPageLimit)
	}
}

func TestWriteSessionLineLimitOmitsComplete(t *testing.T) {
	t.Parallel()

	sessionID := domain.SessionID("session-1")
	reader := &staticReader{records: recordedEvents(sessionID,
		domain.SessionCreated{WorkspaceRoot: "/workspace"},
		domain.TurnStarted{TurnID: "turn-1", Input: "inspect"},
		domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"},
		domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: strings.Repeat("a", maxLineBytes)},
	)}
	var buf bytes.Buffer
	_, err := WriteSession(context.Background(), reader, sessionID, exportNow, &buf)
	if !IsCode(err, CodeLineLimit) {
		t.Fatalf("WriteSession() error = %v, want code %q", err, CodeLineLimit)
	}
	assertRejectedWithoutComplete(t, buf.Bytes())
}

func TestWriteSessionCancelAfterSnapshotOmitsComplete(t *testing.T) {
	t.Parallel()

	store := newExportStore(t)
	sessionID := domain.SessionID("session-1")
	appendEvents(t, store, sessionID, domain.SessionCreated{WorkspaceRoot: "/workspace"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &cancelAfterWrite{cancel: cancel}
	_, err := WriteSession(ctx, store, sessionID, exportNow, writer)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteSession() error = %v, want context.Canceled", err)
	}
	assertRejectedWithoutComplete(t, writer.buf.Bytes())
	if !bytes.Contains(writer.buf.Bytes(), []byte(`"type":"transcript.snapshot"`)) {
		t.Fatalf("expected snapshot before cancel, got %s", writer.buf.Bytes())
	}
}

func TestWriteSessionShortWrite(t *testing.T) {
	t.Parallel()

	store := newExportStore(t)
	sessionID := domain.SessionID("session-1")
	appendEvents(t, store, sessionID, domain.SessionCreated{WorkspaceRoot: "/workspace"})

	for _, shortOn := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("write %d", shortOn), func(t *testing.T) {
			writer := &shortWriter{shortOn: shortOn}
			_, err := WriteSession(context.Background(), store, sessionID, exportNow, writer)
			if !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("WriteSession() error = %v, want io.ErrShortWrite", err)
			}
			if shortOn > 1 && !bytes.Contains(writer.buf.Bytes(), []byte(`"type":"transcript.snapshot"`)) {
				t.Fatal("expected snapshot before later short write")
			}
			if bytes.Contains(writer.buf.Bytes(), []byte(`"type":"transcript.complete"`)) && shortOn <= 3 {
				if shortOn < 3 {
					t.Fatal("complete trailer must not be published after an earlier short write")
				}
			}
			if _, _, _, acceptErr := consumerAccepts(writer.buf.Bytes()); acceptErr == nil {
				t.Fatal("consumer accepted a short write")
			}
		})
	}
}

func TestWriteSessionCorruptStoreWritesNothing(t *testing.T) {
	t.Parallel()

	corrupt, err := application.NewStoreError(application.StoreError{
		Code:      application.StoreCodeCorrupt,
		SessionID: "session-1",
		Cause:     errors.New("unreadable canonical payload"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_, writeErr := WriteSession(context.Background(), &staticReader{err: corrupt}, "session-1", exportNow, &buf)
	if !application.IsStoreCode(writeErr, application.StoreCodeCorrupt) {
		t.Fatalf("WriteSession() error = %v, want store corrupt", writeErr)
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes after corrupt read", buf.Len())
	}
}

func TestWriteSessionUnsupportedCanonicalTypeWritesNothing(t *testing.T) {
	t.Parallel()

	sessionID := domain.SessionID("session-1")
	records := recordedEvents(sessionID, domain.SessionCreated{WorkspaceRoot: "/workspace"})
	records = append(records, domain.RecordedEvent{
		SchemaVersion: 1,
		ID:            "event-2",
		CommandID:     "command-2",
		SessionID:     sessionID,
		Sequence:      2,
		OccurredAt:    exportNow,
		Event:         unknownEvent{},
	})
	var buf bytes.Buffer
	_, err := WriteSession(context.Background(), &staticReader{records: records}, sessionID, exportNow, &buf)
	if !IsCode(err, CodeUnsupportedEventType) {
		t.Fatalf("WriteSession() error = %v, want code %q", err, CodeUnsupportedEventType)
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes after unsupported canonical type", buf.Len())
	}
}

func TestWriteSessionUnsupportedSchemaVersionWritesNothing(t *testing.T) {
	t.Parallel()

	sessionID := domain.SessionID("session-1")
	records := recordedEvents(sessionID, domain.SessionCreated{WorkspaceRoot: "/workspace"})
	records[0].SchemaVersion = 2
	var buf bytes.Buffer
	_, err := WriteSession(context.Background(), &staticReader{records: records}, sessionID, exportNow, &buf)
	if !IsCode(err, CodeUnsupportedSchemaVersion) {
		t.Fatalf("WriteSession() error = %v, want code %q", err, CodeUnsupportedSchemaVersion)
	}
	if buf.Len() != 0 {
		t.Fatalf("wrote %d bytes after unsupported schema version", buf.Len())
	}
}

func TestWriteSessionCorruptAfterSnapshotOmitsComplete(t *testing.T) {
	t.Parallel()

	store := newExportStore(t)
	sessionID := domain.SessionID("session-1")
	appendEvents(t, store, sessionID, domain.SessionCreated{WorkspaceRoot: "/workspace"})
	corrupt, err := application.NewStoreError(application.StoreError{
		Code:      application.StoreCodeCorrupt,
		SessionID: sessionID,
		Cause:     errors.New("unreadable canonical payload"),
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := &recordingReader{inner: store, failAt: 2, failErr: corrupt}
	var buf bytes.Buffer
	_, writeErr := WriteSession(context.Background(), reader, sessionID, exportNow, &buf)
	if !application.IsStoreCode(writeErr, application.StoreCodeCorrupt) {
		t.Fatalf("WriteSession() error = %v, want store corrupt", writeErr)
	}
	assertRejectedWithoutComplete(t, buf.Bytes())
}

type toolStepPayload struct {
	StepIndex uint32 `json:"stepIndex"`
	StepRef   string `json:"stepRef"`
	CallID    string `json:"callID"`
}

type staticReader struct {
	records []domain.RecordedEvent
	err     error
}

func (r *staticReader) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	if err := ctx.Err(); err != nil {
		return application.StreamPage{}, err
	}
	if r.err != nil {
		return application.StreamPage{}, r.err
	}
	head := uint64(len(r.records))
	if request.HeadVersion != nil {
		head = *request.HeadVersion
	}
	if request.AfterSequence > head {
		return application.StreamPage{}, invalidRead(request.SessionID, "invalid pinned cursor")
	}
	start := request.AfterSequence
	end := start + uint64(request.Limit)
	if end > head {
		end = head
	}
	var records []domain.RecordedEvent
	if start < end {
		records = append([]domain.RecordedEvent(nil), r.records[start:end]...)
	}
	next := start
	if len(records) > 0 {
		next = records[len(records)-1].Sequence
	}
	return application.StreamPage{Records: records, HeadVersion: head, NextAfterSequence: next, End: next == head}, nil
}

type recordingReader struct {
	inner   StreamReader
	limits  []uint32
	failAt  int
	failErr error
	n       int
}

func (r *recordingReader) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	r.n++
	r.limits = append(r.limits, request.Limit)
	if r.failAt > 0 && r.n == r.failAt {
		return application.StreamPage{}, r.failErr
	}
	return r.inner.ReadStream(ctx, request)
}

type appendAfterFirstRead struct {
	t             *testing.T
	store         *memory.EventStore
	sessionID     domain.SessionID
	extra         domain.Event
	appended      bool
	sawPinnedHead bool
}

func (r *appendAfterFirstRead) ReadStream(ctx context.Context, request application.ReadStreamRequest) (application.StreamPage, error) {
	if request.HeadVersion != nil {
		r.sawPinnedHead = true
	}
	page, err := r.store.ReadStream(ctx, request)
	if err != nil {
		return application.StreamPage{}, err
	}
	if !r.appended {
		r.appended = true
		appendEvents(r.t, r.store, r.sessionID, r.extra)
	}
	return page, nil
}

type shortWriter struct {
	buf     bytes.Buffer
	writes  int
	shortOn int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.shortOn {
		if len(p) == 0 {
			return 0, nil
		}
		n := len(p) - 1
		w.buf.Write(p[:n])
		return n, nil
	}
	return w.buf.Write(p)
}

type cancelAfterWrite struct {
	buf    bytes.Buffer
	cancel context.CancelFunc
	writes int
}

func (w *cancelAfterWrite) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	w.writes++
	if w.writes == 1 && w.cancel != nil {
		w.cancel()
	}
	return n, err
}

func newExportStore(t *testing.T) *memory.EventStore {
	t.Helper()
	store, err := memory.NewEventStore(exportAuthority)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func appendEvents(t *testing.T, store *memory.EventStore, sessionID domain.SessionID, events ...domain.Event) {
	t.Helper()
	page, err := store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: sessionID, Limit: 256})
	if err != nil {
		t.Fatalf("ReadStream() error = %v", err)
	}
	version := page.HeadVersion
	for i, event := range events {
		seq := version + uint64(i) + 1
		request := application.AppendRequest{
			AppendID:        domain.AppendID(fmt.Sprintf("append-%s-%d", sessionID, seq)),
			SessionID:       sessionID,
			ExpectedVersion: version + uint64(i),
			CommandID:       domain.CommandID(fmt.Sprintf("command-%s-%d", sessionID, seq)),
			Authority:       exportAuthority,
			Events: []application.ProposedEvent{{
				ID:            domain.EventID(fmt.Sprintf("event-%s-%d", sessionID, seq)),
				SchemaVersion: 1,
				OccurredAt:    exportNow,
				Event:         event,
			}},
		}
		if _, err := store.Append(context.Background(), request); err != nil {
			t.Fatalf("Append(%d) error = %v", seq, err)
		}
	}
}

func recordedEvents(sessionID domain.SessionID, events ...domain.Event) []domain.RecordedEvent {
	records := make([]domain.RecordedEvent, len(events))
	for i, event := range events {
		seq := uint64(i + 1)
		records[i] = domain.RecordedEvent{
			SchemaVersion: 1,
			ID:            domain.EventID(fmt.Sprintf("event-%d", seq)),
			CommandID:     domain.CommandID(fmt.Sprintf("command-%d", seq)),
			SessionID:     sessionID,
			Sequence:      seq,
			OccurredAt:    exportNow,
			Event:         event,
		}
	}
	return records
}

func idleTurnEvents() []domain.Event {
	return []domain.Event{
		domain.SessionCreated{WorkspaceRoot: "/workspace"},
		domain.TurnStarted{TurnID: "turn-1", Input: "inspect"},
		domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"},
		domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "done"},
		domain.TurnCompleted{TurnID: "turn-1"},
	}
}

func twoStepHistory(withUsage bool) []domain.Event {
	offer1 := domain.ToolCallOffer{ID: "call-1", Name: "read_file", Arguments: `{"path":"a.txt"}`}
	offer2 := domain.ToolCallOffer{ID: "call-2", Name: "read_file", Arguments: `{"path":"b.txt"}`}
	events := []domain.Event{
		domain.SessionCreated{WorkspaceRoot: "/workspace"},
		domain.TurnStarted{TurnID: "turn-1", Input: "read files"},
		domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-1"},
	}
	if withUsage {
		events = append(events, domain.ModelRequestRecorded{
			TurnID:        "turn-1",
			ItemID:        "item-1",
			AdapterFamily: "openai-compat",
			ModelID:       "test",
			Messages:      []domain.ModelPromptMessage{{Role: domain.PromptRoleUser, Text: "read files"}},
		}, domain.ModelUsageRecorded{
			TurnID: "turn-1", ItemID: "item-1", InputTokens: 3, OutputTokens: 5, FinishReason: domain.FinishReasonToolCalls,
		})
	}
	events = append(events,
		domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: "reading a", ToolCalls: []domain.ToolCallOffer{offer1}},
		domain.ToolCallStarted{TurnID: "turn-1", ItemID: "item-2", CallID: "call-1", Name: "read_file", Arguments: `{"path":"a.txt"}`, StepIndex: 1},
		domain.ToolCallCompleted{TurnID: "turn-1", ItemID: "item-2", CallID: "call-1", Content: "a", Truncated: false},
		domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-3"},
		domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-3", Text: "reading b", ToolCalls: []domain.ToolCallOffer{offer2}},
		domain.ToolCallStarted{TurnID: "turn-1", ItemID: "item-4", CallID: "call-2", Name: "read_file", Arguments: `{"path":"b.txt"}`, StepIndex: 2},
		domain.ToolCallCompleted{TurnID: "turn-1", ItemID: "item-4", CallID: "call-2", Content: "b", Truncated: false},
		domain.AssistantMessageStarted{TurnID: "turn-1", ItemID: "item-5"},
		domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-5", Text: "done"},
		domain.TurnCompleted{TurnID: "turn-1"},
	)
	return events
}

func mustAcceptExport(t *testing.T, data []byte) (snapshotPayload, completePayload, []Line) {
	t.Helper()
	snapshot, complete, facts, err := consumerAccepts(data)
	if err != nil {
		t.Fatalf("consumer rejected export: %v\n%s", err, data)
	}
	return snapshot, complete, facts
}

func assertRejectedWithoutComplete(t *testing.T, data []byte) {
	t.Helper()
	if bytes.Contains(data, []byte(`"type":"transcript.complete"`)) {
		t.Fatalf("wrote complete line: %s", data)
	}
	if _, _, _, err := consumerAccepts(data); err == nil {
		t.Fatal("consumer accepted truncated transcript")
	}
}

func consumerAccepts(data []byte) (snapshotPayload, completePayload, []Line, error) {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return snapshotPayload{}, completePayload{}, nil, errors.New("transcript must end with a complete newline")
	}
	rawLines := bytes.Split(data[:len(data)-1], []byte{'\n'})
	if len(rawLines) < 2 {
		return snapshotPayload{}, completePayload{}, nil, errors.New("transcript missing snapshot or complete")
	}
	first, err := UnmarshalLine(rawLines[0])
	if err != nil || first.Snapshot == nil {
		return snapshotPayload{}, completePayload{}, nil, errors.New("first line is not transcript.snapshot")
	}
	last, err := UnmarshalLine(rawLines[len(rawLines)-1])
	if err != nil || last.Complete == nil {
		return snapshotPayload{}, completePayload{}, nil, errors.New("last line is not transcript.complete")
	}
	var snapshot snapshotPayload
	if err := json.Unmarshal(first.Snapshot.Payload, &snapshot); err != nil {
		return snapshotPayload{}, completePayload{}, nil, err
	}
	var complete completePayload
	if err := json.Unmarshal(last.Complete.Payload, &complete); err != nil {
		return snapshotPayload{}, completePayload{}, nil, err
	}
	if complete.HeadSequence != snapshot.HeadSequence {
		return snapshotPayload{}, completePayload{}, nil, fmt.Errorf("complete.headSequence %d != snapshot.headSequence %d", complete.HeadSequence, snapshot.HeadSequence)
	}
	factRaw := rawLines[1 : len(rawLines)-1]
	if uint64(len(factRaw)) != complete.FactLines {
		return snapshotPayload{}, completePayload{}, nil, fmt.Errorf("fact lines %d != complete.factLines %d", len(factRaw), complete.FactLines)
	}
	facts := make([]Line, 0, len(factRaw))
	for _, raw := range factRaw {
		decoded, err := UnmarshalLine(raw)
		if err != nil || decoded.Line == nil {
			return snapshotPayload{}, completePayload{}, nil, errors.New("intervening line is not a fact")
		}
		facts = append(facts, *decoded.Line)
	}
	return snapshot, complete, facts, nil
}
