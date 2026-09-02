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
)

// InProcessCapabilities are the Scenario action types the in-process
// executor's RunAttempt actually drives. A checked-in `och.eval.executor`
// document for `in_process` should declare exactly these (design §9's
// capability check refuses to pair a Scenario requiring more than this with
// this executor before any Attempt is created); `cancel` and `restart` are
// not implemented yet.
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

// BuildConfig maps a validated Subject and one Attempt's isolated
// directories (design §8) into one composition.Config (design §15).
// Absolute paths are Attempt facts, never Subject identity (design §10):
// they come from directories, never from subject.
func BuildConfig(subject Subject, directories AttemptRootDirectories, attemptID AttemptID) (composition.Config, error) {
	if err := subject.Validate(); err != nil {
		return composition.Config{}, fmt.Errorf("eval: build config: %w", err)
	}
	if _, err := ParseAttemptID(string(attemptID)); err != nil {
		return composition.Config{}, fmt.Errorf("eval: build config: %w", err)
	}
	return composition.Config{
		WorkspaceRoot:  directories.Workspace,
		DatabasePath:   filepath.Join(directories.Database, "harness.db"),
		RuntimeID:      string(attemptID),
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
		AllowUnsandboxedExec: subject.Policy.SandboxPolicy == SandboxPolicyUnsandboxedAllowed,
	}, nil
}

// ExecutionOutcome is what running a Scenario's ordered actions in-process
// produced. SessionID is exposed so a caller can pass it to
// composition.ExportSession for transcript evidence once the Assembly has
// closed; RunAttempt does not collect evidence itself (design §14: both
// executors collect evidence "only after their writer has stopped").
type ExecutionOutcome struct {
	SessionID string
	Outcome   Outcome
}

// RunAttempt drives one Attempt's Scenario actions in-process (design §15):
// it calls composition.Open, creates one Session through
// Service.CreateSession, and drives prompt/compact actions through
// RunTurn/CompactSession (collect is a no-op here; its evidence is
// collected separately after shutdown). It never calls Engine, Provider,
// Context Engine, Store, or an adapter directly, and it closes the Assembly
// on every path before returning.
//
// RunAttempt returns a non-nil error only when nothing durable happened yet
// -- an invalid Scenario, a nil ctx, or composition.Open/CreateSession
// failing before any action ran. Once at least one action has run, every
// further problem (including any RunTurn/CompactSession failure) is
// recorded only in the returned Outcome -- err is nil -- because design §13
// requires Outcome, not a Go error, to carry execution/collection
// classification.
func RunAttempt(ctx context.Context, attemptID AttemptID, config composition.Config, scenario Scenario) (ExecutionOutcome, error) {
	if ctx == nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run attempt: context is required")
	}
	if err := scenario.Validate(); err != nil {
		return ExecutionOutcome{}, fmt.Errorf("eval: run attempt: %w", err)
	}

	started := time.Now().UTC()

	assembly, err := composition.Open(ctx, config)
	if err != nil {
		outcome := infraFailedOutcome(attemptID, started, "composition_open_failed", err)
		return ExecutionOutcome{Outcome: outcome}, err
	}
	defer assembly.Close()

	created, err := assembly.Service().CreateSession(ctx, application.CreateSessionRequest{WorkspaceRoot: config.WorkspaceRoot})
	if err != nil {
		outcome := infraFailedOutcome(attemptID, started, "create_session_failed", err)
		return ExecutionOutcome{Outcome: outcome}, err
	}
	sessionID := created.SessionID

	turnCount := 0
	for _, action := range scenario.Actions {
		outcome, terminal, ranTurn := runAction(ctx, assembly.Service(), sessionID, action, attemptID, started)
		if ranTurn {
			turnCount++
		}
		if terminal {
			return ExecutionOutcome{SessionID: string(sessionID), Outcome: outcome}, nil
		}
	}

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
	if session, loadErr := assembly.Service().LoadSession(ctx, sessionID); loadErr == nil {
		outcome.TerminalSession = &TerminalSessionFacts{
			SessionID: string(sessionID),
			TurnCount: turnCount,
			Open:      session.Status == domain.SessionStatusActive,
			Running:   session.ActiveTurn != nil,
		}
	}
	return ExecutionOutcome{SessionID: string(sessionID), Outcome: outcome}, nil
}

// runAction drives one action and reports whether it terminated the Attempt
// (a failure or an unsupported action type) and, when it did not terminate,
// whether it was a Turn (so the caller can count completed Turns for the
// final TerminalSessionFacts).
func runAction(ctx context.Context, service *application.Service, sessionID domain.SessionID, action ScenarioAction, attemptID AttemptID, started time.Time) (outcome Outcome, terminal bool, ranTurn bool) {
	switch action.Type {
	case ActionPrompt:
		requestID, err := NewGeneratedID()
		if err != nil {
			return infraFailedOutcome(attemptID, started, "generate_request_id_failed", err), true, false
		}
		result, err := service.RunTurn(ctx, application.RunTurnRequest{
			SessionID: sessionID,
			RequestID: domain.RunTurnRequestID(requestID),
			Input:     action.Prompt.Text,
			Sink:      discardSink{},
		})
		if err != nil {
			return classifyRunTurnFailure(ctx, attemptID, started, result, err), true, false
		}
		if result.Status != domain.TurnStatusCompleted {
			return subjectFailedOutcome(attemptID, started, "turn_not_completed", result.Status, sessionID), true, false
		}
		return Outcome{}, false, true
	case ActionCompact:
		if _, err := service.CompactSession(ctx, application.CompactSessionRequest{
			SessionID: sessionID,
			Strategy:  action.Compact.Strategy,
			Focus:     action.Compact.Focus,
		}); err != nil {
			return infraFailedOutcome(attemptID, started, "compact_session_failed", err), true, false
		}
		return Outcome{}, false, false
	case ActionCollect:
		// The declared workspace path or verifier fact is validated and
		// captured by evidence collection after shutdown (design §14), not
		// during live execution.
		return Outcome{}, false, false
	default:
		outcome := Outcome{
			FormatVersion:    FormatVersion,
			Schema:           SchemaOutcome,
			AttemptID:        attemptID,
			Status:           OutcomeInfraFailed,
			Code:             "unsupported_action",
			Message:          boundedRedactedMessage(fmt.Sprintf("the in-process executor does not yet drive %q actions", action.Type)),
			StartedAt:        started,
			EndedAt:          time.Now().UTC(),
			CollectionStatus: CollectionNotStarted,
		}
		return outcome, true, false
	}
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
// §13's indeterminate); otherwise the failure is the Subject's.
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
