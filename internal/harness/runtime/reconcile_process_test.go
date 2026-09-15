package runtime

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func recoveryProcessConfig(path, owner string) Config {
	return Config{SQLite: sqlite.Config{Path: path, RuntimeID: owner, LeaseDuration: 3 * time.Second}, HeartbeatInterval: 100 * time.Millisecond, HeartbeatDeadline: 500 * time.Millisecond}
}

// Seeded canonical histories isolate the recovery transaction itself. The
// previous Composition matrix covers how real model/tool operations reach them.
func TestRecoveryProcessKilledAtCommit(t *testing.T) {
	type fixture struct {
		path, kind, side string
		before           []domain.RecordedEvent
	}
	var pending []fixture
	for _, kind := range []string{"assistant", "tool", "manual_compaction", "active_compaction"} {
		for _, side := range []string{"before_publish", "after_publish"} {
			for _, kill := range []bool{false, true} {
				path := filepath.Join(t.TempDir(), "recovery.db")
				t.Run(fmt.Sprintf("%s/%s/kill=%t", kind, side, kill), func(t *testing.T) {
					before := seedRecoveryProcess(t, path, kind)
					runRecoveryProcess(t, path, side, kill)
					got := readRecoveryProcess(t, path)
					if kill && side == "before_publish" {
						if !reflect.DeepEqual(got, before) {
							t.Fatal("uncommitted recovery escaped SQLite transaction")
						}
					} else {
						assertProcessRecovery(t, before, got, kind)
					}
					if kill {
						early, err := Launch(context.Background(), recoveryProcessConfig(path, "early"))
						if early != nil {
							_ = early.Shutdown(context.Background())
						}
						var held *ErrLeaseHeld
						if !errors.As(err, &held) || held.Owner != "recovering" {
							t.Fatalf("killed recovery released its lease: %v", err)
						}
						pending = append(pending, fixture{path, kind, side, before})
					}
				})
			}
		}
	}
	// Leases age together while the other fixtures execute; no clock or row edit.
	for _, f := range pending {
		t.Run(f.kind+"/"+f.side+"/successor", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var host *Host
			for {
				var err error
				host, err = Launch(ctx, recoveryProcessConfig(f.path, "successor"))
				if err == nil {
					break
				}
				var held *ErrLeaseHeld
				if !errors.As(err, &held) {
					t.Fatal(err)
				}
				select {
				case <-ctx.Done():
					t.Fatal("lease did not expire")
				case <-time.After(50 * time.Millisecond):
				}
			}
			defer host.Shutdown(context.Background())
			if !host.Ready() {
				t.Fatal("successor not ready after reconciliation")
			}
			got := readRecoveryProcess(t, f.path)
			assertProcessRecovery(t, f.before, got, f.kind)
			if err := host.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			again, err := Launch(ctx, recoveryProcessConfig(f.path, "third"))
			if err != nil {
				t.Fatal(err)
			}
			defer again.Shutdown(context.Background())
			if !reflect.DeepEqual(got, readRecoveryProcess(t, f.path)) {
				t.Fatal("restarting recovered host duplicated facts")
			}
		})
	}
}

func seedRecoveryProcess(t *testing.T, path, kind string) []domain.RecordedEvent {
	t.Helper()
	store, err := sqlite.Open(context.Background(), recoveryProcessConfig(path, "seed").SQLite)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	before := seedRecoverySession(t, store, "session-kill", kind)
	if err := store.ReleaseLease(context.Background()); err != nil {
		t.Fatal(err)
	}
	return before
}

