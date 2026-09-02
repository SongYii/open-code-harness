package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// errACPSubprocessUnsupportedOnWindows is returned (never on Unix, where
// acpProcessSupported is true) by RunACPAttempt and by
// acp_process_windows.go's own startACPProcess stub — the one error value
// both paths share, defined here rather than per-platform file so it
// compiles and resolves identically regardless of GOOS.
var errACPSubprocessUnsupportedOnWindows = errors.New("eval: acp_subprocess is not supported on this platform (Windows)")

// defaultACPStderrBytes bounds one launch's captured stderr (design §19's
// own stderr evidence bound; this package's own default until a caller
// supplies EvalSetLimits.StderrBytes through ACPLaunchConfig).
const defaultACPStderrBytes = 8 << 20

// ACPLaunchConfig is everything RunACPAttempt needs beyond the Scenario/
// Subject/Attempt facts RunAttempt itself takes (design §16): one
// resolved, hashed och binary shared across every Attempt in a run
// (ResolveACPBinary), and the bounded startup/shutdown/stderr limits this
// launch enforces.
type ACPLaunchConfig struct {
	Binary          ACPBinaryIdentity
	StderrLimit     int64
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
}

func (config ACPLaunchConfig) withDefaults() ACPLaunchConfig {
	if config.StderrLimit <= 0 {
		config.StderrLimit = defaultACPStderrBytes
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = DefaultProcessStartup
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = DefaultShutdownGrace
	}
	return config
}

// RunACPAttempt drives one Attempt's Scenario actions against a real,
// independently spawned och -acp subprocess (design §16), mirroring
// RunAttempt's own contract and ExecutionOutcome shape as closely as an
// entirely different execution surface allows: evidence collection
// (CollectEvidence) reads the same canonical SQLite database format
// either executor produces and needs no changes to drive an ACP Attempt.
//
// This task supports exactly the same baseline action set the in-process
// executor's own first slice did: prompt and collect. compact has no ACP
// wire method yet (Task 14's own scope); cancel and restart are Task
// 13's scope (the process-group escalation ladder and the non-interactive
// approval handler this function's own fail-closed placeholder defers
// to). Reaching any of those three action types here is recorded as
// infra_failed/unsupported_action, exactly like the in-process executor's
// own default branch for a genuinely unimplemented action.
//
// On Windows, RunACPAttempt refuses immediately (acpProcessSupported is
// false there) — no process is ever spawned.
func RunACPAttempt(ctx context.Context, attemptID AttemptID, subject Subject, directories AttemptRootDirectories, scenario Scenario, launch ACPLaunchConfig) (ExecutionOutcome, error) {
	if !acpProcessSupported {
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: %w", errACPSubprocessUnsupportedOnWindows)
	}
	if ctx == nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: context is required")
	}
	if err := scenario.Validate(); err != nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: %w", err)
	}
	launch = launch.withDefaults()
	started := time.Now().UTC()
	runtimeID := launchRuntimeID(attemptID, 0)

	argv, err := NormalizedArgv(subject)
	if err != nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: %w", err)
	}
	fullArgv := append([]string{
		"-acp",
		"-workspace", directories.Workspace,
		"-database", AttemptDatabasePath(directories),
		"-runtime-id", runtimeID,
		"-audit-dir", directories.Audit,
	}, argv...)

	env, err := BuildChildEnvironment(subject)
	if err != nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: %w", err)
	}

	process, err := startACPProcess(launch.Binary.Path, fullArgv, env, directories.Workspace, launch.StderrLimit)
	if err != nil {
		return ExecutionOutcome{WriterStopped: true, Outcome: infraFailedOutcome(attemptID, started, "acp_spawn_failed", err)}, err
	}

	conn := newACPConnection(process.stdout, process.stdin, acpFailClosedPermissionHandler)

	initCtx, cancel := context.WithTimeout(ctx, launch.StartupTimeout)
	_, err = conn.initialize(initCtx)
	cancel()
	if err != nil {
		closeAndReapACP(conn, process, launch.ShutdownTimeout)
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: initialize: %w", err)
	}

	sessionCtx, cancel := context.WithTimeout(ctx, launch.StartupTimeout)
	sessionID, err := conn.newSession(sessionCtx, directories.Workspace)
	cancel()
	if err != nil {
		closeAndReapACP(conn, process, launch.ShutdownTimeout)
		return ExecutionOutcome{}, fmt.Errorf("eval: run acp attempt: new session: %w", err)
	}

	var outcome Outcome
	terminal := false
	turnCount := 0
	for _, action := range scenario.Actions {
		outcome, terminal = runACPAction(ctx, conn, sessionID, action, attemptID, started)
		if terminal {
			break
		}
		if action.Type == ActionPrompt {
			turnCount++
		}
	}

	stopped := closeAndReapACP(conn, process, launch.ShutdownTimeout)
	if terminal {
		return ExecutionOutcome{SessionID: sessionID, WriterStopped: stopped, Outcome: outcome}, nil
	}

	completed := Outcome{
		FormatVersion:    FormatVersion,
		Schema:           SchemaOutcome,
		AttemptID:        attemptID,
		Status:           OutcomeCompleted,
		Code:             "ok",
		Message:          "every scenario action completed",
		StartedAt:        started,
		EndedAt:          time.Now().UTC(),
		CollectionStatus: CollectionNotStarted,
		TerminalSession: &TerminalSessionFacts{
			SessionID: sessionID,
			TurnCount: turnCount,
			Open:      true,
		},
	}
	return ExecutionOutcome{SessionID: sessionID, WriterStopped: stopped, Outcome: completed}, nil
}

