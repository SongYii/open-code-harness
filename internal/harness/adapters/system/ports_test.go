package system_test

import (
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/system"
)

func TestClockReportsUTC(t *testing.T) {
	got := system.Clock{}.Now()
	if got.Location() != time.UTC {
		t.Fatalf("Now() location = %v, want UTC", got.Location())
	}
	if got.IsZero() {
		t.Fatal("Now() = zero time")
	}
}

// TestIDsAreUniqueValidAndKindPrefixed covers the three properties the port
// depends on: identifiers parse, they say what kind they are, and repeated
// calls do not collide. Uniqueness is sampled rather than proven; a collision
// at this sample size would mean the random source is broken, not unlucky.
func TestIDsAreUniqueValidAndKindPrefixed(t *testing.T) {
	ids := system.IDs{}
	const samples = 256

	kinds := map[string]func() (string, error){
		"session":  func() (string, error) { v, err := ids.NewSessionID(); return string(v), err },
		"turn":     func() (string, error) { v, err := ids.NewTurnID(); return string(v), err },
		"item":     func() (string, error) { v, err := ids.NewItemID(); return string(v), err },
		"command":  func() (string, error) { v, err := ids.NewCommandID(); return string(v), err },
		"append":   func() (string, error) { v, err := ids.NewAppendID(); return string(v), err },
		"event":    func() (string, error) { v, err := ids.NewEventID(); return string(v), err },
		"approval": func() (string, error) { v, err := ids.NewApprovalID(); return string(v), err },
	}
	for kind, generate := range kinds {
		t.Run(kind, func(t *testing.T) {
			seen := make(map[string]struct{}, samples)
			for range samples {
				value, err := generate()
				if err != nil {
					t.Fatalf("generate %s: %v", kind, err)
				}
				if !strings.HasPrefix(value, kind+"-") {
					t.Fatalf("%s id = %q, want a %q prefix", kind, value, kind+"-")
				}
				if len(value) != len(kind)+1+32 {
					t.Fatalf("%s id = %q, want %d hex characters after the prefix", kind, value, 32)
				}
				if _, duplicate := seen[value]; duplicate {
					t.Fatalf("%s id %q repeated within %d samples", kind, value, samples)
				}
				seen[value] = struct{}{}
			}
		})
	}
}

// TestIDsAreSafeForConcurrentUse pins that the generator holds no state a
// caller could race on; Application hands one instance to a Service that runs
// turns concurrently.
func TestIDsAreSafeForConcurrentUse(t *testing.T) {
	ids := system.IDs{}
	results := make(chan string, 64)
	for range 64 {
		go func() {
			value, err := ids.NewEventID()
			if err != nil {
				results <- ""
				return
			}
			results <- string(value)
		}()
	}
	seen := map[string]struct{}{}
	for range 64 {
		value := <-results
		if value == "" {
			t.Fatal("concurrent generation failed")
		}
		if _, duplicate := seen[value]; duplicate {
			t.Fatalf("concurrent generation repeated %q", value)
		}
		seen[value] = struct{}{}
	}
}
