package sqlite

import (
	"context"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestRebuildReproducesMaintainedProjection(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-rb1", "session-rb", 0, "command-rb1",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-rb", Input: "x"},
		domain.AssistantMessageStarted{TurnID: "turn-rb", ItemID: "item-rb"}))
	mustAppend(t, store, appendRequest("append-rb2", "session-rb", 3, "command-rb2",
		domain.AssistantMessageCompleted{TurnID: "turn-rb", ItemID: "item-rb", Text: "done"},
		domain.TurnCompleted{TurnID: "turn-rb"}))

	if err := store.RebuildAndVerifySessionHeads(context.Background()); err != nil {
		t.Fatalf("rebuild and verify: %v", err)
	}
}

func TestRebuildDetectsSeededMismatch(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-rb-seed", "session-rb-seed", 0, "command-rb-seed",
		domain.SessionCreated{WorkspaceRoot: "/w"},
		domain.TurnStarted{TurnID: "turn-rb-seed", Input: "x"}))

	if _, err := store.writer.ExecContext(context.Background(),
		"UPDATE session_heads SET status = 'idle', active_turn_id = NULL WHERE session_id = 'session-rb-seed'"); err != nil {
		t.Fatalf("seed mismatch: %v", err)
	}

	if err := store.RebuildAndVerifySessionHeads(context.Background()); err == nil {
		t.Fatal("rebuild on mismatched projection = nil, want corruption")
	} else {
		requireStoreCode(t, err, application.StoreCodeCorrupt)
	}
}
