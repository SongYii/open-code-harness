package composition

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/sqlite"
	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/runtime"
	"github.com/SongYii/open-code-harness/internal/harness/testkit"
)

const crashInput = "Create the synthetic effect.txt file once."
const crashEffect = "synthetic committed side effect"
const crashKeyEnv = "OCH_PROCESS_CRASH_FIXTURE_KEY"
const crashPrefix = "OCH_CRASH_CHECKPOINT "

type crashCheckpoint struct {
	Session  domain.SessionID
	Baseline int
	Boundary string
	Config   Config
}

type crashedMessages struct {
	checkpoint crashCheckpoint
	records    []domain.RecordedEvent
	effect     os.FileInfo
}

// Each kill is issued by the parent only after the child reaches a real SQLite
// commit hook. No Close, cancellation, lease edit or fabricated history stands
// in for process death. Controls release the SAME hook and must complete.
func TestMessagesProcessCrashRecovery(t *testing.T) {
	t.Setenv(crashKeyEnv, "local-fixture-only")
	boundaries := []string{"assistant_before_commit", "tool_before_execution", "tool_after_effect", "summary_before_commit", "summary_after_commit", "audit_before_export"}
	var crashed []crashedMessages
	// Kill all children before recovery so their real 30s leases age together.
	// This avoids both parallel global-env mutation and six serial lease waits.
	for _, boundary := range boundaries {
		for _, kill := range []bool{false, true} {
			root := t.TempDir()
			t.Run(fmt.Sprintf("%s/kill=%t", boundary, kill), func(t *testing.T) {
				point := runMessagesCrashChild(t, root, boundary, kill)
				records := readMessagesCrashDB(t, point.Config.DatabasePath, point.Session)
				if kill {
					assertMessagesCrashBoundary(t, point, records)
					// Death must not silently release the durable lease. Prove
					// takeover is refused before waiting for natural expiration.
					early, err := runtime.Launch(context.Background(), runtime.Config{SQLite: sqlite.Config{Path: point.Config.DatabasePath, RuntimeID: "crash-early-successor"}})
					if early != nil {
						_ = early.Shutdown(context.Background())
					}
					var held *runtime.ErrLeaseHeld
					if !errors.As(err, &held) || held.Owner != "crash-child" {
						t.Fatalf("dead child's live lease was bypassed: %v", err)
					}
					effect, err := os.Stat(filepath.Join(point.Config.WorkspaceRoot, "effect.txt"))
					if err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
					crashed = append(crashed, crashedMessages{point, records, effect})
				} else {
					assertMessagesCrashControl(t, point, records)
				}
			})
		}
	}
	for _, fixture := range crashed {
		t.Run(fixture.checkpoint.Boundary+"/recover", func(t *testing.T) {
			recoverMessagesCrash(t, fixture)
		})
	}
}

func runMessagesCrashChild(t *testing.T, root, boundary string, kill bool) crashCheckpoint {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestMessagesProcessCrashHelper$", "-test.timeout=20s")
	cmd.Env = append(os.Environ(), "OCH_CRASH_CHILD_ROOT="+root, "OCH_CRASH_CHILD_BOUNDARY="+boundary)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	lines := make(chan string, 1)
	outputDone := make(chan string, 1)
	go func() {
		var output strings.Builder
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if output.Len() < 32<<10 {
				output.WriteString(line + "\n")
			}
			if strings.HasPrefix(line, crashPrefix) {
				select {
				case lines <- strings.TrimPrefix(line, crashPrefix):
				default:
				}
			}
		}
		if err := scanner.Err(); err != nil {
			output.WriteString(err.Error())
		}
		outputDone <- output.String()
	}()
	var point crashCheckpoint
	select {
	case line := <-lines:
		if err := json.Unmarshal([]byte(line), &point); err != nil {
			t.Fatal(err)
		}
	case output := <-outputDone:
		_ = cmd.Wait()
		t.Fatalf("child exited before checkpoint: %s", output)
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("child did not reach commit boundary")
	}
	if point.Boundary != boundary || point.Session == "" {
		t.Fatal("wrong checkpoint identity")
	}
	if kill {
		// Process.Kill is SIGKILL on Unix; the child cannot run deferred cleanup.
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
	} else if _, err := io.WriteString(stdin, "continue\n"); err != nil {
		t.Fatal(err)
	}
	output := <-outputDone
	err = cmd.Wait()
	if kill {
		var exited *exec.ExitError
		if !errors.As(err, &exited) || exited.Success() {
			t.Fatalf("child was not killed: %v\n%s", err, output)
		}
		// Unix reports the signal, so distinguish our SIGKILL from a panic,
		// test timeout or an ordinary nonzero exit. Windows uses TerminateProcess.
		if goruntime.GOOS != "windows" {
			status, ok := exited.Sys().(syscall.WaitStatus)
			if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatalf("child did not exit from SIGKILL: %v", exited.ProcessState)
			}
		}
	} else if err != nil {
		t.Fatalf("control failed: %v\n%s", err, output)
	}
	return point
}

