//go:build ignore

// Prepare a private Composition overlay without adding a production test hook.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: go run experiments/deepseek-messages-live/prepare.go /tmp/och-deepseek-live.<suffix>")
	}
	root := filepath.Clean(os.Args[1])
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || filepath.Dir(root) != "/tmp" || !strings.HasPrefix(filepath.Base(root), "och-deepseek-live.") {
		fail("a private existing /tmp/och-deepseek-live.<suffix> directory is required")
	}
	repo, err := os.Getwd()
	if err != nil {
		fail("working directory unavailable")
	}
	assembly := filepath.Join(repo, "internal/harness/composition/assembly.go")
	source, err := os.ReadFile(assembly)
	if err != nil {
		fail("run from the repository root")
	}
	needle := "model, err = anthropic.New(anthropic.Config{"
	if strings.Count(string(source), needle) != 1 {
		fail("assembly shape changed: review injection before running")
	}
	patched := strings.Replace(string(source), needle, needle+"\n\t\t\tHTTPClient: messagesLiveHTTPClient,", 1)
	snapshot := filepath.Join(root, "assembly.overlay")
	if err := os.WriteFile(snapshot, []byte(patched), 0600); err != nil {
		fail("cannot write private assembly snapshot")
	}
	manifest := struct{ Replace map[string]string }{map[string]string{
		assembly: snapshot,
		filepath.Join(repo, "internal/harness/composition/messages_live_test.go"):         filepath.Join(repo, "experiments/deepseek-messages-live/composition_live_test.go"),
		filepath.Join(repo, "internal/harness/composition/messages_live_fixture_test.go"): filepath.Join(repo, "experiments/deepseek-messages-live/composition_fixture_test.go"),
	}}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fail("cannot encode overlay")
	}
	path := filepath.Join(root, "composition-overlay.json")
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		fail("cannot write overlay")
	}
	fmt.Println(path)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