func seedRecoverySession(t *testing.T, store *sqlite.Store, session domain.SessionID, kind string) []domain.RecordedEvent {
	t.Helper()
	events := []domain.Event{domain.SessionCreated{WorkspaceRoot: "/synthetic"}}
	if kind == "manual_compaction" {
		events = append(events, validContextCompactionStarted("compaction-kill", domain.ContextTriggerManual))
	} else if kind != "idle" {
		events = append(events, domain.TurnStarted{TurnID: "turn-kill", Input: "synthetic"}, domain.AssistantMessageStarted{TurnID: "turn-kill", ItemID: "assistant-kill"})
		if kind == "tool" {
			events = append(events, domain.AssistantMessageCompleted{TurnID: "turn-kill", ItemID: "assistant-kill", Text: "calling", ToolCalls: []domain.ToolCallOffer{{ID: "call-kill", Name: "read_file", Arguments: `{"path":"synthetic"}`}}}, domain.ToolCallStarted{TurnID: "turn-kill", ItemID: "tool-kill", CallID: "call-kill", Name: "read_file", Arguments: `{"path":"synthetic"}`, StepIndex: 1})
		}
		if kind == "active_compaction" {
			events = append(events, validContextCompactionStarted("compaction-kill", domain.ContextTriggerOverflowRetry))
		}
	}
	var proposedEvents []application.ProposedEvent
	for i, event := range events {
		proposedEvents = append(proposedEvents, proposed(fmt.Sprintf("%s-seed-%d", session, i), event))
	}
	hostAppend(t, store, application.AppendRequest{AppendID: domain.AppendID(string(session) + "-seed-session"), SessionID: session, CommandID: domain.CommandID(string(session) + "-session-command"), Authority: store.Authority(), Events: proposedEvents[:1]})
	end := len(proposedEvents)
	if kind == "manual_compaction" || kind == "active_compaction" {
		end--
	}
	if end > 1 {
		hostAppend(t, store, application.AppendRequest{AppendID: domain.AppendID(string(session) + "-seed-turn"), SessionID: session, ExpectedVersion: 1, CommandID: domain.CommandID(string(session) + "-command-kill"), Authority: store.Authority(), Events: proposedEvents[1:end]})
	}
	if end < len(proposedEvents) {
		hostAppend(t, store, application.AppendRequest{AppendID: domain.AppendID(string(session) + "-seed-compaction"), SessionID: session, ExpectedVersion: uint64(end), CommandID: domain.CommandID(string(session) + "-compaction-command"), Authority: store.Authority(), Events: proposedEvents[end:]})
	}
	before := readAllRuntime(t, store, session)
	if _, err := domain.Replay(before); err != nil {
		t.Fatal(err)
	}
	return before
}

func readRecoveryProcess(t *testing.T, path string) []domain.RecordedEvent {
	return readRecoverySession(t, path, "session-kill")
}

func readRecoverySession(t *testing.T, path string, session domain.SessionID) []domain.RecordedEvent {
	t.Helper()
	reader, err := sqlite.OpenReader(context.Background(), sqlite.ReaderConfig{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	page, err := reader.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: session, Limit: 256})
	if err != nil || uint64(len(page.Records)) != page.HeadVersion {
		t.Fatalf("read complete recovery fixture: %v", err)
	}
	return page.Records
}

func assertProcessRecovery(t *testing.T, before, after []domain.RecordedEvent, kind string) {
	t.Helper()
	session := before[0].SessionID
	want := []domain.Event{domain.AssistantMessageInterrupted{TurnID: "turn-kill", ItemID: "assistant-kill", Code: processCrashCode}, domain.TurnInterrupted{TurnID: "turn-kill", Reason: processCrashCode}}
	failed := domain.ContextCompactionFailed{ID: "compaction-kill", Code: runtimeRecoveredCode, Message: runtimeRecoveredMessage}
	item := "assistant-kill"
	if kind == "tool" {
		item = "tool-kill"
		want[0] = domain.ToolCallInterrupted{TurnID: "turn-kill", ItemID: "tool-kill", CallID: "call-kill", Code: processCrashCode}
	}
	if kind == "manual_compaction" {
		want = []domain.Event{failed}
	}
	if kind == "active_compaction" {
		want = append([]domain.Event{failed}, want...)
	}
	if len(after) != len(before)+len(want) || !reflect.DeepEqual(before, after[:len(before)]) {
		t.Fatal("recovery changed prefix or duplicated/missed terminal facts")
	}
	appendID := recoveryAppendID(session, "turn-kill", item)
	lineage := before[1].CommandID
	if kind == "manual_compaction" {
		appendID = recoveryCompactionAppendID(session, "compaction-kill")
	}
	for i, event := range want {
		got := after[len(before)+i]
		if !reflect.DeepEqual(got.Event, event) || got.ID != recoveryEventID(appendID, i) || got.CommandID != lineage || !got.OccurredAt.Equal(testTime) {
			t.Fatalf("wrong deterministic recovery record %d: %+v", i, got)
		}
	}
	state, err := domain.Replay(after)
	if err != nil || state.ActiveTurn != nil || state.ContextCompaction != nil {
		t.Fatalf("recovered state invalid: %v", err)
	}
	if kind != "manual_compaction" {
		digest, err := application.DigestRunTurnRequestV1(session, "synthetic")
		if err != nil {
			t.Fatal(err)
		}
		result, err := application.ReconstructRequestResult(application.CommandRequestRecord{RunTurnRequestID: "request-kill", RequestDigest: digest, SessionID: session, CommandID: lineage, TurnID: "turn-kill", ItemID: "assistant-kill", AdmissionAppendID: domain.AppendID(string(session) + "-seed-turn")}, after)
		if err != nil || result.Status != domain.TurnStatusInterrupted || !result.TerminalCommitted {
			t.Fatalf("recovered request cannot be reconstructed: %v", err)
		}
	}
}

