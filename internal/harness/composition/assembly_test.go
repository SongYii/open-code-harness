package composition_test

import (
	"context"
	"os"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/localexec"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
)

// TestOpenConstructsNothingWhenTheConfigIsInvalid pins the fail-closed rule:
// validation runs to completion before any resource exists, so a rejected
// config leaves no database file and no lease behind.
func TestOpenConstructsNothingWhenTheConfigIsInvalid(t *testing.T) {
	config := validConfig(t)
	config.Provider.ModelID = ""

	assembly, err := composition.Open(context.Background(), config)
	if err == nil {
		t.Fatal("Open() error = nil, want a validation failure")
	}
	if assembly != nil {
		t.Fatalf("Open() = %#v, want a nil assembly alongside the error", assembly)
	}
	if _, statErr := os.Stat(config.DatabasePath); !os.IsNotExist(statErr) {
		t.Fatalf("Open() created %s despite refusing the config", config.DatabasePath)
	}
}

// TestOpenRefusesAMissingCredentialBeforeTouchingTheDatabase keeps the
// credential check ahead of construction. Reading the key later would mean a
// database file and a lease existed before the assembly could possibly work.
func TestOpenRefusesAMissingCredentialBeforeTouchingTheDatabase(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "")

	assembly, err := composition.Open(context.Background(), config)
	if err == nil {
		t.Fatal("Open() error = nil, want a missing-credential failure")
	}
	if assembly != nil {
		t.Fatalf("Open() = %#v, want a nil assembly alongside the error", assembly)
	}
	if _, statErr := os.Stat(config.DatabasePath); !os.IsNotExist(statErr) {
		t.Fatalf("Open() created %s before checking the credential", config.DatabasePath)
	}
}

func TestOpenRejectsANilContext(t *testing.T) {
	//nolint:staticcheck // passing a nil context is exactly what this asserts.
	assembly, err := composition.Open(nil, validConfig(t))
	if err == nil || assembly != nil {
		t.Fatalf("Open(nil) = (%#v, %v), want (nil, error)", assembly, err)
	}
}

// TestOpenAndCloseRoundTrip builds a real assembly against a real database
// and shuts it down. The provider is never contacted: constructing the model
// adapter does not open a connection, so no server is needed to prove the
// wiring holds.
func TestOpenAndCloseRoundTrip(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")

	assembly, err := composition.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if assembly.Service() == nil || assembly.Host() == nil || assembly.Store() == nil {
		t.Fatal("Open() returned an assembly with a missing component")
	}
	if !assembly.Host().Ready() {
		t.Fatal("Open() returned before the host was ready")
	}
	if _, statErr := os.Stat(config.DatabasePath); statErr != nil {
		t.Fatalf("Open() did not create %s: %v", config.DatabasePath, statErr)
	}
	if err := assembly.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestOpenSucceedsWithDefaultFlagWhenSandboxIsAvailable proves the real,
// unforced default path: on a host where bwrap or sandbox-exec actually
// works, Open needs no escape hatch at all. Skips wherever this CI host
// has neither.
func TestOpenSucceedsWithDefaultFlagWhenSandboxIsAvailable(t *testing.T) {
	available, reason := localexec.Availability()
	if !available {
		t.Skipf("no OS-level exec sandbox is available in this environment: %s", reason)
	}
	config := validConfig(t)
	config.AllowUnsandboxedExec = false
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")

	assembly, err := composition.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() error = %v, want the default flag to be enough when the sandbox is real", err)
	}
	if err := assembly.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if assembly.Host().Ready() {
		t.Fatal("Close() left the host admitting work")
	}
}

// TestCloseIsIdempotent pins that a second Close returns the first result
// rather than shutting down again, which would release a lease this assembly
// no longer owns.
func TestCloseIsIdempotent(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")

	assembly, err := composition.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	first := assembly.Close()
	second := assembly.Close()
	if first != nil {
		t.Fatalf("Close() error = %v", first)
	}
	if second != first {
		t.Fatalf("second Close() = %v, want the first result %v", second, first)
	}
}

// TestOpenRefusesASecondAssemblyOverTheSameDatabase proves the fencing lease
// is real through the composition root: a second host cannot take a database
// the first still holds.
func TestOpenRefusesASecondAssemblyOverTheSameDatabase(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")

	first, err := composition.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })

	second := config
	second.RuntimeID = "composition-test-successor"
	assembly, err := composition.Open(context.Background(), second)
	if err == nil {
		_ = assembly.Close()
		t.Fatal("Open() accepted a second assembly over a held database")
	}
	if assembly != nil {
		t.Fatalf("Open() = %#v, want a nil assembly alongside the error", assembly)
	}
}

// TestOpenReleasesTheHostWhenALaterStepFails drives the release path: the
// host launches, and the provider adapter then rejects an unusable base URL.
// The lease must not survive, or the database would stay locked by a process
// that owns nothing.
func TestOpenReleasesTheHostWhenALaterStepFails(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")
	// Passes Validate (non-empty) and fails inside the provider adapter.
	config.Provider.BaseURL = "http-not-a-scheme://%zz"

	assembly, err := composition.Open(context.Background(), config)
	if err == nil {
		_ = assembly.Close()
		t.Fatal("Open() accepted an unusable provider base URL")
	}
	if assembly != nil {
		t.Fatalf("Open() = %#v, want a nil assembly alongside the error", assembly)
	}

	// The lease is free again, so a fresh assembly may take the database.
	config.Provider.BaseURL = "https://provider.invalid/v1"
	recovered, err := composition.Open(context.Background(), config)
	if err != nil {
		t.Fatalf("Open() after a released failure error = %v; the lease outlived the failed assembly", err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
