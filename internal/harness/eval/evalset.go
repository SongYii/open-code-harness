package eval

import (
	"fmt"
	"time"
)

// SchemaEvalSet is the `och.eval.set` document schema (design §6).
const SchemaEvalSet = "och.eval.set"

// EvalSet limit defaults and hard maximums, design §19's own table.
const (
	DefaultConcurrentAttempts = 1
	MaxConcurrentAttempts     = 8

	DefaultMaxExpandedAttempts = 256
	MaxMaxExpandedAttempts     = 4096

	DefaultAttemptWallTime = 15 * time.Minute
	MaxAttemptWallTime     = 2 * time.Hour

	DefaultTurnActionTime = 5 * time.Minute
	MaxTurnActionTime     = 30 * time.Minute

	DefaultProcessStartup = 30 * time.Second
	MaxProcessStartup     = 2 * time.Minute

	DefaultCancellationGrace = 10 * time.Second
	MaxCancellationGrace     = time.Minute

	DefaultShutdownGrace = 10 * time.Second
	MaxShutdownGrace     = time.Minute

	DefaultEvidenceCollectionTime = 2 * time.Minute
	MaxEvidenceCollectionTime     = 10 * time.Minute

	DefaultFixtureFiles = 10_000
	MaxFixtureFiles     = 100_000

	DefaultArtifactFiles = 10_000
	MaxArtifactFiles     = 100_000

	DefaultOneArtifactBytes = 16 * 1024 * 1024
	MaxOneArtifactBytes     = 64 * 1024 * 1024

	DefaultTotalAttemptArtifactBytes = 256 * 1024 * 1024
	MaxTotalAttemptArtifactBytes     = 1024 * 1024 * 1024

	DefaultStdoutBytes = 8 * 1024 * 1024
	MaxStdoutBytes     = 64 * 1024 * 1024

	DefaultStderrBytes = 8 * 1024 * 1024
	MaxStderrBytes     = 64 * 1024 * 1024
)

// EvalSetLimits bounds an EvalSet (design §19). A zero field takes its own
// default; every field is capped at its own hard maximum. TokenCap has no
// default — design §19 requires an EvalSet to provide a positive per-Attempt
// token cap explicitly.
type EvalSetLimits struct {
	ConcurrentAttempts     int           `json:"concurrentAttempts,omitempty"`
	MaxExpandedAttempts    int           `json:"maxExpandedAttempts,omitempty"`
	AttemptWallTime        time.Duration `json:"attemptWallTime,omitempty"`
	TurnActionTime         time.Duration `json:"turnActionTime,omitempty"`
	ProcessStartup         time.Duration `json:"processStartup,omitempty"`
	CancellationGrace      time.Duration `json:"cancellationGrace,omitempty"`
	ShutdownGrace          time.Duration `json:"shutdownGrace,omitempty"`
	EvidenceCollectionTime time.Duration `json:"evidenceCollectionTime,omitempty"`
	FixtureFiles           int           `json:"fixtureFiles,omitempty"`
	ArtifactFiles          int           `json:"artifactFiles,omitempty"`
	OneArtifactBytes       int64         `json:"oneArtifactBytes,omitempty"`
	TotalArtifactBytes     int64         `json:"totalArtifactBytes,omitempty"`
	StdoutBytes            int64         `json:"stdoutBytes,omitempty"`
	StderrBytes            int64         `json:"stderrBytes,omitempty"`

	// TokenCap is required and positive (design §19): "An EvalSet must
	// provide a positive per-Attempt token cap."
	TokenCap int64 `json:"tokenCap"`

	// CostCapMicrounits is optional. When set, every Subject this EvalSet
	// references must carry a frozen price-table digest (design §19);
	// EvalSet.Validate cannot check that on its own because Subject bodies
	// are not embedded here, only referenced — the expansion step that
	// resolves full Subject documents enforces it (see ExpandAttempts).
	CostCapMicrounits int64 `json:"costCapMicrounits,omitempty"`
}

func (limits EvalSetLimits) withDefaults() EvalSetLimits {
	if limits.ConcurrentAttempts == 0 {
		limits.ConcurrentAttempts = DefaultConcurrentAttempts
	}
	if limits.MaxExpandedAttempts == 0 {
		limits.MaxExpandedAttempts = DefaultMaxExpandedAttempts
	}
	if limits.AttemptWallTime == 0 {
		limits.AttemptWallTime = DefaultAttemptWallTime
	}
	if limits.TurnActionTime == 0 {
		limits.TurnActionTime = DefaultTurnActionTime
	}
	if limits.ProcessStartup == 0 {
		limits.ProcessStartup = DefaultProcessStartup
	}
	if limits.CancellationGrace == 0 {
		limits.CancellationGrace = DefaultCancellationGrace
	}
	if limits.ShutdownGrace == 0 {
		limits.ShutdownGrace = DefaultShutdownGrace
	}
	if limits.EvidenceCollectionTime == 0 {
		limits.EvidenceCollectionTime = DefaultEvidenceCollectionTime
	}
	if limits.FixtureFiles == 0 {
		limits.FixtureFiles = DefaultFixtureFiles
	}
	if limits.ArtifactFiles == 0 {
		limits.ArtifactFiles = DefaultArtifactFiles
	}
	if limits.OneArtifactBytes == 0 {
		limits.OneArtifactBytes = DefaultOneArtifactBytes
	}
	if limits.TotalArtifactBytes == 0 {
		limits.TotalArtifactBytes = DefaultTotalAttemptArtifactBytes
	}
	if limits.StdoutBytes == 0 {
		limits.StdoutBytes = DefaultStdoutBytes
	}
	if limits.StderrBytes == 0 {
		limits.StderrBytes = DefaultStderrBytes
	}
	return limits
}

