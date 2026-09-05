package workspacefs_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestMutationGuardedWrite(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "note")
	created, err := files.Write(ctx, abs, []byte("before"), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent})
	if err != nil || created.Operation != tools.MutationCreate || created.Version == "" {
		t.Fatal(created, err)
	}
	read, err := files.Read(ctx, abs, 64)
	if err != nil || string(read.Data) != "before" || read.Version != created.Version {
		t.Fatal(read, err)
	}
	guard := tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: read.Version}
	updated, err := files.Write(ctx, abs, []byte("after"), guard)
	if err != nil || updated.Operation != tools.MutationUpdate || updated.Version == read.Version {
		t.Fatal(updated, err)
	}
	if _, err := files.Write(ctx, abs, []byte("lost"), guard); !tools.IsCode(err, tools.CodeFilesystemStaleVersion) {
		t.Fatal(err)
	}
	if _, err := files.Write(ctx, abs, []byte("lost"), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}); !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
	assertFile(t, abs, "after")
}

func TestMutationConcurrentCreatorsPreserveWinner(t *testing.T) {
	files, root := newTestFS(t)
	abs := filepath.Join(root, "note")
	start := make(chan struct{})
	type outcome struct {
		data string
		err  error
	}
	results := make(chan outcome, 12)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			data := strings.Repeat("x", i+1)
			_, err := files.Write(context.Background(), abs, []byte(data), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent})
			results <- outcome{data, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for result := range results {
		if result.err == nil {
			winners++
			assertFile(t, abs, result.data)
		} else if !errors.Is(result.err, fs.ErrExist) {
			t.Fatal(result.err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d", winners)
	}
}

func TestMutationEditLiteralAndNewlines(t *testing.T) {
	for _, tc := range []struct {
		name, before, old, new, want string
		all                          bool
		code                         tools.ErrorCode
	}{
		{name: "unique", before: "one two", old: "one", new: "ONE", want: "ONE two"},
		{name: "missing", before: "one", old: "absent", new: "x", code: tools.CodeEditNoMatch},
		{name: "ambiguous", before: "one one", old: "one", new: "x", code: tools.CodeEditAmbiguous},
		{name: "all", before: "one one", old: "one", new: "x", want: "x x", all: true},
		{name: "lf", before: "a\nb\n", old: "a\r\nb", new: "x\r\ny", want: "x\ny\n"},
		{name: "crlf", before: "a\r\nb\r\n", old: "a\nb", new: "x\ny", want: "x\r\ny\r\n"},
		{name: "dominant crlf", before: "a\r\nb\r\nc\n", old: "b", new: "B", want: "a\r\nB\r\nc\r\n"},
		{name: "delete", before: "a b", old: "a ", new: "", want: "b"},
		{name: "empty old", before: "a", old: "", new: "b", code: tools.CodeInvalidArgs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, root := newTestFS(t)
			abs := filepath.Join(root, "note")
			writeRel(t, root, "note", []byte(tc.before))
			read, err := files.Read(context.Background(), abs, 64)
			if err != nil {
				t.Fatal(err)
			}
			result, err := files.Edit(context.Background(), abs, []byte(tc.old), []byte(tc.new), tc.all, tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: read.Version})
			if tc.code != "" {
				if !tools.IsCode(err, tc.code) {
					t.Fatal(err)
				}
				assertFile(t, abs, tc.before)
				return
			}
			if err != nil || result.Operation != tools.MutationUpdate || result.Version == read.Version {
				t.Fatal(result, err)
			}
			assertFile(t, abs, tc.want)
		})
	}
}

func TestMutationGuardBeforeMatchAndExternalChange(t *testing.T) {
	files, root := newTestFS(t)
	abs := filepath.Join(root, "note")
	writeRel(t, root, "note", []byte("before"))
	read, err := files.Read(context.Background(), abs, 64)
	if err != nil {
		t.Fatal(err)
	}
	writeRel(t, root, "note", []byte("external"))
	guard := tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: read.Version}
	if _, err := files.Edit(context.Background(), abs, []byte("missing"), []byte("x"), false, guard); !tools.IsCode(err, tools.CodeFilesystemStaleVersion) {
		t.Fatal(err)
	}
	assertFile(t, abs, "external")
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Write(context.Background(), abs, []byte("lost"), guard); !tools.IsCode(err, tools.CodeFilesystemStaleVersion) {
		t.Fatal(err)
	}
}

func TestMutationRejectsInvalidTargetsAndText(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	if _, err := files.Read(ctx, root, 8); !tools.IsCode(err, tools.CodeFilesystemIsDirectory) {
		t.Fatal(err)
	}
	if _, err := files.Write(ctx, root, []byte("x"), tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}); !tools.IsCode(err, tools.CodeFilesystemIsDirectory) {
		t.Fatal(err)
	}
	abs := filepath.Join(root, "binary")
	writeRel(t, root, "binary", []byte{0xff})
	if _, err := files.Read(ctx, abs, 8); !tools.IsCode(err, tools.CodeFilesystemNotText) {
		t.Fatal(err)
	}
	if _, err := files.Write(ctx, filepath.Join(root, "new"), []byte{0xff}, tools.MutationGuard{Kind: tools.GuardCreateIfAbsent}); !tools.IsCode(err, tools.CodeFilesystemNotText) {
		t.Fatal(err)
	}
	if _, err := files.Write(ctx, filepath.Join(root, "new"), []byte("x"), tools.MutationGuard{}); !tools.IsCode(err, tools.CodeInvalidArgs) {
		t.Fatal(err)
	}
	writeRel(t, root, "large", []byte(strings.Repeat("x", tools.MaxEditFileBytes+1)))
	large := filepath.Join(root, "large")
	read, err := files.Read(ctx, large, 8)
	if err != nil || !read.Truncated {
		t.Fatal(read, err)
	}
	if _, err := files.Edit(ctx, large, []byte("x"), []byte("y"), true, tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: read.Version}); !tools.IsCode(err, tools.CodeFilesystemTooLarge) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := files.Edit(ctx, large, []byte("x"), []byte("y"), true, tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: read.Version}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestMutationRejailsAndPreservesMode(t *testing.T) {
	files, root := newTestFS(t)
	ctx := context.Background()
	abs := filepath.Join(root, "note")
	writeRel(t, root, "note", []byte("keep"))
	if err := os.Chmod(abs, 0o751); err != nil {
		t.Fatal(err)
	}
	read, err := files.Read(ctx, abs, 64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.Write(ctx, abs, []byte("updated"), tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: read.Version}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(abs)
	if err != nil || info.Mode().Perm() != 0o751 {
		t.Fatal(info, err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, abs); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Write(ctx, abs, []byte("lost"), tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: read.Version}); !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatal(err)
	}
	if _, err := files.Edit(ctx, abs, []byte("secret"), []byte("lost"), false, tools.MutationGuard{Kind: tools.GuardReplaceIfVersion, Version: read.Version}); !tools.IsCode(err, tools.CodeScopeDenied) {
		t.Fatal(err)
	}
	assertFile(t, outside, "secret")
}

func assertFile(t *testing.T, abs, want string) {
	t.Helper()
	got, err := os.ReadFile(abs)
	if err != nil || string(got) != want {
		t.Fatalf("file = %q, err=%v, want %q", got, err, want)
	}
}
