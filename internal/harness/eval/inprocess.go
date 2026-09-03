package eval

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/application"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/engine"
	"github.com/SongYii/open-code-harness/internal/harness/policy"
	"github.com/SongYii/open-code-harness/internal/harness/redact"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// InProcessCapabilities are the Scenario action types the in-process
// executor's RunAttempt actually drives without a special derived
// capability (design §9's capability check refuses to pair a Scenario
// requiring more than this with this executor before any Attempt is
// created). restart's clean_shutdown mode is supported by every executor
// (design §7) and so needs no capability entry; interrupt/kill are
// ACP-subprocess-only and are rejected by Scenario.DerivedRequiredCapabilities
// pairing against this executor's Capabilities, which never includes them.
var InProcessCapabilities = []string{"prompt", "compact", "collect"}

// maxOutcomeMessageBytes bounds Outcome.Message (design §13: "bounded safe
// message"). There is no design-given exact number for this bound, so this
// package picks one generous enough for a useful diagnostic and applies the
// existing redaction policy before truncation, matching design §10's
// "existing redaction policy" requirement for published fields.
const maxOutcomeMessageBytes = 2048

// discardSink drops every runtime event. Design §15: "A Scenario request
// sink may collect bounded live diagnostics, but canonical scoring
// evidence comes from transcript/audit/workspace after shutdown" -- bounded
// live diagnostic collection is optional and not implemented in this slice;
// canonical evidence never depends on it.
type discardSink struct{}

func (discardSink) Emit(context.Context, engine.RuntimeEvent) error { return nil }

// BuildConfig maps a validated Subject, one Attempt's isolated directories
// (design §8), a specific launch's runtime ID, and its scripted Approver
// into one composition.Config (design §15/§16). Absolute paths are Attempt
// facts, never Subject identity (design §10): they come from directories,
// never from subject. runtimeID is the caller's responsibility so a
// clean_shutdown restart (design §16) can launch its successor Assembly
// under a distinct runtime ID without BuildConfig needing to know anything
// about launch ordinals itself.
func BuildConfig(subject Subject, directories AttemptRootDirectories, runtimeID string, approver tools.Approver) (composition.Config, error) {
	if err := subject.Validate(); err != nil {
		return composition.Config{}, fmt.Errorf("eval: build config: %w", err)
	}
	if !hasText(runtimeID) {
		return composition.Config{}, fmt.Errorf("eval: build config: runtimeID is required")
	}
	return composition.Config{
		WorkspaceRoot:  directories.Workspace,
		DatabasePath:   filepath.Join(directories.Database, "harness.db"),
		RuntimeID:      runtimeID,
		AuditDirectory: directories.Audit,
		Provider: composition.Provider{
			BaseURL:               subject.Provider.NormalizedEndpoint,
			ModelID:               subject.Provider.ModelID,
			APIKeyEnv:             subject.Provider.CredentialEnvVar,
			ContextWindow:         subject.Provider.ContextWindow,
			MaxOutput:             subject.Provider.MaxOutput,
			AllowInsecureLoopback: subject.Provider.Lane == ProviderLaneFixture,
		},
		Policy: policy.Mode(subject.Policy.Mode),
		Limits: composition.Limits{
			MaxSteps:            subject.Policy.Limits.MaxSteps,
			MaxToolCallsPerStep: subject.Policy.Limits.MaxToolCallsPerStep,
			MaxAssistantBytes:   subject.Policy.Limits.MaxAssistantBytes,
			ApprovalTimeout:     subject.Policy.Limits.ApprovalTimeout,
		},
		Context: composition.Context{
			TriggerPercent:                 subject.Context.TriggerPercent,
			TargetPercent:                  subject.Context.TargetPercent,
			TailPercent:                    subject.Context.TailPercent,
			MaxSummaryChunks:               subject.Context.MaxSummaryChunks,
			MaxOverflowCompactionsPerTurn:  subject.Context.MaxOverflowCompactionsPerTurn,
			MaxPrunedToolResultsPerRequest: subject.Context.MaxPrunedToolResultsPerRequest,
			CompactionTimeout:              subject.Context.CompactionTimeout,
		},
		Approver:             approver,
		AllowUnsandboxedExec: subject.Policy.SandboxPolicy == SandboxPolicyUnsandboxedAllowed,
	}, nil
}

