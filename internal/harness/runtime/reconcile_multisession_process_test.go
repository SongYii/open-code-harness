package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestRecoveryProcessKilledBetweenSessions(t *testing.T) {
	// Production enumerates sorted active Turns first, then de-duplicates the
	// compaction candidates. Deliberately place manual compaction first by name:
	// it must still be recovered last, while the active compaction appears once.
	sessions := []struct {
		id   domain.SessionID
		kind string
	}{
		{"b-assistant", "assistant"},
		{"c-compacting-turn", "active_compaction"},
		{"d-tool", "tool"},
		{"a-manual", "manual_compaction"},
		{"e-idle", "idle"},
	}
	type fixture struct {
		name, path string
		before     [][]domain.RecordedEvent
		published  [][]domain.RecordedEvent
	}
	var pending []fixture
	for _, at := range []int{2, 4} {
		for _, side := range []string{"before_publish", "after_publish"} {
			for _, kill := range []bool{false, true} {
				path := filepath.Join(t.TempDir(), "multi.db")
				name := fmt.Sprintf("commit=%d/%s/kill=%t", at, side, kill)
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					store, err := sqlite.Open(ctx, recoveryProcessConfig(path, "seed").SQLite)
					if err != nil {
						t.Fatal(err)
					}
					defer store.Close()
					var before [][]domain.RecordedEvent
					for _, session := range sessions {
						before = append(before, seedRecoverySession(t, store, session.id, session.kind))
					}
					if err := store.ReleaseLease(ctx); err != nil {
						t.Fatal(err)
					}
					if err := store.Close(); err != nil {
						t.Fatal(err)
					}
					runRecoveryProcess(t, path, side, kill, fmt.Sprintf("OCH_RECOVERY_KILL_AT=%d", at), "OCH_RECOVERY_CANDIDATES=4")
					committed := 4
					if kill {
						committed = at
						if side == "before_publish" {
							committed--
						}
					}
					var published [][]domain.RecordedEvent
					for i, session := range sessions {
						got := readRecoverySession(t, path, session.id)
						published = append(published, got)
						if i < committed {
							assertProcessRecovery(t, before[i], got, session.kind)
						} else if !reflect.DeepEqual(before[i], got) {
							t.Fatalf("pending/idle session %s changed", session.id)
						}
					}
					if kill {
						early, err := Launch(ctx, recoveryProcessConfig(path, "early"))
						if early != nil {
							_ = early.Shutdown(ctx)
						}
						var held *ErrLeaseHeld
						if !errors.As(err, &held) || held.Owner != "recovering" {
							t.Fatalf("partial recovery bypassed live lease: %v", err)
						}
						pending = append(pending, fixture{name, path, before, published})
					}
				})
			}
		}
	}
	for _, f := range pending {
		t.Run(f.name+"/successor", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var host *Host
			for {
				var err error
				host, err = Launch(ctx, recoveryProcessConfig(f.path, "successor"))
				if err == nil {
					break
				}
				var held *ErrLeaseHeld
				if !errors.As(err, &held) {
					t.Fatal(err)
				}
				select {
				case <-ctx.Done():
					t.Fatal("lease did not expire")
				case <-time.After(50 * time.Millisecond):
				}
			}
			defer host.Shutdown(context.Background())
			if !host.Ready() {
				t.Fatal("successor not ready")
			}
			var recovered [][]domain.RecordedEvent
			for i, session := range sessions {
				got := readRecoverySession(t, f.path, session.id)
				recovered = append(recovered, got)
				prefix := f.published[i]
				if len(got) < len(prefix) || !reflect.DeepEqual(got[:len(prefix)], prefix) {
					t.Fatalf("successor rewrote published session %s", session.id)
				}
				if session.kind == "idle" {
					if !reflect.DeepEqual(f.before[i], got) {
						t.Fatal("idle session gained recovery facts")
					}
				} else {
					assertProcessRecovery(t, f.before[i], got, session.kind)
				}
			}
			if err := host.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			again, err := Launch(ctx, recoveryProcessConfig(f.path, "third"))
			if err != nil {
				t.Fatal(err)
			}
			defer again.Shutdown(context.Background())
			for i, session := range sessions {
				if !reflect.DeepEqual(recovered[i], readRecoverySession(t, f.path, session.id)) {
					t.Fatalf("third host duplicated recovery for %s", session.id)
				}
			}
		})
	}
}
