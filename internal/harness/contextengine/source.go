package contextengine

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// SourceSchemaVersion identifies the source-event filter and digest chain
// this package implements (design §7.2).
const SourceSchemaVersion = "och_context_source_v1"

// IsSourceEvent reports whether event counts toward coveredEventCount and
// the source digest chain (design §7.2): exactly the six event types the
// projection grammar (§9.1, projector.go) folds into a conversational
// message. Session, model request/usage, policy, approval, and context
// events never form conversational units and are excluded; so are the
// AssistantMessage lifecycle's Started/Failed/Interrupted variants and the
// terminal Turn events (TurnCompleted/Failed/Interrupted) — the latter
// close boundary eligibility (projector.go) but never themselves become a
// message, so they stay out of the digest that identifies message content.
func IsSourceEvent(event domain.Event) bool {
	switch event.(type) {
	case domain.TurnStarted,
		domain.AssistantMessageCompleted,
		domain.ToolCallStarted,
		domain.ToolCallCompleted,
		domain.ToolCallFailed,
		domain.ToolCallInterrupted:
		return true
	default:
		return false
	}
}

var sourceDigestSeed = sha256.Sum256([]byte("och-context-source-v1\n"))

// InitialSourceDigest is D0 (design §7.2): SHA256("och-context-source-v1\n").
func InitialSourceDigest() [32]byte { return sourceDigestSeed }

// ExtendSourceDigest computes one step of the extendable chain (design
// §7.2):
//
//	Di = SHA256("och-context-source-step-v1\n" || Di-1 ||
//	     uint64-big-endian(len(encoded)) || encoded)
//
// where encoded is domain.MarshalRecordedEvent(record). The length prefix
// exists specifically to prevent concatenation ambiguity between two
// adjacent encodings. ExtendSourceDigest does not itself filter by
// IsSourceEvent — that is a separately testable concern a caller applies
// before calling this function; passing a non-source record here would
// silently fold it into the chain, which is exactly the "source
// filter/digest" mutation this task's own mutation check targets.
func ExtendSourceDigest(previous [32]byte, record domain.RecordedEvent) ([32]byte, error) {
	encoded, err := domain.MarshalRecordedEvent(record)
	if err != nil {
		return [32]byte{}, err
	}
	hasher := sha256.New()
	hasher.Write([]byte("och-context-source-step-v1\n"))
	hasher.Write(previous[:])
	var lengthPrefix [8]byte
	binary.BigEndian.PutUint64(lengthPrefix[:], uint64(len(encoded)))
	hasher.Write(lengthPrefix[:])
	hasher.Write(encoded)
	var next [32]byte
	copy(next[:], hasher.Sum(nil))
	return next, nil
}

// ExtendSourceDigestOverRecords rolls ExtendSourceDigest forward from seed
// across records in order, applying the IsSourceEvent filter itself and
// skipping every non-source record. Passing InitialSourceDigest() as seed
// scans the full history from D0 (design §7.2's cold-rebuild mode);
// passing a prior validated Dn scans only newly covered records (the
// rolling-successor mode) — the caller decides which records to pass in
// either case, this function does not distinguish the two modes itself.
func ExtendSourceDigestOverRecords(seed [32]byte, records []domain.RecordedEvent) (digest [32]byte, coveredCount uint64, err error) {
	digest = seed
	for _, record := range records {
		if !IsSourceEvent(record.Event) {
			continue
		}
		digest, err = ExtendSourceDigest(digest, record)
		if err != nil {
			return [32]byte{}, 0, err
		}
		coveredCount++
	}
	return digest, coveredCount, nil
}

// ComputeSourceDigest is ExtendSourceDigestOverRecords(InitialSourceDigest(), records) —
// the cold-rebuild mode, spelled out for the common case of digesting a
// complete record slice from scratch.
func ComputeSourceDigest(records []domain.RecordedEvent) (digest [32]byte, coveredCount uint64, err error) {
	return ExtendSourceDigestOverRecords(InitialSourceDigest(), records)
}
