package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// Digest is the fixed-size SHA-256 value used to identify immutable requests.
type Digest [sha256.Size]byte

func (digest Digest) String() string { return hex.EncodeToString(digest[:]) }

func (digest Digest) MarshalText() ([]byte, error) { return []byte(digest.String()), nil }

// RuntimeID identifies an application/storage runtime rather than a domain
// aggregate.
type RuntimeID string

func ParseRuntimeID(value string) (RuntimeID, error) {
	if value == "" || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return "", fmt.Errorf("runtime ID must be valid UTF-8 and not blank or padded")
	}
	return RuntimeID(value), nil
}

type WriterAuthority struct {
	RuntimeID    RuntimeID
	FencingToken uint64
}

func (authority WriterAuthority) Validate() error {
	if _, err := ParseRuntimeID(string(authority.RuntimeID)); err != nil {
		return err
	}
	if authority.FencingToken == 0 {
		return fmt.Errorf("fencing token must be greater than zero")
	}
	return nil
}

// EventStoreV2 is the v2 event-stream boundary. It remains distinct from the
// existing v1 EventStore while the migration is in progress.
type EventStoreV2 interface {
	ReadStream(context.Context, ReadStreamRequest) (StreamPage, error)
	Append(context.Context, AppendRequestV2) (CommitReceipt, error)
	ResolveAppend(context.Context, ResolveAppendRequest) (AppendResolution, error)
	FindCommandRequest(context.Context, FindCommandRequestRequest) (CommandRequestLookup, error)
}

type ReadStreamRequest struct {
	SessionID     domain.SessionID
	AfterSequence uint64
	Limit         uint32
	HeadVersion   *uint64
}

type StreamPage struct {
	Records           []domain.RecordedEvent
	HeadVersion       uint64
	NextAfterSequence uint64
	End               bool
}

type AppendRequestV2 struct {
	AppendID        domain.AppendID
	SessionID       domain.SessionID
	ExpectedVersion uint64
	CommandID       domain.CommandID
	Authority       WriterAuthority
	Admission       *CommandAdmission
	Events          []ProposedEvent
}

type ProposedEvent struct {
	ID            domain.EventID
	SchemaVersion uint32
	OccurredAt    time.Time
	Event         domain.Event
}

type CommandAdmission struct {
	RunTurnRequestID domain.RunTurnRequestID
	RequestDigest    Digest
	TurnID           domain.TurnID
	ItemID           domain.ItemID
}

type CommitReceipt struct {
	AppendID       domain.AppendID
	CommitPosition uint64
	FirstSequence  uint64
	LastSequence   uint64
}

type ResolveAppendRequest struct {
	AppendID      domain.AppendID
	RequestDigest Digest
}

type AppendResolutionKind string

const (
	AppendResolutionCommitted        AppendResolutionKind = "committed"
	AppendResolutionNotFound         AppendResolutionKind = "not_found"
	AppendResolutionIdentityMismatch AppendResolutionKind = "identity_mismatch"
)

type AppendResolution struct {
	Kind    AppendResolutionKind
	Receipt *CommitReceipt
}

func (resolution AppendResolution) Validate() error {
	switch resolution.Kind {
	case AppendResolutionCommitted:
		if resolution.Receipt == nil {
			return fmt.Errorf("committed append resolution requires receipt")
		}
	case AppendResolutionNotFound, AppendResolutionIdentityMismatch:
		if resolution.Receipt != nil {
			return fmt.Errorf("%s append resolution must not include receipt", resolution.Kind)
		}
	default:
		return fmt.Errorf("unknown append resolution kind %q", resolution.Kind)
	}
	return nil
}

type FindCommandRequestRequest struct {
	RunTurnRequestID domain.RunTurnRequestID
	SessionID        domain.SessionID
	RequestDigest    Digest
}

type CommandRequestRecord struct {
	RunTurnRequestID  domain.RunTurnRequestID
	RequestDigest     Digest
	SessionID         domain.SessionID
	CommandID         domain.CommandID
	TurnID            domain.TurnID
	ItemID            domain.ItemID
	AdmissionAppendID domain.AppendID
}

type CommandRequestLookupKind string

const (
	CommandRequestLookupFound            CommandRequestLookupKind = "found"
	CommandRequestLookupNotFound         CommandRequestLookupKind = "not_found"
	CommandRequestLookupIdentityMismatch CommandRequestLookupKind = "identity_mismatch"
)

type CommandRequestLookup struct {
	Kind   CommandRequestLookupKind
	Record *CommandRequestRecord
}

func (lookup CommandRequestLookup) Validate() error {
	switch lookup.Kind {
	case CommandRequestLookupFound:
		if lookup.Record == nil {
			return fmt.Errorf("found command request lookup requires record")
		}
	case CommandRequestLookupNotFound, CommandRequestLookupIdentityMismatch:
		if lookup.Record != nil {
			return fmt.Errorf("%s command request lookup must not include record", lookup.Kind)
		}
	default:
		return fmt.Errorf("unknown command request lookup kind %q", lookup.Kind)
	}
	return nil
}
