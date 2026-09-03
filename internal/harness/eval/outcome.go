package eval

import (
	"fmt"
	"time"
)

// OutcomeStatus classifies execution and evidence collection, never
// behavioral quality (design §4/§13).
type OutcomeStatus string

const (
	// OutcomeCompleted means the executor reached the Scenario boundary and
	// required collection completed.
	OutcomeCompleted OutcomeStatus = "completed"
	// OutcomeSubjectFailed means OCH/provider/tool/protocol behavior
	// failed, but runner authority and evidence classification remain
	// sound.
	OutcomeSubjectFailed OutcomeStatus = "subject_failed"
	// OutcomeInfraFailed means fixture, spawn, storage, runner, host, or
	// required collection infrastructure failed.
	OutcomeInfraFailed OutcomeStatus = "infra_failed"
	// OutcomeIndeterminate means durable evidence cannot prove whether
	// Subject or infrastructure owns the failure.
	OutcomeIndeterminate OutcomeStatus = "indeterminate"
)

// CollectionStatus describes how much of the required evidence collection
// completed for an Attempt (design §13).
type CollectionStatus string

const (
	CollectionComplete   CollectionStatus = "complete"
	CollectionPartial    CollectionStatus = "partial"
	CollectionNotStarted CollectionStatus = "not_started"
)

// TerminalSessionFacts are the terminal Session/Turn facts Outcome records
// when known (design §13).
type TerminalSessionFacts struct {
	SessionID string `json:"sessionId"`
	TurnCount int    `json:"turnCount"`
	Open      bool   `json:"open"`
	Running   bool   `json:"running"`
}

// Outcome is the frozen `och.eval.outcome` document: execution
// classification and collection status, written once, atomically, after
// execution or recovery (design §12/§13/§18) and never mutated afterward.
type Outcome struct {
	FormatVersion int       `json:"formatVersion"`
	Schema        string    `json:"schema"`
	AttemptID     AttemptID `json:"attemptId"`

	Status  OutcomeStatus `json:"status"`
	Code    string        `json:"code"`
	Message string        `json:"message"`

	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt"`

	// TerminalSession is nil when the terminal Session/Turn state could
	// not be determined (design §13: "terminal Session/Turn facts when
	// known").
	TerminalSession *TerminalSessionFacts `json:"terminalSession,omitempty"`

	// LimitsHit names every limit (design §19) whose enforcement affected
	// this Attempt.
	LimitsHit []string `json:"limitsHit,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`

	CollectionStatus CollectionStatus `json:"collectionStatus"`

	// Recovered is true only when this Outcome was published by crash
	// recovery (design §18) rather than normal execution.
	Recovered bool `json:"recovered"`
}

// Duration is EndedAt minus StartedAt. It is derived rather than stored so
// it can never disagree with the two timestamps it is computed from.
func (outcome Outcome) Duration() time.Duration {
	return outcome.EndedAt.Sub(outcome.StartedAt)
}

// DecodeOutcome strictly decodes and validates one `och.eval.outcome`
// document (design §6).
func DecodeOutcome(data []byte) (Outcome, error) {
	var outcome Outcome
	if err := decodeStrict(data, &outcome); err != nil {
		return Outcome{}, fmt.Errorf("eval: outcome: %w", err)
	}
	if outcome.Schema != SchemaOutcome {
		return Outcome{}, fmt.Errorf("eval: outcome: %w: %q", errUnsupportedSchema, outcome.Schema)
	}
	if outcome.FormatVersion != FormatVersion {
		return Outcome{}, fmt.Errorf("eval: outcome: %w: %d", errUnsupportedFormatVersion, outcome.FormatVersion)
	}
	if err := outcome.Validate(); err != nil {
		return Outcome{}, err
	}
	return outcome, nil
}

// Validate checks every field this document requires.
func (outcome Outcome) Validate() error {
	if _, err := ParseAttemptID(string(outcome.AttemptID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	switch outcome.Status {
	case OutcomeCompleted, OutcomeSubjectFailed, OutcomeInfraFailed, OutcomeIndeterminate:
	default:
		return fmt.Errorf("%w: unknown status %q", errInvalidDocument, outcome.Status)
	}
	if !hasText(outcome.Code) {
		return fmt.Errorf("%w: code is required", errInvalidDocument)
	}
	if outcome.StartedAt.IsZero() {
		return fmt.Errorf("%w: startedAt is required", errInvalidDocument)
	}
	if outcome.EndedAt.IsZero() {
		return fmt.Errorf("%w: endedAt is required", errInvalidDocument)
	}
	if outcome.EndedAt.Before(outcome.StartedAt) {
		return fmt.Errorf("%w: endedAt must not precede startedAt", errInvalidDocument)
	}
	if err := requireNonEmptyEntries("limitsHit", outcome.LimitsHit); err != nil {
		return err
	}
	switch outcome.CollectionStatus {
	case CollectionComplete, CollectionPartial, CollectionNotStarted:
	default:
		return fmt.Errorf("%w: unknown collectionStatus %q", errInvalidDocument, outcome.CollectionStatus)
	}
	if outcome.TerminalSession != nil && !hasText(outcome.TerminalSession.SessionID) {
		return fmt.Errorf("%w: terminalSession.sessionId is required when terminalSession is present", errInvalidDocument)
	}
	return nil
}
