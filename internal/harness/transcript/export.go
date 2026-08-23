package transcript

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

const streamPageLimit uint32 = 256

// StreamReader is the read-only EventStore surface WriteSession pages against.
type StreamReader interface {
	ReadStream(context.Context, application.ReadStreamRequest) (application.StreamPage, error)
}

// Result is the successful export diagnostic. It is returned only after the
// complete trailer is written.
type Result struct {
	HeadSequence uint64
	FactLines    uint64
	Open         bool
	Running      bool
}

type pinnedSnapshot struct {
	headSequence uint64
	factLines    uint64
	open         bool
	running      bool
}

func WriteSession(ctx context.Context, src StreamReader, sessionID domain.SessionID, now time.Time, w io.Writer) (Result, error) {
	if err := contextErr(ctx); err != nil {
		return Result{}, err
	}
	if _, err := domain.ParseSessionID(string(sessionID)); err != nil {
		return Result{}, &Error{Code: CodeInvalidSessionID, Message: "invalid session id"}
	}
	if src == nil {
		return Result{}, invalidRead(sessionID, "stream reader is required")
	}

	snapshot, err := inspectPinned(ctx, src, sessionID)
	if err != nil {
		return Result{}, err
	}
	if err := writePinned(ctx, src, sessionID, now, w, snapshot); err != nil {
		return Result{}, err
	}
	return Result{
		HeadSequence: snapshot.headSequence,
		FactLines:    snapshot.factLines,
		Open:         snapshot.open,
		Running:      snapshot.running,
	}, nil
}

func inspectPinned(ctx context.Context, src StreamReader, sessionID domain.SessionID) (pinnedSnapshot, error) {
	var state domain.Session
	var factLines uint64
	// Discarded after this pass so the write pass starts stepIndex at 1.
	steps := make(map[domain.TurnID]uint32)
	head, err := forEachPinnedPage(ctx, src, sessionID, nil, func(page application.StreamPage, cursor uint64) error {
		for index, record := range page.Records {
			if err := contextErr(ctx); err != nil {
				return err
			}
			expected := cursor + uint64(index) + 1
			if err := failClosedRecord(sessionID, record, expected, page.HeadVersion); err != nil {
				return err
			}
			applied, applyErr := domain.Apply(state, record)
			if applyErr != nil {
				return applyErr
			}
			state = applied
			_, ok, projectErr := ProjectRecord(record, steps)
			if projectErr != nil {
				return projectErr
			}
			if ok {
				factLines++
			}
		}
		return nil
	})
	if err != nil {
		return pinnedSnapshot{}, err
	}
	return pinnedSnapshot{
		headSequence: head,
		factLines:    factLines,
		open:         state.Status == domain.SessionStatusActive,
		running:      state.ActiveTurn != nil,
	}, nil
}