// launchRuntimeID derives one launch's distinct runtime ID from the Attempt
// and a monotonic launch ordinal (design §16): the initial Assembly is
// launch 0; a clean_shutdown restart's successor Assembly is launch 1, and
// so on. An ACP writer, its successor, and a compactor must never share a
// runtime ID (design §16); this in-process executor has no compactor of
// its own process, but the same one-ID-per-launch discipline still applies
// to its own reopen.
func launchRuntimeID(attemptID AttemptID, ordinal int) string {
	return fmt.Sprintf("%s-launch-%d", attemptID, ordinal)
}

// ExecutionOutcome is what running a Scenario's ordered actions in-process
// produced. SessionID is exposed so a caller can pass it to
// composition.ExportSession/composition.ExportEvaluationEvidence for
// transcript evidence once the Assembly has closed; RunAttempt does not
// collect evidence itself (design §14: both executors collect evidence
// "only after their writer has stopped"). WriterStopped is that stopped-
// writer proof: true only when the Assembly active at the end of this
// Attempt's execution was closed with a nil error. A caller must not
// attempt evidence collection when WriterStopped is false -- the cold
// evaluation APIs (design §14) already refuse a live lease, but a
// well-behaved caller should not even try.
type ExecutionOutcome struct {
	SessionID     string
	WriterStopped bool
	Outcome       Outcome
}

// executionState is the mutable state RunAttempt threads through its
// action loop: the currently live Assembly/Service (reassigned by a
// clean_shutdown restart, design §16), a monotonic launch ordinal, and any
// prompt actions currently running asynchronously because a later action in
// the same Scenario cancels them.
type executionState struct {
	assembly      *composition.Assembly
	service       *application.Service
	sessionID     domain.SessionID
	config        composition.Config
	launchOrdinal int
	turnCount     int
	pending       map[ActionID]*pendingPrompt
}

type pendingPrompt struct {
	cancel context.CancelFunc
	done   chan promptResult
}

type promptResult struct {
	result application.RunTurnResult
	err    error
}

// RunAttempt drives one Attempt's Scenario actions in-process (design §15):
// it calls composition.Open, creates one Session through
// Service.CreateSession, and drives prompt/compact/cancel/collect actions
// and clean_shutdown restarts through public Application/Composition
// methods only -- never Engine, Provider, Context Engine, Store, or an
// adapter directly. matcher is this Attempt's compiled ApprovalScript
// (design §7); RunAttempt wires it into every launched Assembly's Approver
// via BuildConfig/NewApprover, resetting its per-prompt ordinal with
// BeginPrompt before each prompt action.
//
// A Scenario action targeting a prompt with cancel runs that prompt
// asynchronously so the following cancel action can actually interrupt it
// mid-flight; cancellation itself is absorbed as the Scenario's own
// declared behavior; and does not terminate the Attempt on its own.
// interrupt/kill restart modes are rejected as unsupported (ACP-subprocess-
// only, design §7); a clean_shutdown restart explicitly closes the current
// Assembly, checks its result, and only reopens a fresh Assembly -- under a
// new launch's distinct runtime ID -- and loads the same Session once that
// closure is proven; no path here ever drops an Assembly by abandoning it.
//
// RunAttempt returns a non-nil error only when nothing durable happened yet
// -- an invalid Scenario, a nil ctx, or composition.Open/CreateSession
// failing before any action ran. Once at least one action has run, every
// further problem is recorded only in the returned Outcome -- err is nil --
// because design §13 requires Outcome, not a Go error, to carry execution/
// collection classification.
func RunAttempt(ctx context.Context, attemptID AttemptID, subject Subject, directories AttemptRootDirectories, scenario Scenario, matcher *ApprovalMatcher) (ExecutionOutcome, error) {
	if ctx == nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run attempt: context is required")
	}
	if err := scenario.Validate(); err != nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run attempt: %w", err)
	}

	started := time.Now().UTC()
	state := &executionState{pending: make(map[ActionID]*pendingPrompt)}

	config, err := BuildConfig(subject, directories, launchRuntimeID(attemptID, state.launchOrdinal), NewApprover(matcher))
	if err != nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run attempt: %w", err)
	}
	state.config = config

	assembly, err := composition.Open(ctx, config)
	if err != nil {
		return ExecutionOutcome{WriterStopped: true, Outcome: infraFailedOutcome(attemptID, started, "composition_open_failed", err)}, err
	}
	state.assembly = assembly
	state.service = assembly.Service()

	created, err := state.service.CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
	if err != nil {
		stopped, _ := closeAssembly(state.assembly)
		return ExecutionOutcome{WriterStopped: stopped, Outcome: infraFailedOutcome(attemptID, started, "create_session_failed", err)}, err
	}
	state.sessionID = created.SessionID

	// Pre-scan: which prompt actions have a later cancel action targeting
	// them. Only those launch asynchronously; every other prompt runs
	// synchronously exactly as before.
	hasCanceller := make(map[ActionID]bool)
	for _, action := range scenario.Actions {
		if action.Type == ActionCancel {
			hasCanceller[action.Cancel.TargetActionID] = true
		}
	}

	for _, action := range scenario.Actions {
		outcome, terminal := runAction(ctx, state, action, hasCanceller, matcher, attemptID, started)
		if terminal {
			drainPending(state)
			stopped, _ := closeAssembly(state.assembly)
			return ExecutionOutcome{SessionID: string(state.sessionID), WriterStopped: stopped, Outcome: outcome}, nil
		}
	}
	drainPending(state)

	outcome := Outcome{
		FormatVersion:    FormatVersion,
		Schema:           SchemaOutcome,
		AttemptID:        attemptID,
		Status:           OutcomeCompleted,
		Code:             "ok",
		Message:          "every scenario action completed",
		StartedAt:        started,
		EndedAt:          time.Now().UTC(),
		CollectionStatus: CollectionNotStarted,
	}
	if session, loadErr := state.service.LoadSession(ctx, state.sessionID); loadErr == nil {
		outcome.TerminalSession = &TerminalSessionFacts{
			SessionID: string(state.sessionID),
			TurnCount: state.turnCount,
			Open:      session.Status == domain.SessionStatusActive,
			Running:   session.ActiveTurn != nil,
		}
	}
	stopped, _ := closeAssembly(state.assembly)
	return ExecutionOutcome{SessionID: string(state.sessionID), WriterStopped: stopped, Outcome: outcome}, nil
}

