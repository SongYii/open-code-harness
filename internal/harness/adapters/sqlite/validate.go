package sqlite

import (
	"crypto/sha256"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// identityReservation is one creation-event identity claimed by a batch.
type identityReservation struct {
	kind           string
	id             string
	introducingEvt domain.EventID
}

// preparedEvent carries the canonical record bytes alongside the proposed
// metadata so one immutable decision feeds both the digest and the inserts.
type preparedEvent struct {
	record        domain.RecordedEvent
	eventType     string
	payload       []byte
	payloadDigest [sha256.Size]byte
	reservations  []identityReservation
}

// preparedAppend is the canonicalized append batch: cloned request, computed
// digest, canonical record bytes, in-batch uniqueness, and creation-identity
// extraction. IDs, schema versions, UTC timestamps, and event-count and byte
// limits were already enforced by DigestAppendRequest before any storage
// state was observed.
type preparedAppend struct {
	request application.AppendRequest
	digest  application.Digest
	events  []preparedEvent
	turnIDs []domain.TurnID
	itemIDs []domain.ItemID
}

func prepareEvents(request application.AppendRequest, digest application.Digest, sessionVersion uint64) (*preparedAppend, error) {
	prepared := &preparedAppend{
		request: request,
		digest:  digest,
		events:  make([]preparedEvent, 0, len(request.Events)),
	}
	seenEventIDs := make(map[domain.EventID]struct{}, len(request.Events))
	seenTurns := make(map[domain.TurnID]struct{})
	seenItems := make(map[domain.ItemID]struct{})
	for index, proposed := range request.Events {
		if _, exists := seenEventIDs[proposed.ID]; exists {
			return nil, fmt.Errorf("duplicate event ID %q", proposed.ID)
		}
		seenEventIDs[proposed.ID] = struct{}{}

		record := domain.RecordedEvent{
			SchemaVersion: int(proposed.SchemaVersion),
			ID:            proposed.ID,
			CommandID:     request.CommandID,
			SessionID:     request.SessionID,
			Sequence:      sessionVersion + uint64(index) + 1,
			OccurredAt:    proposed.OccurredAt,
			Event:         proposed.Event,
		}
		payload, err := domain.MarshalRecordedEvent(record)
		if err != nil {
			return nil, fmt.Errorf("canonicalize event %d: %w", index, err)
		}
		eventType := record.Event.EventType()
		entry := preparedEvent{
			record:        record,
			eventType:     eventType,
			payload:       payload,
			payloadDigest: sha256.Sum256(payload),
		}
		switch event := proposed.Event.(type) {
		case domain.TurnStarted:
			if _, exists := seenTurns[event.TurnID]; exists {
				return nil, domainIdentityError(request.SessionID, "turn")
			}
			seenTurns[event.TurnID] = struct{}{}
			entry.reservations = append(entry.reservations,
				identityReservation{kind: "turn", id: string(event.TurnID), introducingEvt: proposed.ID})
		case domain.AssistantMessageStarted:
			if _, exists := seenItems[event.ItemID]; exists {
				return nil, domainIdentityError(request.SessionID, "item")
			}
			seenItems[event.ItemID] = struct{}{}
			entry.reservations = append(entry.reservations,
				identityReservation{kind: "item", id: string(event.ItemID), introducingEvt: proposed.ID})
		case domain.ToolCallStarted:
			if _, exists := seenItems[event.ItemID]; exists {
				return nil, domainIdentityError(request.SessionID, "item")
			}
			seenItems[event.ItemID] = struct{}{}
			entry.reservations = append(entry.reservations,
				identityReservation{kind: "item", id: string(event.ItemID), introducingEvt: proposed.ID})
		}
		prepared.events = append(prepared.events, entry)
	}
	for turnID := range seenTurns {
		prepared.turnIDs = append(prepared.turnIDs, turnID)
	}
	for itemID := range seenItems {
		prepared.itemIDs = append(prepared.itemIDs, itemID)
	}
	return prepared, nil
}

// cloneAppendRequest takes ownership of every event before authorization or
// storage state is observed, mirroring the reference adapter.
func cloneAppendRequest(request application.AppendRequest) (application.AppendRequest, error) {
	cloned := request
	if request.Admission != nil {
		admission := *request.Admission
		cloned.Admission = &admission
	}
	cloned.Events = make([]application.ProposedEvent, len(request.Events))
	for i, event := range request.Events {
		copy := event
		var err error
		copy.Event, err = domain.CloneEvent(event.Event)
		if err != nil {
			return application.AppendRequest{}, err
		}
		cloned.Events[i] = copy
	}
	return cloned, nil
}

func domainIdentityError(session domain.SessionID, kind string) error {
	storeErr, err := application.NewStoreError(application.StoreError{
		Code:         application.StoreCodeDomainIdentityConflict,
		SessionID:    session,
		IdentityKind: kind,
	})
	if err != nil {
		return err
	}
	return storeErr
}

func containsTurnID(ids []domain.TurnID, want domain.TurnID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func containsItemID(ids []domain.ItemID, want domain.ItemID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