func readMessagesCrashDB(t *testing.T, path string, session domain.SessionID) []domain.RecordedEvent {
	t.Helper()
	reader, err := sqlite.OpenReader(context.Background(), sqlite.ReaderConfig{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	page, err := reader.ReadStream(context.Background(), application.ReadStreamRequest{SessionID: session, Limit: 256})
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(page.Records)) != page.HeadVersion {
		t.Fatal("fixture exceeded pinned read bound")
	}
	return page.Records
}

func crashEventCount(records []domain.RecordedEvent, kind string) int {
	n := 0
	for _, record := range records {
		if record.Event.EventType() == kind {
			n++
		}
	}
	return n
}

func assertMessagesCrashBoundary(t *testing.T, point crashCheckpoint, records []domain.RecordedEvent) {
	t.Helper()
	if len(records) <= point.Baseline {
		t.Fatal("checkpoint has no new durable evidence")
	}
	if _, err := domain.Replay(records); err != nil {
		t.Fatal(err)
	}
	tail := records[point.Baseline:]
	wantAssistant, wantTool, wantSummary, wantTurn := 0, 0, 0, 0
	switch point.Boundary {
	case "assistant_before_commit":
		if crashEventCount(tail, domain.EventModelRequestRecorded) != 1 {
			t.Fatal("model request was not persisted")
		}
	case "tool_before_execution", "tool_after_effect":
		wantAssistant, wantTool = 1, 1
	case "summary_before_commit", "summary_after_commit":
		if crashEventCount(tail, domain.EventContextCompactionStarted) != 1 {
			t.Fatal("summary was not started")
		}
		if point.Boundary == "summary_after_commit" {
			wantSummary = 1
		}
	case "audit_before_export":
		wantAssistant, wantTurn = 1, 1
	}
	for kind, want := range map[string]int{
		domain.EventAssistantMessageCompleted:  wantAssistant,
		domain.EventToolCallStarted:            wantTool,
		domain.EventToolCallCompleted:          0,
		domain.EventContextCompactionCompleted: wantSummary,
		domain.EventTurnCompleted:              wantTurn,
	} {
		if got := crashEventCount(tail, kind); got != want {
			t.Fatalf("boundary %s: %s=%d want %d", point.Boundary, kind, got, want)
		}
	}
	assertCrashEffect(t, point.Config.WorkspaceRoot, point.Boundary == "tool_after_effect")
	if point.Boundary == "audit_before_export" {
		audit, err := VerifyAuditSnapshot(filepath.Join(filepath.Dir(point.Config.DatabasePath), "audit"))
		if err != nil {
			t.Fatal(err)
		}
		if len(audit.Sessions) != 1 || len(audit.Sessions[0].Events) != point.Baseline {
			t.Fatal("replica was not held at the baseline")
		}
	}
}

func assertCrashEffect(t *testing.T, workspace string, want bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workspace, "effect.txt"))
	if !want {
		if !os.IsNotExist(err) {
			t.Fatalf("unexpected tool side effect: %v", err)
		}
	} else if err != nil || string(data) != crashEffect {
		t.Fatalf("tool side effect missing or changed: %v", err)
	}
}

