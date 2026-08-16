package sqlite

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

var errInvalidPinnedCursor = errors.New("invalid pinned cursor")

func readRejected(session domain.SessionID, cause error) error {
	return newStoreError(application.StoreCodeInvalidRead, session, cause)
}

func wrapDetail(detail string, cause error) error {
	if cause == nil {
		return errors.New(detail)
	}
	return fmt.Errorf("%s: %w", detail, cause)
}

// ResolveAppend is a read-only receipt lookup by AppendID plus request
// digest. Inability to perform the lookup is an error, never NotFound.
func (store *Store) ResolveAppend(ctx context.Context, request application.ResolveAppendRequest) (application.AppendResolution, error) {
	if err := contextError(ctx); err != nil {
		return application.AppendResolution{}, appendRejected("", err)
	}
	if _, err := domain.ParseAppendID(string(request.AppendID)); err != nil {
		return application.AppendResolution{}, appendRejected("", err)
	}

	var storedDigest []byte
	var storedPosition, storedFirst, storedLast uint64
	var storedSession string
	err := store.db.QueryRowContext(ctx,
		"SELECT request_digest, commit_position, first_sequence, last_sequence, session_id FROM event_appends WHERE append_id = ?",
		string(request.AppendID)).Scan(&storedDigest, &storedPosition, &storedFirst, &storedLast, &storedSession)
	switch {
	case isNoRows(err):
		return application.AppendResolution{Kind: application.AppendResolutionNotFound}, nil
	case err != nil:
		return application.AppendResolution{}, mapStorageError(err, "")
	}
	if !bytes.Equal(storedDigest, request.RequestDigest[:]) {
		return application.AppendResolution{Kind: application.AppendResolutionIdentityMismatch}, nil
	}
	if storedPosition == 0 || storedFirst == 0 || storedLast < storedFirst {
		return application.AppendResolution{}, newStoreError(application.StoreCodeCorrupt, domain.SessionID(storedSession),
			wrapDetail(fmt.Sprintf("stored receipt for append %q is inconsistent", request.AppendID), nil))
	}
	receipt := application.CommitReceipt{
		AppendID:       request.AppendID,
		CommitPosition: storedPosition,
		FirstSequence:  storedFirst,
		LastSequence:   storedLast,
	}
	if err := receiptIdentityCheck(receipt, domain.SessionID(storedSession)); err != nil {
		return application.AppendResolution{}, err
	}
	return application.AppendResolution{Kind: application.AppendResolutionCommitted, Receipt: &receipt}, nil
}

// FindCommandRequest compares Session and digest together; an existing
// request ID with either mismatch returns IdentityMismatch without revealing
// another Session's record.
func (store *Store) FindCommandRequest(ctx context.Context, request application.FindCommandRequestRequest) (application.CommandRequestLookup, error) {
	if err := contextError(ctx); err != nil {
		return application.CommandRequestLookup{}, appendRejected(request.SessionID, err)
	}
	if _, err := domain.ParseRunTurnRequestID(string(request.RunTurnRequestID)); err != nil {
		return application.CommandRequestLookup{}, appendRejected(request.SessionID, err)
	}
	if _, err := domain.ParseSessionID(string(request.SessionID)); err != nil {
		return application.CommandRequestLookup{}, appendRejected(request.SessionID, err)
	}

	var record application.CommandRequestRecord
	var sessionID, commandID, turnID, itemID, admissionAppendID string
	var requestDigest []byte
	err := store.db.QueryRowContext(ctx,
		"SELECT run_turn_request_id, request_digest, session_id, command_id, turn_id, item_id, admission_append_id FROM command_requests WHERE run_turn_request_id = ?",
		string(request.RunTurnRequestID)).Scan(&record.RunTurnRequestID, &requestDigest, &sessionID, &commandID, &turnID, &itemID, &admissionAppendID)
	switch {
	case isNoRows(err):
		return application.CommandRequestLookup{Kind: application.CommandRequestLookupNotFound}, nil
	case err != nil:
		return application.CommandRequestLookup{}, mapStorageError(err, request.SessionID)
	}
	if sessionID != string(request.SessionID) || !bytes.Equal(requestDigest, request.RequestDigest[:]) {
		return application.CommandRequestLookup{Kind: application.CommandRequestLookupIdentityMismatch}, nil
	}
	if len(requestDigest) != len(request.RequestDigest) {
		return application.CommandRequestLookup{}, newStoreError(application.StoreCodeCorrupt, request.SessionID,
			wrapDetail("stored request digest has unexpected length", nil))
	}
	copy(record.RequestDigest[:], requestDigest)
	record.SessionID = domain.SessionID(sessionID)
	record.CommandID = domain.CommandID(commandID)
	record.TurnID = domain.TurnID(turnID)
	record.ItemID = domain.ItemID(itemID)
	record.AdmissionAppendID = domain.AppendID(admissionAppendID)
	return application.CommandRequestLookup{Kind: application.CommandRequestLookupFound, Record: &record}, nil
}

func receiptIdentityCheck(receipt application.CommitReceipt, session domain.SessionID) error {
	if _, err := domain.ParseSessionID(string(session)); err != nil {
		return newStoreError(application.StoreCodeCorrupt, session, wrapDetail("receipt session identity invalid", err))
	}
	return nil
}
