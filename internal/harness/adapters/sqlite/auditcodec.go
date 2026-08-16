package sqlite

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// auditFormatVersionV1 is the frozen audit codec version introduced with
// Slice 3. A codec for any committed format cannot be removed from a
// supported upgrade path.
const auditFormatVersionV1 uint32 = 1

// auditGenesisDigest seeds the audit hash chain. Derivation is frozen:
// SHA-256 of "open-code-harness/audit-chain/genesis/v1".
var auditGenesisDigest = sha256.Sum256([]byte("open-code-harness/audit-chain/genesis/v1"))

// auditBatch is the codec-neutral fact set of one atomic append.
type auditBatch struct {
	FormatVersion   uint32
	CommitPosition  uint64
	AppendID        string
	CommandID       string
	SessionID       string
	ExpectedVersion uint64
	FirstSequence   uint64
	LastSequence    uint64
	CommittedAtUnix float64
	PreviousDigest  [sha256.Size]byte
	BatchDigest     [sha256.Size]byte
	Events          [][]byte
}

// auditEnvelopeV1 is the canonical JSON line for one append. Field order is
// frozen by struct declaration order and must never change for format
// version 1.
type auditEnvelopeV1 struct {
	FormatVersion   uint32            `json:"formatVersion"`
	CommitPosition  uint64            `json:"commitPosition"`
	AppendID        string            `json:"appendId"`
	CommandID       string            `json:"commandId"`
	SessionID       string            `json:"sessionId"`
	ExpectedVersion uint64            `json:"expectedVersion"`
	FirstSequence   uint64            `json:"firstSequence"`
	LastSequence    uint64            `json:"lastSequence"`
	CommittedAt     string            `json:"committedAt"`
	PreviousDigest  string            `json:"previousDigest"`
	Events          []json.RawMessage `json:"events"`
	BatchDigest     string            `json:"batchDigest"`
}

// auditEnvelopeV1Unsigned omits the digest for digest computation. Its field
// order must mirror auditEnvelopeV1 exactly.
type auditEnvelopeV1Unsigned struct {
	FormatVersion   uint32            `json:"formatVersion"`
	CommitPosition  uint64            `json:"commitPosition"`
	AppendID        string            `json:"appendId"`
	CommandID       string            `json:"commandId"`
	SessionID       string            `json:"sessionId"`
	ExpectedVersion uint64            `json:"expectedVersion"`
	FirstSequence   uint64            `json:"firstSequence"`
	LastSequence    uint64            `json:"lastSequence"`
	CommittedAt     string            `json:"committedAt"`
	PreviousDigest  string            `json:"previousDigest"`
	Events          []json.RawMessage `json:"events"`
}

type auditCodecV1 struct{}

func (auditCodecV1) FormatVersion() uint32 { return auditFormatVersionV1 }

func (auditCodecV1) Encode(batch auditBatch) ([]byte, [sha256.Size]byte, error) {
	if batch.FormatVersion != auditFormatVersionV1 {
		return nil, [sha256.Size]byte{}, fmt.Errorf("audit codec v1 refuses format version %d", batch.FormatVersion)
	}
	unsigned := auditEnvelopeV1Unsigned{
		FormatVersion:   batch.FormatVersion,
		CommitPosition:  batch.CommitPosition,
		AppendID:        batch.AppendID,
		CommandID:       batch.CommandID,
		SessionID:       batch.SessionID,
		ExpectedVersion: batch.ExpectedVersion,
		FirstSequence:   batch.FirstSequence,
		LastSequence:    batch.LastSequence,
		CommittedAt:     formatAuditTimestamp(batch.CommittedAtUnix),
		PreviousDigest:  auditDigestString(batch.PreviousDigest),
		Events:          make([]json.RawMessage, len(batch.Events)),
	}
	for i, event := range batch.Events {
		if !json.Valid(event) {
			return nil, [sha256.Size]byte{}, fmt.Errorf("audit event %d is not valid JSON", i)
		}
		unsigned.Events[i] = json.RawMessage(event)
	}
	unsignedBytes, err := json.Marshal(unsigned)
	if err != nil {
		return nil, [sha256.Size]byte{}, fmt.Errorf("audit encode: %w", err)
	}
	digest := sha256.Sum256(unsignedBytes)
	envelope := auditEnvelopeV1{
		FormatVersion:   unsigned.FormatVersion,
		CommitPosition:  unsigned.CommitPosition,
		AppendID:        unsigned.AppendID,
		CommandID:       unsigned.CommandID,
		SessionID:       unsigned.SessionID,
		ExpectedVersion: unsigned.ExpectedVersion,
		FirstSequence:   unsigned.FirstSequence,
		LastSequence:    unsigned.LastSequence,
		CommittedAt:     unsigned.CommittedAt,
		PreviousDigest:  unsigned.PreviousDigest,
		Events:          unsigned.Events,
		BatchDigest:     auditDigestString(digest),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, [sha256.Size]byte{}, fmt.Errorf("audit encode: %w", err)
	}
	return encoded, digest, nil
}

