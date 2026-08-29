package sqlite

import (
	"context"
	"database/sql"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestRebuildReproducesMaintainedProjection(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-rb1", "session-rb", 0, "command-rb1",
		domain.SessionCreated{WorkspaceRoot: "/w/."},
		domain.TurnStarted{TurnID: "turn-rb", Input: "x"},
		domain.AssistantMessageStarted{TurnID: "turn-rb", ItemID: "item-rb"}))
	mustAppend(t, store, appendRequest("append-rb2", "session-rb", 3, "command-rb2",
		domain.AssistantMessageCompleted{TurnID: "turn-rb", ItemID: "item-rb", Text: "done"},
		domain.TurnCompleted{TurnID: "turn-rb"}))
	mustAppend(t, store, appendRequest("append-rb3", "session-rb", 5, "command-rb3",
		domain.SessionDeleted{}))

	if err := store.RebuildAndVerifySessionHeads(context.Background()); err != nil {
		t.Fatalf("rebuild and verify: %v", err)
	}
}

func TestRebuildDetectsOrphanHead(t *testing.T) {
	store := openStore(t, tempStoreConfig(t))
	mustAppend(t, store, appendRequest("append-rb-orphan", "session-rb-orphan-source", 0, "command-rb-orphan",
		domain.SessionCreated{WorkspaceRoot: "/w"}))
	raw, err := sql.Open("sqlite", "file:"+store.config.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("INSERT INTO session_heads (session_id, workspace_root, status, updated_at_commit_position) VALUES ('session-rb-orphan', '/w', 'idle', 1)"); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.RebuildAndVerifySessionHeads(context.Background()); err == nil {
		t.Fatal("RebuildAndVerifySessionHeads(orphan) = nil, want corruption")
	} else {
		requireStoreCode(t, err, application.StoreCodeCorrupt)
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