// drainPending cancels and waits for every prompt action still running
// asynchronously, regardless of how RunAttempt is about to return. A
// validated Scenario's every canceler-targeted prompt is expected to have
// already been resolved by its matching cancel action in the main loop;
// this is the safety net for an early-terminating path that never reached
// that cancel action, so no goroutine is ever leaked and no evaluation
// evidence collection can race a still-running Turn.
func drainPending(state *executionState) {
	for id, pending := range state.pending {
		pending.cancel()
		<-pending.done
		delete(state.pending, id)
	}
}

// closeAssembly closes assembly and reports whether the writer was
// provably stopped -- true only when Close returned nil, or assembly was
// already nil (composition.Open itself never returns a non-nil Assembly
// with a non-nil error, so a nil assembly means nothing was ever opened).
func closeAssembly(assembly *composition.Assembly) (bool, error) {
	if assembly == nil {
		return true, nil
	}
	if err := assembly.Close(); err != nil {
		return false, err
	}
	return true, nil
}

// runAction drives one action and reports whether it terminated the
// Attempt. A prompt action with a later canceler launches asynchronously
// and never terminates on its own; its resolution happens when the loop
// reaches the matching cancel action.
func runAction(ctx context.Context, state *executionState, action ScenarioAction, hasCanceller map[ActionID]bool, matcher *ApprovalMatcher, attemptID AttemptID, started time.Time) (outcome Outcome, terminal bool) {
	switch action.Type {
	case ActionPrompt:
		matcher.BeginPrompt(action.ID)
		if hasCanceller[action.ID] {
			return startAsyncPrompt(ctx, state, action, attemptID, started)
		}
		return runSyncPrompt(ctx, state, action, attemptID, started)
	case ActionCancel:
		return runCancel(state, action, attemptID, started)
	case ActionCompact:
		if _, err := state.service.CompactSession(ctx, application.CompactSessionRequest{
			SessionID: state.sessionID,
			Strategy:  action.Compact.Strategy,
			Focus:     action.Compact.Focus,
		}); err != nil {
			return infraFailedOutcome(attemptID, started, "compact_session_failed", err), true
		}
		return Outcome{}, false
	case ActionCollect:
		// The declared workspace path or verifier fact is validated and
		// captured by evidence collection after shutdown (design §14), not
		// during live execution.
		return Outcome{}, false
	case ActionRestart:
		return runRestart(ctx, state, action, attemptID, started)
	default:
		return Outcome{
			FormatVersion:    FormatVersion,
			Schema:           SchemaOutcome,
			AttemptID:        attemptID,
			Status:           OutcomeInfraFailed,
			Code:             "unsupported_action",
			Message:          boundedRedactedMessage(fmt.Sprintf("the in-process executor does not yet drive %q actions", action.Type)),
			StartedAt:        started,
			EndedAt:          time.Now().UTC(),
			CollectionStatus: CollectionNotStarted,
		}, true
	}
}