func assertMessagesCrashControl(t *testing.T, point crashCheckpoint, records []domain.RecordedEvent) {
	t.Helper()
	state, err := domain.Replay(records)
	if err != nil || state.ActiveTurn != nil || state.ContextCompaction != nil {
		t.Fatalf("control did not finish: %v", err)
	}
	tail := records[point.Baseline:]
	if strings.HasPrefix(point.Boundary, "summary_") {
		if crashEventCount(tail, domain.EventContextCompactionCompleted) != 1 {
			t.Fatal("control did not commit summary")
		}
	} else if crashEventCount(tail, domain.EventTurnCompleted) != 1 {
		t.Fatal("control did not complete turn")
	}
	tool := strings.HasPrefix(point.Boundary, "tool_")
	if tool && crashEventCount(tail, domain.EventToolCallCompleted) != 1 {
		t.Fatal("control did not complete exactly one tool")
	}
	assertCrashEffect(t, point.Config.WorkspaceRoot, tool)
	if !strings.HasPrefix(point.Boundary, "summary_") {
		config := point.Config
		config.Diagnostics = io.Discard
		config.RuntimeID = "control-replay"
		assembly, err := Open(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		defer assembly.Close()
		result, err := assembly.Service().RunTurn(context.Background(), application.RunTurnRequest{SessionID: point.Session, RequestID: "crash-request", Input: crashInput, Sink: &testkit.RecordingSink{}})
		if err != nil || result.Status != domain.TurnStatusCompleted || !result.TerminalCommitted {
			t.Fatalf("completed control cannot be replayed: %v", err)
		}
		if !reflect.DeepEqual(records, messagesLifecycleRecords(t, assembly.Store(), point.Session)) {
			t.Fatal("completed control retry appended new work")
		}
	}
}

func recoverMessagesCrash(t *testing.T, fixture crashedMessages) {
	t.Helper()
	point := fixture.checkpoint
	config := point.Config
	config.Diagnostics = io.Discard
	config.RuntimeID = "crash-successor"
	// The provider lived inside the killed process. Replaying a durable request
	// must not contact it. Only CreateSession is used for fresh work here.
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var assembly *Assembly
	for {
		var err error
		assembly, err = Open(ctx, config)
		if err == nil {
			break
		}
		var held *runtime.ErrLeaseHeld
		if !errors.As(err, &held) {
			t.Fatalf("successor launch: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("dead child's lease did not expire")
		case <-time.After(200 * time.Millisecond):
		}
	}
	defer assembly.Close()
	records := messagesLifecycleRecords(t, assembly.Store(), point.Session)
	if !reflect.DeepEqual(records[:min(len(records), len(fixture.records))], fixture.records) {
		t.Fatal("recovery changed committed history")
	}
	state, err := domain.Replay(records)
	if err != nil || state.ActiveTurn != nil || state.ContextCompaction != nil {
		t.Fatalf("recovery left active/invalid work: %v", err)
	}
	wantNew := 2
	if point.Boundary == "summary_before_commit" {
		wantNew = 1
	}
	if point.Boundary == "summary_after_commit" || point.Boundary == "audit_before_export" {
		wantNew = 0
	}
	if len(records) != len(fixture.records)+wantNew {
		t.Fatalf("recovery appended %d events, want %d", len(records)-len(fixture.records), wantNew)
	}
	for _, record := range records[len(fixture.records):] {
		switch e := record.Event.(type) {
		case domain.AssistantMessageInterrupted:
			if e.Code != "process_crash" || strings.HasPrefix(point.Boundary, "tool_") {
				t.Fatal("wrong assistant recovery")
			}
		case domain.ToolCallInterrupted:
			if e.Code != "process_crash" || e.CallID != "crash-tool" {
				t.Fatal("wrong tool recovery identity")
			}
		case domain.TurnInterrupted:
			if e.Reason != "process_crash" {
				t.Fatal("wrong turn recovery reason")
			}
		case domain.ContextCompactionFailed:
			if e.Code != "runtime_recovered" {
				t.Fatal("wrong summary recovery reason")
			}
		default:
			t.Fatalf("recovery invented %s", record.Event.EventType())
		}
	}
	if !strings.HasPrefix(point.Boundary, "summary_") {
		result, err := assembly.Service().RunTurn(ctx, application.RunTurnRequest{SessionID: point.Session, RequestID: "crash-request", Input: crashInput, Sink: &testkit.RecordingSink{}})
		want := domain.TurnStatusInterrupted
		if point.Boundary == "audit_before_export" {
			want = domain.TurnStatusCompleted
		}
		validError := err == nil
		if want == domain.TurnStatusInterrupted {
			var interrupted *application.Error
			validError = errors.As(err, &interrupted) && interrupted.Category == application.CategoryCanceled && interrupted.Code == "process_crash" && interrupted.TerminalCommitted
		}
		if !validError || result.Status != want || !result.TerminalCommitted {
			t.Fatalf("durable retry: status=%s terminal=%t err=%v", result.Status, result.TerminalCommitted, err)
		}
		if !reflect.DeepEqual(records, messagesLifecycleRecords(t, assembly.Store(), point.Session)) {
			t.Fatal("retry appended or re-executed work")
		}
	}
	assertCrashEffect(t, config.WorkspaceRoot, fixture.effect != nil)
	if fixture.effect != nil {
		after, err := os.Stat(filepath.Join(config.WorkspaceRoot, "effect.txt"))
		if err != nil || !os.SameFile(fixture.effect, after) || !after.ModTime().Equal(fixture.effect.ModTime()) {
			t.Fatal("recovery rewrote the tool side effect")
		}
	}
	store, err := assembly.host.Store()
	if err != nil {
		t.Fatal(err)
	}
	export := sqlite.ExportConfig{Directory: filepath.Join(filepath.Dir(config.DatabasePath), "audit")}
	if _, err := store.ExportOnce(ctx, export); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExportOnce(ctx, export); err != nil {
		t.Fatal(err)
	}
	if err := assembly.Close(); err != nil {
		t.Fatal(err)
	}
	audit, err := VerifyAuditSnapshot(export.Directory)
	if err != nil || len(audit.Sessions) != 1 || !reflect.DeepEqual(audit.Sessions[0].Events, records) {
		t.Fatalf("audit does not match canonical recovery: %v", err)
	}
	// A second restart must neither repeat recovery nor change checkpoint bytes.
	again, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if !reflect.DeepEqual(records, messagesLifecycleRecords(t, again.Store(), point.Session)) {
		t.Fatal("second restart duplicated recovery")
	}
	if _, err := again.Service().CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot}); err != nil {
		t.Fatalf("successor cannot commit fresh work: %v", err)
	}
}