func writePinned(ctx context.Context, src StreamReader, sessionID domain.SessionID, now time.Time, w io.Writer, snapshot pinnedSnapshot) error {
	occurredAt := formatTimestamp(now)
	snapshotPayload, err := json.Marshal(snapshotPayload{
		HeadSequence: snapshot.headSequence,
		Open:         snapshot.open,
		Running:      snapshot.running,
		Stability:    StabilityExperimental,
	})
	if err != nil {
		return invalidLine("invalid snapshot payload")
	}
	encodedSnapshot, err := MarshalSnapshot(SnapshotLine{
		FormatVersion: FormatVersion,
		Schema:        Schema,
		SessionID:     string(sessionID),
		OccurredAt:    occurredAt,
		Type:          TypeSnapshot,
		Payload:       snapshotPayload,
	})
	if err != nil {
		return err
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := writeJSONL(w, encodedSnapshot); err != nil {
		return err
	}
	// Snapshot is on the wire: later failures must not emit transcript.complete.

	if err := contextErr(ctx); err != nil {
		return err
	}

	head := snapshot.headSequence
	steps := make(map[domain.TurnID]uint32)
	if _, err := forEachPinnedPage(ctx, src, sessionID, &head, func(page application.StreamPage, cursor uint64) error {
		for index, record := range page.Records {
			if err := contextErr(ctx); err != nil {
				return err
			}
			expected := cursor + uint64(index) + 1
			if record.SessionID != sessionID || record.Sequence != expected || record.Sequence > page.HeadVersion {
				return invalidRead(sessionID, "invalid record on write pass")
			}
			line, ok, projectErr := ProjectRecord(record, steps)
			if projectErr != nil {
				return projectErr
			}
			if !ok {
				continue
			}
			encoded, marshalErr := MarshalLine(line)
			if marshalErr != nil {
				return marshalErr
			}
			if err := writeJSONL(w, encoded); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if err := contextErr(ctx); err != nil {
		return err
	}
	completePayload, err := json.Marshal(completePayload{
		HeadSequence: snapshot.headSequence,
		FactLines:    snapshot.factLines,
		Open:         snapshot.open,
		Running:      snapshot.running,
	})
	if err != nil {
		return invalidLine("invalid complete payload")
	}
	encodedComplete, err := MarshalComplete(CompleteLine{
		FormatVersion: FormatVersion,
		Schema:        Schema,
		SessionID:     string(sessionID),
		OccurredAt:    occurredAt,
		Type:          TypeComplete,
		Payload:       completePayload,
	})
	if err != nil {
		return err
	}
	return writeJSONL(w, encodedComplete)
}

func forEachPinnedPage(ctx context.Context, src StreamReader, sessionID domain.SessionID, head *uint64, fn func(page application.StreamPage, cursor uint64) error) (uint64, error) {
	var cursor uint64
	var pinned uint64
	hasHead := head != nil
	if hasHead {
		pinned = *head
	}
	for {
		if err := contextErr(ctx); err != nil {
			return 0, err
		}
		var requestedHead *uint64
		if hasHead {
			value := pinned
			requestedHead = &value
		}
		page, err := src.ReadStream(ctx, application.ReadStreamRequest{
			SessionID:     sessionID,
			AfterSequence: cursor,
			Limit:         streamPageLimit,
			HeadVersion:   requestedHead,
		})
		if err != nil {
			return 0, err
		}
		if err := contextErr(ctx); err != nil {
			return 0, err
		}
		if uint32(len(page.Records)) > streamPageLimit {
			return 0, invalidRead(sessionID, "pinned page exceeds limit")
		}
		if !hasHead {
			if len(page.Records) == 0 && page.HeadVersion == 0 {
				return 0, &Error{Code: CodeSessionNotFound, Message: "session not found"}
			}
			if (len(page.Records) == 0) != (page.HeadVersion == 0) {
				return 0, invalidRead(sessionID, "empty first page does not match head")
			}
			pinned = page.HeadVersion
			hasHead = true
		} else if page.HeadVersion != pinned {
			return 0, invalidRead(sessionID, "pinned head changed")
		}
		if page.HeadVersion < cursor {
			return 0, invalidRead(sessionID, "invalid pinned page bounds")
		}
		if err := fn(page, cursor); err != nil {
			return 0, err
		}
		next := cursor
		if len(page.Records) > 0 {
			next = page.Records[len(page.Records)-1].Sequence
		}
		if page.NextAfterSequence != next || page.End != (next == page.HeadVersion) {
			return 0, invalidRead(sessionID, "invalid pinned page cursor or end")
		}
		if page.End {
			return pinned, nil
		}
		if next == cursor {
			return 0, invalidRead(sessionID, "non-terminal page made no progress")
		}
		cursor = next
	}
}

func failClosedRecord(sessionID domain.SessionID, record domain.RecordedEvent, expected, head uint64) error {
	if record.SessionID != sessionID || record.Sequence != expected || record.Sequence > head {
		return invalidRead(sessionID, "invalid record")
	}
	if record.SchemaVersion != 1 {
		return &Error{Code: CodeUnsupportedSchemaVersion, Message: "unsupported schema version"}
	}
	if _, err := domain.MarshalRecordedEvent(record); err != nil {
		return failClosedCanonical(err)
	}
	return nil
}

func failClosedCanonical(err error) error {
	if err == nil {
		return nil
	}
	if domain.IsCode(err, domain.CodeInvalidEvent) && err.Error() == string(domain.CodeInvalidEvent)+": unsupported event type" {
		return &Error{Code: CodeUnsupportedEventType, Message: "unsupported event type"}
	}
	if domain.IsCode(err, domain.CodeInvalidEvent) && err.Error() == string(domain.CodeInvalidEvent)+": unsupported schema version" {
		return &Error{Code: CodeUnsupportedSchemaVersion, Message: "unsupported schema version"}
	}
	return err
}

func writeJSONL(w io.Writer, encoded []byte) error {
	if w == nil {
		return errors.New("writer is required")
	}
	line := make([]byte, len(encoded)+1)
	copy(line, encoded)
	line[len(encoded)] = '\n'
	_, err := w.Write(line)
	return err
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	return ctx.Err()
}

func invalidRead(sessionID domain.SessionID, message string) error {
	err, buildErr := application.NewStoreError(application.StoreError{
		Code:      application.StoreCodeInvalidRead,
		SessionID: sessionID,
		Cause:     fmt.Errorf("%s", message),
	})
	if buildErr != nil {
		return buildErr
	}
	return err
}
