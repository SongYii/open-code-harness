package eval

import (
	"fmt"
	"path/filepath"
	"time"
)

// AttemptPaths are one Attempt's isolated absolute filesystem locations
// (design §8/§10: "Absolute workspace, database, audit, artifact, binary,
// and temporary paths are Attempt facts, not Subject identity"). They are
// machine-local, never enter a Scenario/Subject/Executor digest, and match
// the isolation root shape design §8's fixture isolation constructs one per
// Attempt (workspace/, database/, audit/, process/, log/, evidence/).
type AttemptPaths struct {
	Root      string `json:"root"`
	Workspace string `json:"workspace"`
	Database  string `json:"database"`
	Audit     string `json:"audit"`
	Process   string `json:"process"`
	Log       string `json:"log"`
	Evidence  string `json:"evidence"`
}

// Attempt is the frozen `och.eval.attempt` document: one execution of one
// Cell (Scenario × Subject × Executor) and repetition index (design §4).
// Retry always creates another Attempt; this document is written once,
// atomically, before the Subject starts (design §12) and is never mutated
// afterward. It carries no field shaped like a credential value or a full
// process environment -- RuntimeID is an opaque launch identity, never a
// secret, and design §10's redaction discipline applies to any diagnostic
// text this or later documents carry, not to Attempt's own fixed field set.
type Attempt struct {
	FormatVersion int       `json:"formatVersion"`
	Schema        string    `json:"schema"`
	ID            AttemptID `json:"id"`

	EvalSetID EvalSetID `json:"evalSetId"`

	ScenarioID     ScenarioID `json:"scenarioId"`
	ScenarioDigest Digest     `json:"scenarioDigest"`
	SubjectID      SubjectID  `json:"subjectId"`
	SubjectDigest  Digest     `json:"subjectDigest"`
	ExecutorID     ExecutorID `json:"executorId"`
	ExecutorDigest Digest     `json:"executorDigest"`

	// RepetitionIndex is this Attempt's position within its Cell's
	// repeated executions (design §4/§9), zero-based.
	RepetitionIndex int `json:"repetitionIndex"`

	// Paths are this Attempt's isolated absolute filesystem locations.
	Paths AttemptPaths `json:"paths"`

	// RuntimeID is the Subject's initial writer launch identity (design
	// §16: every writer process receives a fresh runtime ID derived from
	// the Attempt; a restart or manual compaction launches under its own
	// distinct runtime ID, recorded on Outcome, not here, since Attempt is
	// written once before the Subject ever starts).
	RuntimeID string `json:"runtimeId"`

	// PublishedAt is when this Attempt document was published, immediately
	// before the Subject starts (design §12).
	PublishedAt time.Time `json:"publishedAt"`
}

// DecodeAttempt strictly decodes and validates one `och.eval.attempt`
// document (design §6).
func DecodeAttempt(data []byte) (Attempt, error) {
	var attempt Attempt
	if err := decodeStrict(data, &attempt); err != nil {
		return Attempt{}, fmt.Errorf("eval: attempt: %w", err)
	}
	if attempt.Schema != SchemaAttempt {
		return Attempt{}, fmt.Errorf("eval: attempt: %w: %q", errUnsupportedSchema, attempt.Schema)
	}
	if attempt.FormatVersion != FormatVersion {
		return Attempt{}, fmt.Errorf("eval: attempt: %w: %d", errUnsupportedFormatVersion, attempt.FormatVersion)
	}
	if err := attempt.Validate(); err != nil {
		return Attempt{}, err
	}
	return attempt, nil
}

// Validate checks every field this document requires. It does not check
// that the referenced Scenario/Subject/Executor digests actually match a
// published document — that cross-check belongs to whatever constructs an
// Attempt (matrix expansion, not yet implemented), which has those
// documents in hand.
func (attempt Attempt) Validate() error {
	if _, err := ParseAttemptID(string(attempt.ID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if _, err := ParseEvalSetID(string(attempt.EvalSetID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if _, err := ParseScenarioID(string(attempt.ScenarioID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if !digestStringPattern.MatchString(string(attempt.ScenarioDigest)) {
		return fmt.Errorf("%w: scenarioDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	if _, err := ParseSubjectID(string(attempt.SubjectID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if !digestStringPattern.MatchString(string(attempt.SubjectDigest)) {
		return fmt.Errorf("%w: subjectDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	if _, err := ParseExecutorID(string(attempt.ExecutorID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if !digestStringPattern.MatchString(string(attempt.ExecutorDigest)) {
		return fmt.Errorf("%w: executorDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	if attempt.RepetitionIndex < 0 {
		return fmt.Errorf("%w: repetitionIndex must not be negative", errInvalidDocument)
	}
	if err := attempt.Paths.validate(); err != nil {
		return err
	}
	if !hasText(attempt.RuntimeID) {
		return fmt.Errorf("%w: runtimeId is required", errInvalidDocument)
	}
	if attempt.PublishedAt.IsZero() {
		return fmt.Errorf("%w: publishedAt is required", errInvalidDocument)
	}
	return nil
}

func (paths AttemptPaths) validate() error {
	for name, path := range map[string]string{
		"paths.root":      paths.Root,
		"paths.workspace": paths.Workspace,
		"paths.database":  paths.Database,
		"paths.audit":     paths.Audit,
		"paths.process":   paths.Process,
		"paths.log":       paths.Log,
		"paths.evidence":  paths.Evidence,
	} {
		if !hasText(path) {
			return fmt.Errorf("%w: %s is required", errInvalidDocument, name)
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("%w: %s must be an absolute path", errInvalidDocument, name)
		}
	}
	return nil
}
