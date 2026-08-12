// Package memory provides deterministic in-memory adapters for the harness.
package memory

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

const recordedEventSchemaVersion = 1

// EventStore is an atomic, mutex-protected application.EventStore.
type EventStore struct {
	mu sync.Mutex

	clock application.Clock
	ids   application.IDGenerator

	records      map[domain.SessionID][]domain.RecordedEvent
	loadFaults   map[domain.SessionID]error
	appendFaults map[domain.SessionID]error
}

var _ application.EventStore = (*EventStore)(nil)

// NewEventStore constructs an empty EventStore.
func NewEventStore(clock application.Clock, ids application.IDGenerator) (*EventStore, error) {
	if isNil(clock) {
		return nil, persistenceError("event_store_clock_required", errors.New("clock is required"))
	}
	if isNil(ids) {
		return nil, persistenceError("event_store_id_generator_required", errors.New("ID generator is required"))
	}
	return &EventStore{
		clock:        clock,
		ids:          ids,
		records:      make(map[domain.SessionID][]domain.RecordedEvent),
		loadFaults:   make(map[domain.SessionID]error),
		appendFaults: make(map[domain.SessionID]error),
	}, nil
}

// FailNextLoad arranges one deterministic pre-read failure for sessionID.
func (store *EventStore) FailNextLoad(sessionID domain.SessionID, err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if isNil(err) {
		delete(store.loadFaults, sessionID)
		return
	}
	store.loadFaults[sessionID] = err
}

// FailNextAppend arranges one deterministic pre-commit failure for sessionID.
func (store *EventStore) FailNextAppend(sessionID domain.SessionID, err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if isNil(err) {
		delete(store.appendFaults, sessionID)
		return
	}
	store.appendFaults[sessionID] = err
}

// Load returns the complete authoritative Session stream as a defensive copy.
func (store *EventStore) Load(ctx context.Context, sessionID domain.SessionID) ([]domain.RecordedEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, persistenceError("event_store_load_canceled", err)
	}
	if _, err := domain.ParseSessionID(string(sessionID)); err != nil {
		return nil, persistenceError("event_store_invalid_session_id", err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, persistenceError("event_store_load_canceled", err)
	}
	if cause, exists := store.loadFaults[sessionID]; exists {
		delete(store.loadFaults, sessionID)
		return nil, persistenceError("event_store_load_failed", cause)
	}
	records, err := domain.CloneRecordedEvents(store.records[sessionID])
	if err != nil {
		return nil, persistenceError("event_store_clone_failed", err)
	}
	return records, nil
}

// Append validates, records, replays, and atomically commits one ordered batch.
func (store *EventStore) Append(ctx context.Context, request application.AppendRequest) ([]domain.RecordedEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, persistenceError("event_store_append_canceled", err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, persistenceError("event_store_append_canceled", err)
	}
	if _, err := domain.ParseSessionID(string(request.SessionID)); err != nil {
		return nil, persistenceError("event_store_invalid_session_id", err)
	}
	if _, err := domain.ParseCommandID(string(request.CommandID)); err != nil {
		return nil, persistenceError("event_store_invalid_command_id", err)
	}
	if len(request.Events) == 0 {
		return nil, persistenceError("event_store_empty_batch", errors.New("append requires at least one event"))
	}

	current := store.records[request.SessionID]
	actualVersion := uint64(len(current))
	if actualVersion != request.ExpectedVersion {
		return nil, &application.VersionConflictError{
			SessionID:       request.SessionID,
			ExpectedVersion: request.ExpectedVersion,
			ActualVersion:   actualVersion,
		}
	}
	if cause, exists := store.appendFaults[request.SessionID]; exists {
		delete(store.appendFaults, request.SessionID)
		return nil, persistenceError("event_store_append_failed", cause)
	}

	events := make([]domain.Event, len(request.Events))
	for index, event := range request.Events {
		cloned, err := domain.CloneEvent(event)
		if err != nil {
			return nil, persistenceError("event_store_invalid_event", err)
		}
		if err := validateEvent(cloned); err != nil {
			return nil, persistenceError("event_store_invalid_event", err)
		}
		events[index] = cloned
	}

	occurredAt := store.clock.Now().UTC()
	if err := validateTimestamp(occurredAt); err != nil {
		return nil, persistenceError("event_store_invalid_clock", err)
	}

	seenIDs := make(map[domain.EventID]struct{}, len(current)+len(events))
	for _, record := range current {
		seenIDs[record.ID] = struct{}{}
	}
	batch := make([]domain.RecordedEvent, len(events))
	for index, event := range events {
		eventID, err := store.ids.NewEventID()
		if err != nil {
			return nil, persistenceError("event_store_event_id_failed", err)
		}
		if _, err := domain.ParseEventID(string(eventID)); err != nil {
			return nil, persistenceError("event_store_invalid_event_id", err)
		}
		if _, duplicate := seenIDs[eventID]; duplicate {
			return nil, persistenceError("event_store_duplicate_event_id", errors.New("event ID must be unique"))
		}
		seenIDs[eventID] = struct{}{}
		batch[index] = domain.RecordedEvent{
			SchemaVersion: recordedEventSchemaVersion,
			ID:            eventID,
			CommandID:     request.CommandID,
			SessionID:     request.SessionID,
			Sequence:      actualVersion + uint64(index) + 1,
			OccurredAt:    occurredAt,
			Event:         event,
		}
	}

	candidate := make([]domain.RecordedEvent, 0, len(current)+len(batch))
	candidate = append(candidate, current...)
	candidate = append(candidate, batch...)
	if _, err := domain.Replay(candidate); err != nil {
		return nil, persistenceError("event_store_replay_failed", err)
	}
	returned, err := domain.CloneRecordedEvents(batch)
	if err != nil {
		return nil, persistenceError("event_store_clone_failed", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, persistenceError("event_store_append_canceled", err)
	}
	store.records[request.SessionID] = candidate
	return returned, nil
}

func validateEvent(event domain.Event) error {
	_, err := domain.MarshalRecordedEvent(domain.RecordedEvent{
		SchemaVersion: recordedEventSchemaVersion,
		ID:            "event-validation",
		CommandID:     "command-validation",
		SessionID:     "session-validation",
		Sequence:      1,
		OccurredAt:    time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		Event:         event,
	})
	return err
}

func validateTimestamp(timestamp time.Time) error {
	_, err := domain.MarshalRecordedEvent(domain.RecordedEvent{
		SchemaVersion: recordedEventSchemaVersion,
		ID:            "event-validation",
		CommandID:     "command-validation",
		SessionID:     "session-validation",
		Sequence:      1,
		OccurredAt:    timestamp,
		Event:         domain.SessionCreated{WorkspaceRoot: "/validation"},
	})
	return err
}

func persistenceError(code string, cause error) error {
	return &application.Error{
		Category: application.CategoryPersistence,
		Code:     code,
		Cause:    cause,
	}
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