func (auditCodecV1) Decode(envelope []byte) (auditBatch, error) {
	var decoded auditEnvelopeV1
	if err := json.Unmarshal(envelope, &decoded); err != nil {
		return auditBatch{}, fmt.Errorf("audit decode: %w", err)
	}
	if decoded.FormatVersion != auditFormatVersionV1 {
		return auditBatch{}, &CorruptError{Detail: fmt.Sprintf("audit envelope format version %d is not 1", decoded.FormatVersion)}
	}
	unsigned := auditEnvelopeV1Unsigned{
		FormatVersion:   decoded.FormatVersion,
		CommitPosition:  decoded.CommitPosition,
		AppendID:        decoded.AppendID,
		CommandID:       decoded.CommandID,
		SessionID:       decoded.SessionID,
		ExpectedVersion: decoded.ExpectedVersion,
		FirstSequence:   decoded.FirstSequence,
		LastSequence:    decoded.LastSequence,
		CommittedAt:     decoded.CommittedAt,
		PreviousDigest:  decoded.PreviousDigest,
		Events:          decoded.Events,
	}
	unsignedBytes, err := json.Marshal(unsigned)
	if err != nil {
		return auditBatch{}, fmt.Errorf("audit decode: %w", err)
	}
	digest := sha256.Sum256(unsignedBytes)
	if decoded.BatchDigest != auditDigestString(digest) {
		return auditBatch{}, &CorruptError{Detail: "audit envelope batch digest mismatch"}
	}
	batch := auditBatch{
		FormatVersion:   decoded.FormatVersion,
		CommitPosition:  decoded.CommitPosition,
		AppendID:        decoded.AppendID,
		CommandID:       decoded.CommandID,
		SessionID:       decoded.SessionID,
		ExpectedVersion: decoded.ExpectedVersion,
		FirstSequence:   decoded.FirstSequence,
		LastSequence:    decoded.LastSequence,
		Events:          make([][]byte, len(decoded.Events)),
	}
	parsed, err := time.Parse(time.RFC3339Nano, decoded.CommittedAt)
	if err != nil {
		return auditBatch{}, &CorruptError{Detail: "audit envelope committedAt is not RFC3339Nano"}
	}
	batch.CommittedAtUnix = float64(parsed.Unix()) + float64(parsed.Nanosecond())/1e9
	parsedDigest, err := parseAuditDigestString(decoded.BatchDigest)
	if err != nil {
		return auditBatch{}, &CorruptError{Detail: "audit envelope batchDigest is malformed"}
	}
	batch.BatchDigest = parsedDigest
	previous, err := parseAuditDigestString(decoded.PreviousDigest)
	if err != nil {
		return auditBatch{}, &CorruptError{Detail: "audit envelope previousDigest is malformed"}
	}
	batch.PreviousDigest = previous
	for i, event := range decoded.Events {
		batch.Events[i] = []byte(event)
	}
	return batch, nil
}

// auditCodecFor resolves the frozen codec registry by format version. A
// missing codec is corruption: export and import fail closed.
func auditCodecFor(formatVersion uint32) (interface {
	FormatVersion() uint32
	Encode(auditBatch) ([]byte, [sha256.Size]byte, error)
	Decode([]byte) (auditBatch, error)
}, error) {
	switch formatVersion {
	case auditFormatVersionV1:
		return auditCodecV1{}, nil
	default:
		return nil, &CorruptError{Detail: fmt.Sprintf("no audit codec for format version %d", formatVersion)}
	}
}

func auditDigestString(digest [sha256.Size]byte) string {
	return "sha256:" + fmt.Sprintf("%x", digest[:])
}

func parseAuditDigestString(value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if len(value) != 7+sha256.Size*2 || value[:7] != "sha256:" {
		return digest, fmt.Errorf("malformed audit digest %q", value)
	}
	for i := 0; i < sha256.Size; i++ {
		_, err := fmt.Sscanf(value[7+i*2:7+i*2+2], "%02x", &digest[i])
		if err != nil {
			return digest, err
		}
	}
	return digest, nil
}

// formatAuditTimestamp freezes the committedAt conversion from the stored
// subsecond unixepoch REAL to RFC3339Nano.
func formatAuditTimestamp(unixSeconds float64) string {
	seconds := int64(unixSeconds)
	nanoseconds := int64((unixSeconds - float64(seconds)) * 1e9)
	return time.Unix(seconds, nanoseconds).UTC().Format(time.RFC3339Nano)
}

func auditEventPayloadDigest(payload []byte) [sha256.Size]byte {
	return sha256.Sum256(payload)
}

var _ = bytes.Equal
