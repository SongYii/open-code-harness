package testkit

import (
	"sync"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestFixedClockReturnsConfiguredTimeInUTC(t *testing.T) {
	zone := time.FixedZone("test", 8*60*60)
	configured := time.Date(2026, 8, 12, 9, 2, 3, 4, zone)
	clock := FixedClock{Time: configured}

	got := clock.Now()
	if want := configured.UTC(); !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("Now() = %v (%v), want %v (UTC)", got, got.Location(), want)
	}
}

func TestSequenceIDsAreTypedAndIndependent(t *testing.T) {
	ids := NewSequenceIDs()
	var _ application.IDGenerator = ids

	if got, err := ids.NewSessionID(); err != nil || got != domain.SessionID("session-1") {
		t.Fatalf("session ID = %q, %v", got, err)
	}
	if got, err := ids.NewTurnID(); err != nil || got != domain.TurnID("turn-1") {
		t.Fatalf("turn ID = %q, %v", got, err)
	}
	if got, err := ids.NewItemID(); err != nil || got != domain.ItemID("item-1") {
		t.Fatalf("item ID = %q, %v", got, err)
	}
	if got, err := ids.NewCommandID(); err != nil || got != domain.CommandID("command-1") {
		t.Fatalf("command ID = %q, %v", got, err)
	}
	if got, err := ids.NewEventID(); err != nil || got != domain.EventID("event-1") {
		t.Fatalf("event ID = %q, %v", got, err)
	}

	if got, _ := ids.NewSessionID(); got != "session-2" {
		t.Fatalf("second session ID = %q", got)
	}
	if got, _ := ids.NewEventID(); got != "event-2" {
		t.Fatalf("second event ID = %q", got)
	}
}

func TestSequenceIDsAreConcurrentSafe(t *testing.T) {
	const goroutines = 32
	ids := NewSequenceIDs()

	assertUnique := func(t *testing.T, kind string, next func() (string, error)) {
		t.Helper()
		results := make(chan string, goroutines)
		errorsFound := make(chan error, goroutines)
		var group sync.WaitGroup
		for range goroutines {
			group.Add(1)
			go func() {
				defer group.Done()
				value, err := next()
				if err != nil {
					errorsFound <- err
					return
				}
				results <- value
			}()
		}
		group.Wait()
		close(results)
		close(errorsFound)

		for err := range errorsFound {
			t.Errorf("%s generator failed: %v", kind, err)
		}
		seen := make(map[string]struct{}, goroutines)
		for value := range results {
			if _, exists := seen[value]; exists {
				t.Errorf("%s generator returned duplicate %q", kind, value)
			}
			seen[value] = struct{}{}
		}
		if len(seen) != goroutines {
			t.Errorf("%s generator returned %d unique IDs, want %d", kind, len(seen), goroutines)
		}
	}

	t.Run("session", func(t *testing.T) {
		assertUnique(t, "session", func() (string, error) {
			value, err := ids.NewSessionID()
			return string(value), err
		})
	})
	t.Run("turn", func(t *testing.T) {
		assertUnique(t, "turn", func() (string, error) {
			value, err := ids.NewTurnID()
			return string(value), err
		})
	})
	t.Run("item", func(t *testing.T) {
		assertUnique(t, "item", func() (string, error) {
			value, err := ids.NewItemID()
			return string(value), err
		})
	})
	t.Run("command", func(t *testing.T) {
		assertUnique(t, "command", func() (string, error) {
			value, err := ids.NewCommandID()
			return string(value), err
		})
	})
	t.Run("event", func(t *testing.T) {
		assertUnique(t, "event", func() (string, error) {
			value, err := ids.NewEventID()
			return string(value), err
		})
	})
}