func (limits EvalSetLimits) validate() error {
	limits = limits.withDefaults()
	checks := []struct {
		name  string
		value int64
		max   int64
	}{
		{"concurrentAttempts", int64(limits.ConcurrentAttempts), MaxConcurrentAttempts},
		{"maxExpandedAttempts", int64(limits.MaxExpandedAttempts), MaxMaxExpandedAttempts},
		{"attemptWallTime", int64(limits.AttemptWallTime), int64(MaxAttemptWallTime)},
		{"turnActionTime", int64(limits.TurnActionTime), int64(MaxTurnActionTime)},
		{"processStartup", int64(limits.ProcessStartup), int64(MaxProcessStartup)},
		{"cancellationGrace", int64(limits.CancellationGrace), int64(MaxCancellationGrace)},
		{"shutdownGrace", int64(limits.ShutdownGrace), int64(MaxShutdownGrace)},
		{"evidenceCollectionTime", int64(limits.EvidenceCollectionTime), int64(MaxEvidenceCollectionTime)},
		{"fixtureFiles", int64(limits.FixtureFiles), MaxFixtureFiles},
		{"artifactFiles", int64(limits.ArtifactFiles), MaxArtifactFiles},
		{"oneArtifactBytes", limits.OneArtifactBytes, MaxOneArtifactBytes},
		{"totalArtifactBytes", limits.TotalArtifactBytes, MaxTotalAttemptArtifactBytes},
		{"stdoutBytes", limits.StdoutBytes, MaxStdoutBytes},
		{"stderrBytes", limits.StderrBytes, MaxStderrBytes},
	}
	for _, check := range checks {
		if check.value <= 0 || check.value > check.max {
			return fmt.Errorf("%w: limits.%s must be 1-%d, got %d", errInvalidDocument, check.name, check.max, check.value)
		}
	}
	if limits.TokenCap <= 0 {
		return fmt.Errorf("%w: limits.tokenCap must be positive", errInvalidDocument)
	}
	if limits.CostCapMicrounits < 0 {
		return fmt.Errorf("%w: limits.costCapMicrounits must not be negative", errInvalidDocument)
	}
	return nil
}

// ScenarioRef, SubjectRef, and ExecutorRef freeze one referenced document's
// identity and digest inside an EvalSet (design §9).
type ScenarioRef struct {
	ID     ScenarioID `json:"id"`
	Digest Digest     `json:"digest"`
}

type SubjectRef struct {
	ID     SubjectID `json:"id"`
	Digest Digest    `json:"digest"`
}

type ExecutorRef struct {
	ID     ExecutorID `json:"id"`
	Digest Digest     `json:"digest"`
}

// EvalSet is the frozen `och.eval.set` document: one ordered experiment
// matrix, repetitions, limits, scorer selections, and pairing rules (design
// §4/§9), frozen once before expansion.
type EvalSet struct {
	FormatVersion int       `json:"formatVersion"`
	Schema        string    `json:"schema"`
	ID            EvalSetID `json:"id"`

	Scenarios []ScenarioRef `json:"scenarios"`
	Subjects  []SubjectRef  `json:"subjects"`
	Executors []ExecutorRef `json:"executors"`

	RepetitionCount int    `json:"repetitionCount"`
	PairingSeed     string `json:"pairingSeed"`

	VerifierConfigDigest Digest `json:"verifierConfigDigest,omitempty"`
	JudgeConfigDigest    Digest `json:"judgeConfigDigest,omitempty"`

	Limits       EvalSetLimits `json:"limits"`
	ArtifactRoot string        `json:"artifactRoot"`

	Lane EvalLane `json:"lane"`
}

// DecodeEvalSet strictly decodes and validates one `och.eval.set` document
// (design §6).
func DecodeEvalSet(data []byte) (EvalSet, error) {
	var set EvalSet
	if err := decodeStrict(data, &set); err != nil {
		return EvalSet{}, fmt.Errorf("eval: eval set: %w", err)
	}
	if set.Schema != SchemaEvalSet {
		return EvalSet{}, fmt.Errorf("eval: eval set: %w: %q", errUnsupportedSchema, set.Schema)
	}
	if set.FormatVersion != FormatVersion {
		return EvalSet{}, fmt.Errorf("eval: eval set: %w: %d", errUnsupportedFormatVersion, set.FormatVersion)
	}
	if err := set.Validate(); err != nil {
		return EvalSet{}, err
	}
	return set, nil
}