func runSyncPrompt(ctx context.Context, state *executionState, action ScenarioAction, attemptID AttemptID, started time.Time) (Outcome, bool) {
	requestID, err := NewGeneratedID()
	if err != nil {
		return infraFailedOutcome(attemptID, started, "generate_request_id_failed", err), true
	}
	result, err := state.service.RunTurn(ctx, application.RunTurnRequest{
		SessionID: state.sessionID,
		RequestID: domain.RunTurnRequestID(requestID),
		Input:     action.Prompt.Text,
		Sink:      discardSink{},
	})
	if err != nil {
		return classifyRunTurnFailure(ctx, attemptID, started, result, err), true
	}
	if result.Status != domain.TurnStatusCompleted {
		return subjectFailedOutcome(attemptID, started, "turn_not_completed", result.Status, state.sessionID), true
	}
	state.turnCount++
	return Outcome{}, false
}

// startAsyncPrompt launches a prompt action's RunTurn in a goroutine under
// its own cancelable context, so the loop can continue on to a later cancel
// action without waiting for this Turn to finish. It never itself
// terminates the Attempt; runCancel resolves it.
func startAsyncPrompt(ctx context.Context, state *executionState, action ScenarioAction, attemptID AttemptID, started time.Time) (Outcome, bool) {
	promptCtx, cancel := context.WithCancel(ctx)
	done := make(chan promptResult, 1)
	requestID, err := NewGeneratedID()
	if err != nil {
		cancel()
		return infraFailedOutcome(attemptID, started, "generate_request_id_failed", err), true
	}
	go func() {
		result, runErr := state.service.RunTurn(promptCtx, application.RunTurnRequest{
			SessionID: state.sessionID,
			RequestID: domain.RunTurnRequestID(requestID),
			Input:     action.Prompt.Text,
			Sink:      discardSink{},
		})
		done <- promptResult{result: result, err: runErr}
	}()
	state.pending[action.ID] = &pendingPrompt{cancel: cancel, done: done}
	return Outcome{}, false
}

// runCancel cancels the named prompt's context and waits for its durable
// terminal result (design §7: "cancellation cancels the named in-flight
// prompt, waits for its durable terminal result, then continues ...
// according to the Scenario boundary"). The interruption is the Scenario's
// own declared behavior, not a failure this executor detected, so a
// successfully observed cancellation -- proven by the channel receive
// returning at all -- never terminates the Attempt on its own, regardless
// of whether the canceled Turn's own RunTurn call returned an error.
func runCancel(state *executionState, action ScenarioAction, attemptID AttemptID, started time.Time) (Outcome, bool) {
	target := action.Cancel.TargetActionID
	pending, ok := state.pending[target]
	if !ok {
		return infraFailedOutcome(attemptID, started, "cancel_target_not_pending",
			fmt.Errorf("no in-flight prompt %q to cancel", target)), true
	}
	pending.cancel()
	<-pending.done
	delete(state.pending, target)
	state.turnCount++
	return Outcome{}, false
}

