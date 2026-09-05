package workspacefs_test

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestMutationConcurrentWritersFromOneVersionHaveOneWinner(t *testing.T) {
	files, root := newTestFS(t)
	abs := filepath.Join(root, "state.txt")
	writeRel(t, root, "state.txt", []byte("before"))
	read, err := files.Read(context.Background(), abs, 64)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	type outcome struct {
		content string
		err     error
	}
	results := make(chan outcome, 2)
	var writers sync.WaitGroup
	for _, content := range []string{"writer-a", "writer-b"} {
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			_, writeErr := files.Write(context.Background(), abs, []byte(content), tools.MutationGuard{
				Kind: tools.GuardReplaceIfVersion, Version: read.Version,
			})
			results <- outcome{content: content, err: writeErr}
		}()
	}
	close(start)
	writers.Wait()
	close(results)

	winners, stale := 0, 0
	winnerContent := ""
	for result := range results {
		switch {
		case result.err == nil:
			winners++
			winnerContent = result.content
		case tools.IsCode(result.err, tools.CodeFilesystemStaleVersion):
			stale++
		default:
			t.Fatalf("Write() error = %v, want nil or fs_stale_version", result.err)
		}
	}
	if winners != 1 || stale != 1 {
		t.Fatalf("outcomes = %d success, %d stale; want exactly one of each", winners, stale)
	}
	assertFile(t, abs, winnerContent)
}

func TestMutationConcurrentUnseenCreatorsRefuseOverwrite(t *testing.T) {
	files, root := newTestFS(t)
	abs := filepath.Join(root, "new.txt")

	start := make(chan struct{})
	type outcome struct {
		content string
		err     error
	}
	results := make(chan outcome, 2)
	var creators sync.WaitGroup
	for _, content := range []string{"creator-a", "creator-b"} {
		creators.Add(1)
		go func() {
			defer creators.Done()
			<-start
			_, writeErr := files.Write(context.Background(), abs, []byte(content), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent})
			results <- outcome{content: content, err: writeErr}
		}()
	}
	close(start)
	creators.Wait()
	close(results)

	winners, refused := 0, 0
	winnerContent := ""
	for result := range results {
		switch {
		case result.err == nil:
			winners++
			winnerContent = result.content
		case errors.Is(result.err, fs.ErrExist):
			refused++
		default:
			t.Fatalf("Write() error = %v, want nil or file-exists refusal", result.err)
		}
	}
	if winners != 1 || refused != 1 {
		t.Fatalf("outcomes = %d success, %d refused; want exactly one of each", winners, refused)
	}
	assertFile(t, abs, winnerContent)
}
