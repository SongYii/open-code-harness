package testkit

import (
	"context"
	"errors"
	"sync"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// RecordingSink records every delivery attempt. With FailOrdinal == 0 it is
// safe to share between Emitters; failure injection is a single-Emitter fixture.
type RecordingSink struct {
	mu          sync.Mutex
	FailOrdinal uint64
	Failure     error
	failureUsed bool
	attempts    []engine.RuntimeEvent
	delivered   []engine.RuntimeEvent
}

func (sink *RecordingSink) Emit(_ context.Context, event engine.RuntimeEvent) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.attempts = append(sink.attempts, event)
	if sink.FailOrdinal != 0 && !sink.failureUsed && event.Ordinal == sink.FailOrdinal {
		sink.failureUsed = true
		if sink.Failure != nil {
			return sink.Failure
		}
		return errors.New("recording sink injected failure")
	}
	sink.delivered = append(sink.delivered, event)
	return nil
}
func (sink *RecordingSink) Attempts() []engine.RuntimeEvent {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]engine.RuntimeEvent(nil), sink.attempts...)
}
func (sink *RecordingSink) Delivered() []engine.RuntimeEvent {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]engine.RuntimeEvent(nil), sink.delivered...)
}