// runRestart implements clean_shutdown (design §16): explicitly close the
// current Assembly, check its result, and only then reopen a fresh Assembly
// under a new launch's distinct runtime ID and load the same Session.
// interrupt/kill are ACP-subprocess-only (design §7) and are rejected here
// rather than emulated; a Scenario that declares them should already have
// been refused before this Attempt was created by matrix expansion's
// capability check (design §9), so reaching this branch at all reflects a
// runner-level gap, not Subject behavior.
func runRestart(ctx context.Context, state *executionState, action ScenarioAction, attemptID AttemptID, started time.Time) (Outcome, bool) {
	if action.Restart.Mode != RestartModeCleanShutdown {
		return Outcome{
			FormatVersion: FormatVersion, Schema: SchemaOutcome, AttemptID: attemptID,
			Status: OutcomeInfraFailed, Code: "unsupported_restart_mode",
			Message: boundedRedactedMessage(fmt.Sprintf(
				"the in-process executor supports only %q, not %q", RestartModeCleanShutdown, action.Restart.Mode)),
			StartedAt: started, EndedAt: time.Now().UTC(), CollectionStatus: CollectionNotStarted,
		}, true
	}

	stopped, closeErr := closeAssembly(state.assembly)
	if !stopped {
		return infraFailedOutcome(attemptID, started, "restart_shutdown_unproven", closeErr), true
	}
	state.assembly = nil
	state.launchOrdinal++

	newConfig := state.config
	newConfig.RuntimeID = launchRuntimeID(attemptID, state.launchOrdinal)
	assembly, err := composition.Open(ctx, newConfig)
	if err != nil {
		return infraFailedOutcome(attemptID, started, "restart_reopen_failed", err), true
	}
	if _, err := assembly.Service().LoadSession(ctx, state.sessionID); err != nil {
		_, _ = closeAssembly(assembly)
		return infraFailedOutcome(attemptID, started, "restart_load_session_failed", err), true
	}

	state.config = newConfig
	state.assembly = assembly
	state.service = assembly.Service()
	return Outcome{}, false
}

func infraFailedOutcome(attemptID AttemptID, started time.Time, code string, err error) Outcome {
	return Outcome{
		FormatVersion:    FormatVersion,
		Schema:           SchemaOutcome,
		AttemptID:        attemptID,
		Status:           OutcomeInfraFailed,
		Code:             code,
		Message:          boundedRedactedMessage(err.Error()),
		StartedAt:        started,
		EndedAt:          time.Now().UTC(),
		CollectionStatus: CollectionNotStarted,
	}
}

func subjectFailedOutcome(attemptID AttemptID, started time.Time, code string, status domain.TurnStatus, sessionID domain.SessionID) Outcome {
	return Outcome{
		FormatVersion: FormatVersion,
		Schema:        SchemaOutcome,
		AttemptID:     attemptID,
		Status:        OutcomeSubjectFailed,
		Code:          code,
		Message:       boundedRedactedMessage(fmt.Sprintf("turn ended with status %q", status)),
		StartedAt:     started,
		EndedAt:       time.Now().UTC(),
		TerminalSession: &TerminalSessionFacts{
			SessionID: string(sessionID),
			Open:      true,
		},
		CollectionStatus: CollectionNotStarted,
	}
}

// classifyRunTurnFailure distinguishes a caller-imposed bound (design §19's
// Attempt wall time, delivered as ctx cancellation) from Subject/Provider
// behavior. When the ctx this executor was given is itself done, durable
// evidence cannot prove whether the Subject would have completed (design
// §13's indeterminate); otherwise the failure is the Subject's. This is
// only reached for a *synchronous* prompt's own ctx (RunAttempt's outer
// ctx); a canceled asynchronous prompt is resolved by runCancel instead,
// which treats interruption as the Scenario's own declared behavior rather
// than a failure to classify here.
func classifyRunTurnFailure(ctx context.Context, attemptID AttemptID, started time.Time, result application.RunTurnResult, err error) Outcome {
	base := Outcome{
		FormatVersion:    FormatVersion,
		Schema:           SchemaOutcome,
		AttemptID:        attemptID,
		StartedAt:        started,
		EndedAt:          time.Now().UTC(),
		CollectionStatus: CollectionNotStarted,
		Message:          boundedRedactedMessage(err.Error()),
	}
	if ctx.Err() != nil {
		base.Status = OutcomeIndeterminate
		base.Code = "context_ended_before_turn_completed"
	} else {
		base.Status = OutcomeSubjectFailed
		base.Code = "run_turn_failed"
	}
	if result.SessionID != "" {
		base.TerminalSession = &TerminalSessionFacts{
			SessionID: string(result.SessionID),
			Open:      true,
		}
	}
	return base
}

func boundedRedactedMessage(message string) string {
	return boundedString(redact.Text(message), maxOutcomeMessageBytes)
}

func boundedString(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	truncated := value[:maxBytes]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + "…(truncated)"
}