func runRecoveryProcess(t *testing.T, path, side string, kill bool, fixtureEnv ...string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRecoveryProcessHelper$", "-test.timeout=12s")
	cmd.Env = append(os.Environ(), "OCH_RECOVERY_KILL_DB="+path, "OCH_RECOVERY_KILL_SIDE="+side)
	cmd.Env = append(cmd.Env, fixtureEnv...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	hit := make(chan struct{}, 1)
	done := make(chan string, 1)
	go func() {
		var output strings.Builder
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if output.Len() < 16<<10 {
				output.WriteString(line + "\n")
			}
			if line == "RECOVERY_COMMIT_BOUNDARY" {
				select {
				case hit <- struct{}{}:
				default:
				}
			}
		}
		if err := scanner.Err(); err != nil {
			output.WriteString(err.Error())
		}
		done <- output.String()
	}()
	select {
	case <-hit:
	case output := <-done:
		t.Fatalf("recovery exited before boundary: %s", output)
	case <-ctx.Done():
		t.Fatal("recovery did not reach boundary")
	}
	if kill {
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := io.WriteString(stdin, "continue\n"); err != nil {
			t.Fatal(err)
		}
	}
	output := <-done
	err = cmd.Wait()
	if !kill {
		if err != nil {
			t.Fatalf("recovery control failed: %v\n%s", err, output)
		}
		return
	}
	var exited *exec.ExitError
	if !errors.As(err, &exited) {
		t.Fatalf("recovery child was not killed: %v", err)
	}
	if goruntime.GOOS != "windows" {
		status, ok := exited.Sys().(syscall.WaitStatus)
		if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
			t.Fatalf("wrong process death: %v", exited.ProcessState)
		}
	}
}

func TestRecoveryProcessHelper(t *testing.T) {
	path, side := os.Getenv("OCH_RECOVERY_KILL_DB"), os.Getenv("OCH_RECOVERY_KILL_SIDE")
	if path == "" {
		t.Skip("subprocess fixture only")
	}
	ctx := context.Background()
	store, err := sqlite.Open(ctx, recoveryProcessConfig(path, "recovering").SQLite)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	positiveEnv := func(name string) int {
		value := os.Getenv(name)
		if value == "" {
			return 1
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			t.Fatalf("invalid fixture %s", name)
		}
		return n
	}
	stopAt, wantCandidates := positiveEnv("OCH_RECOVERY_KILL_AT"), positiveEnv("OCH_RECOVERY_CANDIDATES")
	hits := 0
	hook := func() {
		hits++
		if hits != stopAt {
			return
		}
		fmt.Println("RECOVERY_COMMIT_BOUNDARY")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil || line != "continue\n" {
			t.Fatalf("bad release: %v", err)
		}
	}
	switch side {
	case "before_publish":
		store.SetCommitHook("before_publish", hook)
	case "after_publish":
		store.SetCommitHook("after_publish", hook)
	default:
		t.Fatal("unknown boundary")
	}
	// Exactly Launch's startup reconciler and candidate enumeration, before
	// readiness/heartbeat/exporter construction. This local test seam avoids a
	// production startup callback solely for fault injection.
	candidates, recovered, err := reconcileAll(ctx, &reconciler{store: store, authority: store}, store)
	if err != nil || candidates != wantCandidates || recovered != wantCandidates || hits != wantCandidates || stopAt > hits {
		t.Fatalf("reconciliation control: candidates=%d recovered=%d hooks=%d err=%v", candidates, recovered, hits, err)
	}
	if err := store.ReleaseLease(ctx); err != nil {
		t.Fatal(err)
	}
}
