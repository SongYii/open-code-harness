package composition

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/policy"
)

// internalValidConfig mirrors composition_test's validConfig, duplicated
// here because an internal test file (needed to reach the unexported
// checkSandboxAvailability seam) cannot import the external test
// package's helpers. AllowUnsandboxedExec defaults false, unlike the
// external helper: these tests are specifically about that gate.
func internalValidConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	return Config{
		WorkspaceRoot: workspace,
		DatabasePath:  filepath.Join(root, "harness.db"),
		RuntimeID:     "availability-test",
		Provider: Provider{
			BaseURL:       "https://provider.invalid/v1",
			ModelID:       "test-model",
			APIKeyEnv:     "OCH_AVAILABILITY_TEST_API_KEY",
			ContextWindow: 8192,
			MaxOutput:     1024,
		},
		Policy: policy.ModeDefault,
	}
}

// forceSandboxAvailability overrides the checkSandboxAvailability seam for
// the calling test's duration, restoring the real probe on cleanup.
func forceSandboxAvailability(t *testing.T, available bool, reason string) {
	t.Helper()
	original := checkSandboxAvailability
	checkSandboxAvailability = func() (bool, string) { return available, reason }
	t.Cleanup(func() { checkSandboxAvailability = original })
}

func TestOpenFailsClosedWhenSandboxUnavailableAndFlagUnset(t *testing.T) {
	forceSandboxAvailability(t, false, "forced unavailable for test")
	config := internalValidConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")

	assembly, err := Open(context.Background(), config)
	if err == nil {
		t.Fatal("Open() error = nil, want a sandbox-unavailable failure")
	}
	if !errors.Is(err, errInvalidConfig) {
		t.Fatalf("Open() error = %v, want it to wrap errInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), "forced unavailable for test") {
		t.Fatalf("Open() error = %v, want it to name the reason", err)
	}
	if assembly != nil {
		t.Fatalf("Open() = %#v, want a nil assembly alongside the error", assembly)
	}
	if _, statErr := os.Stat(config.DatabasePath); !os.IsNotExist(statErr) {
		t.Fatalf("Open() created %s despite refusing to start", config.DatabasePath)
	}
}

func TestOpenProceedsAndLogsWhenFlagSetAndSandboxUnavailable(t *testing.T) {
	forceSandboxAvailability(t, false, "forced unavailable for test")
	config := internalValidConfig(t)
	config.AllowUnsandboxedExec = true
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")

	var logBuf bytes.Buffer
	originalOutput, originalFlags := log.Writer(), log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})

	assembly, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer assembly.Close()

	if !strings.Contains(logBuf.String(), "forced unavailable for test") {
		t.Fatalf("log output = %q, want it to name the absent guarantee", logBuf.String())
	}
}

func TestOpenProceedsSilentlyWhenSandboxAvailable(t *testing.T) {
	forceSandboxAvailability(t, true, "")
	config := internalValidConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")

	var logBuf bytes.Buffer
	originalOutput := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(originalOutput) })

	assembly, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer assembly.Close()

	if logBuf.Len() != 0 {
		t.Fatalf("log output = %q, want silence when the sandbox is available", logBuf.String())
	}
}
