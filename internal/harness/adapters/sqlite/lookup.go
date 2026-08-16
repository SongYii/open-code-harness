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
	if cause := store.popFault(faultResolve); cause != nil {
		return application.AppendResolution{}, newStoreError(application.StoreCodeUnavailable, "", cause)
	}

	receipt, found, err := store.lookupReceipt(ctx, store.db, request.AppendID, request.RequestDigest, "")
	switch {
	case err != nil:
		var mismatch *application.StoreError
		if errors.As(err, &mismatch) && mismatch.Code == application.StoreCodeAppendIdentityMismatch {
			return application.AppendResolution{Kind: application.AppendResolutionIdentityMismatch}, nil
		}
		return application.AppendResolution{}, err
	case !found:
		return application.AppendResolution{Kind: application.AppendResolutionNotFound}, nil
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
