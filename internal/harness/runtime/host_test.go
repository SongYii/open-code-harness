package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func hostConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		SQLite:            sqlite.Config{Path: filepath.Join(t.TempDir(), "host.db"), RuntimeID: "runtime-host"},
		HeartbeatInterval: 250 * time.Millisecond,
		HeartbeatDeadline: 700 * time.Millisecond,
		ExportInterval:    50 * time.Millisecond,
	}
}

func seedCrashState(t *testing.T, path string) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), sqlite.Config{Path: path, RuntimeID: "runtime-crashed"})
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}
	seedCrashedAssistantItem(t, store)
	// The crashed runtime abandoned its lease; force expiry so the
	// successor may take over.
	if err := store.ExpireLeaseForTesting(context.Background()); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("seed close: %v", err)
	}
}

func TestLaunchReconcilesAndBecomesReady(t *testing.T) {
	config := hostConfig(t)
	seedCrashState(t, config.SQLite.Path)

	host, err := Launch(context.Background(), config)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer func() { _ = host.Shutdown(context.Background()) }()
	if !host.Ready() {
		t.Fatal("host not ready after launch")
	}
	store, err := host.Store()
	if err != nil {
		t.Fatalf("store before reconciliation complete: %v", err)
	}
	page, err := store.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: "session-crash", Limit: 256})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if page.HeadVersion != 5 {
		t.Fatalf("head = %d, want 5 (crash state closed)", page.HeadVersion)
	}
	if terminal, ok := page.Records[4].Event.(domain.TurnInterrupted); !ok || terminal.Reason != processCrashCode {
		t.Fatalf("terminal record = %T, want process_crash TurnInterrupted", page.Records[4].Event)
	}
}

func TestSecondProcessGetsStableDiagnostic(t *testing.T) {
	config := hostConfig(t)
	first, err := Launch(context.Background(), config)
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	defer func() { _ = first.Shutdown(context.Background()) }()

	second := config
	second.SQLite.RuntimeID = "runtime-second"
	_, err = Launch(context.Background(), second)
	if err == nil {
		t.Fatal("second process launched against a live lease")
	}
	var held *ErrLeaseHeld
	if !errors.As(err, &held) || held.Owner != "runtime-host" {
		t.Fatalf("error = %v, want ErrLeaseHeld naming runtime-host", err)
	}
}

func TestShutdownReleasesLeaseForSuccessor(t *testing.T) {
	config := hostConfig(t)
	first, err := Launch(context.Background(), config)
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	firstToken := first.store.Authority().FencingToken
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := first.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if first.Ready() {
		t.Fatal("host still ready after shutdown")
	}
	if _, err := first.Store(); err == nil {
		t.Fatal("store still served after shutdown")
	}

	second := config
	second.SQLite.RuntimeID = "runtime-second"
	successor, err := Launch(context.Background(), second)
	if err != nil {
		t.Fatalf("successor launch after release: %v", err)
	}
	defer func() { _ = successor.Shutdown(context.Background()) }()
	successorToken := successor.store.Authority().FencingToken
	if successorToken <= firstToken {
		t.Fatalf("successor token = %d, want greater than %d", successorToken, firstToken)
	}
}

func TestExporterRunsAfterReadiness(t *testing.T) {
	config := hostConfig(t)
	seedCrashState(t, config.SQLite.Path)
	audit := filepath.Join(t.TempDir(), "audit")
	config.AuditDirectory = audit

	host, err := Launch(context.Background(), config)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer func() { _ = host.Shutdown(context.Background()) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if entries, err := os.ReadDir(filepath.Join(audit, "manifests")); err == nil && len(entries) > 0 {
			return // the background exporter drained the reconciled stream
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("background exporter never published a manifest after readiness")
}