// runACPAction drives one action over an already-initialized ACP session
// and reports whether it terminated the Attempt, mirroring
// inprocess.go's runAction shape for the subset of action types this task
// supports.
func runACPAction(ctx context.Context, conn *acpConnection, sessionID string, action ScenarioAction, attemptID AttemptID, started time.Time) (Outcome, bool) {
	switch action.Type {
	case ActionPrompt:
		stopReason, err := conn.prompt(ctx, sessionID, action.Prompt.Text)
		if err != nil {
			// This connection has no public way yet to distinguish a
			// well-formed session/prompt error response (a genuine Turn
			// failure) from a connection/process-level failure. Until
			// that distinction exists (Task 13's own scope, which also
			// grows the escalation/evidence surface this connection
			// would need it for), any error here is classified
			// conservatively as infra_failed rather than guessing which
			// it was.
			return infraFailedOutcome(attemptID, started, "acp_prompt_failed", err), true
		}
		if stopReason != acpStopReasonEndTurn {
			return Outcome{
				FormatVersion: FormatVersion, Schema: SchemaOutcome, AttemptID: attemptID,
				Status: OutcomeSubjectFailed, Code: "acp_turn_not_completed",
				Message:   boundedRedactedMessage(fmt.Sprintf("turn ended with stop reason %q", stopReason)),
				StartedAt: started, EndedAt: time.Now().UTC(), CollectionStatus: CollectionNotStarted,
				TerminalSession: &TerminalSessionFacts{SessionID: sessionID, Open: true},
			}, true
		}
		return Outcome{}, false
	case ActionCollect:
		// Declared workspace path or verifier fact is validated and
		// captured by evidence collection after shutdown (design §14),
		// identically to the in-process executor.
		return Outcome{}, false
	default:
		return Outcome{
			FormatVersion: FormatVersion, Schema: SchemaOutcome, AttemptID: attemptID,
			Status: OutcomeInfraFailed, Code: "unsupported_action",
			Message: boundedRedactedMessage(fmt.Sprintf(
				"the acp_subprocess executor does not yet drive %q actions", action.Type)),
			StartedAt: started, EndedAt: time.Now().UTC(), CollectionStatus: CollectionNotStarted,
		}, true
	}
}

const acpStopReasonEndTurn = "end_turn"

// closeAndReapACP implements design §16's normal shutdown: close stdin
// (the agent observes EOF and shuts down on its own, exactly like
// och -acp's own ServeACP loop), stop the connection's own read loop, and
// wait up to timeout for the process to actually exit and be reaped.
// stopped is true only once wait has actually returned — an Attempt's
// WriterStopped fact (and thus whether evidence collection may safely
// begin) is never set on anything weaker than that proof.
func closeAndReapACP(conn *acpConnection, process *acpProcess, timeout time.Duration) (stopped bool) {
	_ = process.stdin.Close()
	_ = conn.close()
	exited, _ := process.waitTimeout(timeout)
	return exited
}

// acpFailClosedPermissionHandler is RunACPAttempt's own placeholder
// session/request_permission handler: it denies every request by picking
// a reject-flavored option when the offered set has one, else the last-
// listed option (composition.Config's own "Approver unset becomes a deny
// slot" convention, applied identically here). Task 13 replaces this with
// the real ApprovalMatcher-driven handler that binds the current prompt
// action/ordinal and records sessionId/toolCallId/tool name/offered
// options/decision as bounded evidence.
func acpFailClosedPermissionHandler(_ context.Context, params json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if err := json.Unmarshal(params, &request); err != nil || len(request.Options) == 0 {
		return nil, fmt.Errorf("eval: acp fail-closed handler: malformed or empty session/request_permission params")
	}
	optionID := request.Options[len(request.Options)-1].OptionID
	for _, option := range request.Options {
		if option.Kind == "reject_once" || option.Kind == "reject" {
			optionID = option.OptionID
			break
		}
	}
	return json.Marshal(struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}{Outcome: struct {
		Outcome  string `json:"outcome"`
		OptionID string `json:"optionId"`
	}{Outcome: "selected", OptionID: optionID}})
}
