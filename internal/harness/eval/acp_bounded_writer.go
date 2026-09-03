package eval

import "sync"

// boundedWriter caps the total bytes it retains at limit, discarding
// anything beyond that — used for a subprocess writer's own stderr
// (design §16/§19's stdout/stderr evidence bounds), never for the ACP
// wire itself (stdout), which must never be truncated mid-frame.
type boundedWriter struct {
	limit     int64
	mu        sync.Mutex
	buf       []byte
	truncated bool
}

func newBoundedWriter(limit int64) *boundedWriter {
	return &boundedWriter{limit: limit}
}

// Write always reports success and never blocks the child: bytes beyond
// limit are counted (Truncated becomes true) but not retained.
func (w *boundedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.limit - int64(len(w.buf))
	if remaining > 0 {
		add := p
		if int64(len(add)) > remaining {
			add = add[:remaining]
		}
		w.buf = append(w.buf, add...)
	}
	if int64(len(p)) > remaining {
		w.truncated = true
	}
	return len(p), nil
}

// Bytes returns a copy of everything retained so far.
func (w *boundedWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf...)
}

// Truncated reports whether any byte was ever dropped for exceeding limit.
func (w *boundedWriter) Truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated
}
