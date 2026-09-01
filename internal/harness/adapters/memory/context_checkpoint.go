package memory

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/contextengine"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

var _ application.ContextCheckpointStore = (*EventStore)(nil)

// LoadLatestContextCheckpoint implements application.ContextCheckpointStore
// (design §6.4/§14.1) for the memory adapter. Unlike SQLite's bounded
// context_checkpoint_heads row (verified once at write time, in the same
// append transaction that commits the completion event), the memory
// adapter has no separate write-time hook at all: every committed record
// already lives in this Session's own in-memory stream, so a read here
// simply scans for the latest context.compaction.completed event and
// independently re-verifies its full source digest chain from D0 over
// exactly the prefix it claims to cover. This delivers the same
// verification guarantee design §14.1 requires (never trusting a claimed
// digest blindly) with a simpler, always-correct, O(history) scan rather
// than a cached row -- consistent with this adapter's own established
// precedent of favoring simplicity over performance.
func (store *EventStore) LoadLatestContextCheckpoint(ctx context.Context, sessionID domain.SessionID) (application.ContextCheckpointLookup, error) {
	if err := contextError(ctx); err != nil {
		return application.ContextCheckpointLookup{}, readError(sessionID, err)
	}
	if _, err := domain.ParseSessionID(string(sessionID)); err != nil {
		return application.ContextCheckpointLookup{}, readError(sessionID, err)
	}

	store.mu.Lock()
	records := append([]domain.RecordedEvent(nil), store.state.streams[sessionID]...)
	store.mu.Unlock()

	var latest *domain.ContextCheckpointRecord
	for _, record := range records {
		if event, ok := record.Event.(domain.ContextCompactionCompleted); ok {
			checkpoint := event.Checkpoint
			latest = &checkpoint
		}
	}
	if latest == nil {
		return application.ContextCheckpointLookup{Status: application.ContextCheckpointLookupNone}, nil
	}

	covered := make([]domain.RecordedEvent, 0, len(records))
	for _, record := range records {
		if record.Sequence > latest.ThroughSequence {
			break
		}
		covered = append(covered, record)
	}
	digest, _, err := contextengine.ComputeSourceDigest(covered)
	if err != nil {
		return application.ContextCheckpointLookup{}, storeError(application.StoreCodeCorrupt, sessionID, 0, 0, "",
			fmt.Errorf("context checkpoint source digest could not be recomputed: %w", err))
	}
	if hex.EncodeToString(digest[:]) != latest.SourceDigestHex {
		return application.ContextCheckpointLookup{}, storeError(application.StoreCodeCorrupt, sessionID, 0, 0, "",
			fmt.Errorf("context checkpoint source digest does not match canonical events"))
	}
	return application.ContextCheckpointLookup{Status: application.ContextCheckpointLookupFound, Checkpoint: *latest}, nil
}
