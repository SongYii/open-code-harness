//go:build linux || darwin

package workspacefs_test

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestMutationRejectsFIFOAsNotRegularFileBeforeOpening(t *testing.T) {
	files, root := newTestFS(t)
	fifo := filepath.Join(root, "events")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Read(context.Background(), fifo, 8); !tools.IsCode(err, tools.CodeFSNotRegularFile) {
		t.Fatalf("Read() error = %v, want fs_not_regular_file", err)
	}
	if _, err := files.Write(context.Background(), fifo, []byte("x"), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}); !tools.IsCode(err, tools.CodeFSNotRegularFile) {
		t.Fatalf("Write() error = %v, want fs_not_regular_file", err)
	}
}
