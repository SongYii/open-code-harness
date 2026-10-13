package composition_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func TestInstructionDiscoveryUsesScopeNormalizedPath(t *testing.T) {
	for _, test := range []struct {
		name, tool, path, instructionPath, content string
		denied                                     bool
	}{
		{"read", tools.NameReadFile, `src\file.go`, "src/AGENTS.md", "source fixture", false},
		{"list", tools.NameListDir, `src\nested`, "src/nested/AGENTS.md", "entry.txt", false},
		{"read_symlink_escape", tools.NameReadFile, `escape\file.go`, "", "", true},
		{"list_symlink_escape", tools.NameListDir, `escape\child`, "", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := validConfig(t)
			alias := filepath.Join(t.TempDir(), "workspace-alias")
			if err := os.Symlink(config.WorkspaceRoot, alias); err != nil {
				t.Fatal(err)
			}
			config.WorkspaceRoot = alias
			config.Policy = policy.ModeDefault
			t.Setenv(config.Provider.APIKeyEnv, "normalized-path-fixture-key")

			writeFixture := func(root, relative, content string) {
				t.Helper()
				path := filepath.Join(root, relative)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			const nestedInstruction = "Nested normalized-path instruction must reach the next request."
			const outsideEvidence = "OUTSIDE EVIDENCE MUST NOT ENTER THE MODEL REQUEST"
			if test.denied {
				outside := t.TempDir()
				writeFixture(outside, "file.go", outsideEvidence)
				writeFixture(outside, "AGENTS.md", outsideEvidence)
				writeFixture(outside, "child/AGENTS.md", outsideEvidence)
				if err := os.Symlink(outside, filepath.Join(alias, "escape")); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFixture(alias, test.instructionPath, nestedInstruction)
				writeFixture(alias, "src/file.go", "source fixture")
				writeFixture(alias, "src/nested/entry.txt", "entry fixture")
			}

			provider := newWorkspaceToolProvider(t, test.tool, test.path)
			config.Provider.BaseURL = provider.URL
			config.Provider.AllowInsecureLoopback = true
			assembly, err := composition.Open(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = assembly.Close() })
			created, err := assembly.Service().CreateSession(context.Background(), application.CreateSessionRequest{WorkspaceRoot: alias})
			if err != nil {
				t.Fatal(err)
			}
			result, err := assembly.Service().RunTurn(context.Background(), application.RunTurnRequest{
				SessionID: created.SessionID, RequestID: "normalized-path", Input: "inspect the fixture", Sink: discardSink{},
			})
			if err != nil || result.Status != domain.TurnStatusCompleted {
				t.Fatalf("RunTurn() status=%q error=%v", result.Status, err)
			}
			if !test.denied && !strings.Contains(result.Text, test.content) {
				t.Fatalf("tool result %q omits real workspace content %q", result.Text, test.content)
			}
			if provider.requests.Load() != 2 {
				t.Fatalf("provider requests=%d, want tool call and consuming request", provider.requests.Load())
			}
			records, err := application.ReadWholeStreamPinned(context.Background(), assembly.Store(), created.SessionID, 256)
			if err != nil {
				t.Fatal(err)
			}
			requests, completed, denied := 0, false, false
			for _, record := range records {
				switch event := record.Event.(type) {
				case domain.ToolCallCompleted:
					completed = true
				case domain.ToolCallFailed:
					denied = event.Code == application.CodeScopeDenied
				case domain.ModelRequestRecorded:
					requests++
					found := false
					for _, message := range event.Messages {
						if strings.Contains(message.Text, outsideEvidence) {
							t.Fatal("symlink escape leaked outside content or instructions")
						}
						found = found || strings.Contains(message.Text, nestedInstruction)
					}
					if requests == 2 && !test.denied && !found {
						t.Fatal("normalized path omitted nested AGENTS.md from consuming request")
					}
				}
			}
			if requests != 2 || (test.denied && (!denied || completed)) || (!test.denied && (!completed || denied)) {
				t.Fatalf("durable facts: requests=%d completed=%v denied=%v", requests, completed, denied)
			}
		})
	}
}