// Validate checks every field this document requires: non-empty, duplicate-
// free reference lists, a positive repetition count, valid limits, a lane,
// and (design §23) that a fixture-lane set carries no live-only
// configuration this package can see. It does not resolve referenced
// Scenario/Subject/Executor documents — ExpandAttempts does, and enforces
// the digest-match and capability rules that require them.
func (set EvalSet) Validate() error {
	if _, err := ParseEvalSetID(string(set.ID)); err != nil {
		return fmt.Errorf("%w: %w", errInvalidDocument, err)
	}
	if len(set.Scenarios) == 0 {
		return fmt.Errorf("%w: at least one scenario is required", errInvalidDocument)
	}
	if len(set.Subjects) == 0 {
		return fmt.Errorf("%w: at least one subject is required", errInvalidDocument)
	}
	if len(set.Executors) == 0 {
		return fmt.Errorf("%w: at least one executor is required", errInvalidDocument)
	}
	seenScenarios := make(map[ScenarioID]struct{}, len(set.Scenarios))
	for _, ref := range set.Scenarios {
		if _, err := ParseScenarioID(string(ref.ID)); err != nil {
			return fmt.Errorf("%w: %w", errInvalidDocument, err)
		}
		if !digestStringPattern.MatchString(string(ref.Digest)) {
			return fmt.Errorf("%w: scenario %q: digest must be sha256:<64 lowercase hex>", errInvalidDocument, ref.ID)
		}
		if _, exists := seenScenarios[ref.ID]; exists {
			return fmt.Errorf("%w: duplicate scenario %q", errInvalidDocument, ref.ID)
		}
		seenScenarios[ref.ID] = struct{}{}
	}
	seenSubjects := make(map[SubjectID]struct{}, len(set.Subjects))
	for _, ref := range set.Subjects {
		if _, err := ParseSubjectID(string(ref.ID)); err != nil {
			return fmt.Errorf("%w: %w", errInvalidDocument, err)
		}
		if !digestStringPattern.MatchString(string(ref.Digest)) {
			return fmt.Errorf("%w: subject %q: digest must be sha256:<64 lowercase hex>", errInvalidDocument, ref.ID)
		}
		if _, exists := seenSubjects[ref.ID]; exists {
			return fmt.Errorf("%w: duplicate subject %q", errInvalidDocument, ref.ID)
		}
		seenSubjects[ref.ID] = struct{}{}
	}
	seenExecutors := make(map[ExecutorID]struct{}, len(set.Executors))
	for _, ref := range set.Executors {
		if _, err := ParseExecutorID(string(ref.ID)); err != nil {
			return fmt.Errorf("%w: %w", errInvalidDocument, err)
		}
		if !digestStringPattern.MatchString(string(ref.Digest)) {
			return fmt.Errorf("%w: executor %q: digest must be sha256:<64 lowercase hex>", errInvalidDocument, ref.ID)
		}
		if _, exists := seenExecutors[ref.ID]; exists {
			return fmt.Errorf("%w: duplicate executor %q", errInvalidDocument, ref.ID)
		}
		seenExecutors[ref.ID] = struct{}{}
	}
	if set.RepetitionCount <= 0 {
		return fmt.Errorf("%w: repetitionCount must be positive", errInvalidDocument)
	}
	if !hasText(set.PairingSeed) {
		return fmt.Errorf("%w: pairingSeed is required", errInvalidDocument)
	}
	if set.VerifierConfigDigest != "" && !digestStringPattern.MatchString(string(set.VerifierConfigDigest)) {
		return fmt.Errorf("%w: verifierConfigDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	if set.JudgeConfigDigest != "" && !digestStringPattern.MatchString(string(set.JudgeConfigDigest)) {
		return fmt.Errorf("%w: judgeConfigDigest must be sha256:<64 lowercase hex>", errInvalidDocument)
	}
	// Lane decides whether a judge configuration is required or forbidden.
	// A fixture set naming one would be claiming a judge identity nothing
	// in the deterministic lane can ever exercise; a live set without one
	// could publish a quality Score whose configuration no reader could
	// reconstruct from the Attempt's own evidence.
	switch set.Lane {
	case LaneFixture:
		if set.JudgeConfigDigest != "" {
			return fmt.Errorf("%w: a %q lane set must not declare judgeConfigDigest", errInvalidDocument, LaneFixture)
		}
	case LaneLive:
		if set.JudgeConfigDigest == "" {
			return fmt.Errorf("%w: a %q lane set must declare judgeConfigDigest", errInvalidDocument, LaneLive)
		}
	}
	if err := set.Limits.validate(); err != nil {
		return err
	}
	if !hasText(set.ArtifactRoot) {
		return fmt.Errorf("%w: artifactRoot is required", errInvalidDocument)
	}
	switch set.Lane {
	case LaneFixture, LaneLive:
	default:
		return fmt.Errorf("%w: lane must be %q or %q", errInvalidDocument, LaneFixture, LaneLive)
	}
	return nil
}
