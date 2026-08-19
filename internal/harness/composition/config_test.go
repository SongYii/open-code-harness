package composition_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
)

// validConfig is the baseline every case below mutates one field of, so that
// a failure names exactly one cause.
func validConfig(t *testing.T) composition.Config {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	return composition.Config{
		WorkspaceRoot: workspace,
		DatabasePath:  filepath.Join(root, "harness.db"),
		RuntimeID:     "composition-test",
		Provider: composition.Provider{
			BaseURL:       "https://provider.invalid/v1",
			ModelID:       "test-model",
			APIKeyEnv:     "OCH_TEST_API_KEY",
			ContextWindow: 8192,
			MaxOutput:     1024,
		},
		Policy: policy.ModeDefault,
	}
}

func TestValidateAcceptsABaselineConfig(t *testing.T) {
	if err := validConfig(t).Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestValidateRejectsEveryDocumentedCause(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*composition.Config)
		mention string
	}{
		{"blank workspace", func(c *composition.Config) { c.WorkspaceRoot = "" }, "WorkspaceRoot"},
		{"relative workspace", func(c *composition.Config) { c.WorkspaceRoot = "workspace" }, "WorkspaceRoot"},
		{"missing workspace", func(c *composition.Config) { c.WorkspaceRoot = filepath.Join(c.WorkspaceRoot, "absent") }, "WorkspaceRoot"},
		{"workspace is a file", func(c *composition.Config) { c.WorkspaceRoot = writeFile(t, c.WorkspaceRoot, "not-a-dir") }, "not a directory"},
		{"blank database path", func(c *composition.Config) { c.DatabasePath = "" }, "DatabasePath"},
		{"relative database path", func(c *composition.Config) { c.DatabasePath = "harness.db" }, "DatabasePath"},
		{"database parent missing", func(c *composition.Config) { c.DatabasePath = filepath.Join(c.DatabasePath, "absent", "harness.db") }, "DatabasePath parent"},
		{"blank runtime id", func(c *composition.Config) { c.RuntimeID = "" }, "RuntimeID"},
		{"audit directory missing", func(c *composition.Config) { c.AuditDirectory = filepath.Join(c.WorkspaceRoot, "absent") }, "AuditDirectory"},
		{"blank base url", func(c *composition.Config) { c.Provider.BaseURL = "" }, "BaseURL"},
		{"blank model id", func(c *composition.Config) { c.Provider.ModelID = "" }, "ModelID"},
		{"blank api key env", func(c *composition.Config) { c.Provider.APIKeyEnv = "" }, "APIKeyEnv"},
		{"zero context window", func(c *composition.Config) { c.Provider.ContextWindow = 0 }, "ContextWindow"},
		{"zero max output", func(c *composition.Config) { c.Provider.MaxOutput = 0 }, "MaxOutput"},
		{"unknown policy mode", func(c *composition.Config) { c.Policy = policy.Mode("whatever") }, "Policy"},
		{"negative step limit", func(c *composition.Config) { c.Limits.MaxSteps = -1 }, "Limits"},
		{"negative tool call limit", func(c *composition.Config) { c.Limits.MaxToolCallsPerStep = -1 }, "Limits"},
		{"negative assistant bytes", func(c *composition.Config) { c.Limits.MaxAssistantBytes = -1 }, "Limits"},
		{"negative approval timeout", func(c *composition.Config) { c.Limits.ApprovalTimeout = -time.Second }, "timeouts"},
		{"negative shutdown timeout", func(c *composition.Config) { c.ShutdownTimeout = -time.Second }, "timeouts"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validConfig(t)
			test.mutate(&config)
			err := config.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want an error mentioning %q", test.mention)
			}
			if !strings.Contains(err.Error(), test.mention) {
				t.Fatalf("Validate() = %v, want it to name %q", err, test.mention)
			}
		})
	}
}

// TestValidateAppliesDefaultsWithoutMutatingTheCaller pins that a zero Policy
// and a zero ShutdownTimeout are defaults rather than rejections, and that
// validating does not quietly rewrite the caller's value.
func TestValidateAppliesDefaultsWithoutMutatingTheCaller(t *testing.T) {
	config := validConfig(t)
	config.Policy = ""
	config.ShutdownTimeout = 0
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want defaults to apply", err)
	}
	if config.Policy != "" || config.ShutdownTimeout != 0 {
		t.Fatalf("Validate() mutated the caller's config: policy=%q timeout=%v", config.Policy, config.ShutdownTimeout)
	}
}

func writeFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
