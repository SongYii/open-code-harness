package composition

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/transcript"
)

// ExportSession projects one session onto out as transcript JSONL. It opens a
// read-only sqlite reader so a live writer can keep the runtime lease, and it
// does not print: callers format Result themselves.
func ExportSession(ctx context.Context, databasePath string, sessionID domain.SessionID, out io.Writer) (transcript.Result, error) {
	if ctx == nil {
		return transcript.Result{}, fmt.Errorf("composition: context is required")
	}
	reader, err := sqlite.OpenReader(ctx, sqlite.ReaderConfig{Path: databasePath})
	if err != nil {
		return transcript.Result{}, fmt.Errorf("composition: open reader: %w", err)
	}
	result, writeErr := transcript.WriteSession(ctx, reader, sessionID, time.Now().UTC(), out)
	closeErr := reader.Close()
	if writeErr != nil {
		return transcript.Result{}, writeErr
	}
	if closeErr != nil {
		return result, fmt.Errorf("composition: close reader: %w", closeErr)
	}
	return result, nil
}
