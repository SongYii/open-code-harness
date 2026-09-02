package eval

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
)

// maxIDBytes bounds every eval identifier (design §4).
const maxIDBytes = 128

// userProvidedIDPattern is design §4's user-provided ID shape: an opaque
// lowercase ASCII identifier starting with a letter or digit.
var userProvidedIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// generatedIDPattern is design §4's generated ID shape: cryptographically
// random 128-bit lowercase hex, which is always exactly 32 hex characters.
var generatedIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

var errInvalidID = errors.New("eval: invalid identifier")

// ScenarioID, SubjectID, ExecutorID, and EvalSetID are distinct named types
// over the same validated user-provided string shape so a Scenario ID
// cannot be silently accepted where a Subject or Executor ID was expected,
// matching the domain package's SessionID/EventID/CommandID convention.
type ScenarioID string
type SubjectID string
type ExecutorID string
type EvalSetID string

// AttemptID and ScoreID are distinct named types over the generated-ID
// shape (design §4: "generated Attempt and Score IDs use cryptographically
// random 128-bit lowercase hex").
type AttemptID string
type ScoreID string

// GeneratedID is design §4's generated-identifier shape: cryptographically
// random 128-bit lowercase hex. Attempt and Score IDs (not yet defined in
// this package) use this shape once those documents exist; the primitive
// lives here now so it is not reimplemented when they land.
type GeneratedID string

// ParseScenarioID validates a user-provided Scenario ID (design §4/§7).
func ParseScenarioID(raw string) (ScenarioID, error) {
	if err := validateUserProvidedID(raw); err != nil {
		return "", fmt.Errorf("eval: scenario ID: %w", err)
	}
	return ScenarioID(raw), nil
}

// ParseSubjectID validates a user-provided Subject ID (design §4/§10).
func ParseSubjectID(raw string) (SubjectID, error) {
	if err := validateUserProvidedID(raw); err != nil {
		return "", fmt.Errorf("eval: subject ID: %w", err)
	}
	return SubjectID(raw), nil
}

// ParseExecutorID validates a user-provided Executor ID (design §4/§11).
func ParseExecutorID(raw string) (ExecutorID, error) {
	if err := validateUserProvidedID(raw); err != nil {
		return "", fmt.Errorf("eval: executor ID: %w", err)
	}
	return ExecutorID(raw), nil
}

// ParseEvalSetID validates a user-provided EvalSet ID (design §4/§9).
func ParseEvalSetID(raw string) (EvalSetID, error) {
	if err := validateUserProvidedID(raw); err != nil {
		return "", fmt.Errorf("eval: eval set ID: %w", err)
	}
	return EvalSetID(raw), nil
}

// NewAttemptID mints a fresh generated Attempt ID (design §4).
func NewAttemptID() (AttemptID, error) {
	generated, err := NewGeneratedID()
	if err != nil {
		return "", fmt.Errorf("eval: attempt ID: %w", err)
	}
	return AttemptID(generated), nil
}

// ParseAttemptID validates an already-generated Attempt ID read back from a
// published document.
func ParseAttemptID(raw string) (AttemptID, error) {
	if _, err := ParseGeneratedID(raw); err != nil {
		return "", fmt.Errorf("eval: attempt ID: %w", err)
	}
	return AttemptID(raw), nil
}

// NewScoreID mints a fresh generated Score ID (design §4).
func NewScoreID() (ScoreID, error) {
	generated, err := NewGeneratedID()
	if err != nil {
		return "", fmt.Errorf("eval: score ID: %w", err)
	}
	return ScoreID(generated), nil
}

// ParseScoreID validates an already-generated Score ID read back from a
// published document.
func ParseScoreID(raw string) (ScoreID, error) {
	if _, err := ParseGeneratedID(raw); err != nil {
		return "", fmt.Errorf("eval: score ID: %w", err)
	}
	return ScoreID(raw), nil
}

// NewGeneratedID produces a fresh design §4 generated identifier: 128 bits
// read from a cryptographically secure source, lowercase-hex encoded.
func NewGeneratedID() (GeneratedID, error) {
	raw := make([]byte, 16) // 128 bits
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("eval: generate identifier: %w", err)
	}
	return GeneratedID(hex.EncodeToString(raw)), nil
}

// ParseGeneratedID validates an already-generated identifier read back from
// a published document, rather than minting a new one.
func ParseGeneratedID(raw string) (GeneratedID, error) {
	if !generatedIDPattern.MatchString(raw) {
		return "", fmt.Errorf("eval: generated ID: %w: must be 32 lowercase hex characters", errInvalidID)
	}
	return GeneratedID(raw), nil
}

func validateUserProvidedID(raw string) error {
	if raw == "" {
		return fmt.Errorf("%w: must not be empty", errInvalidID)
	}
	if len(raw) > maxIDBytes {
		return fmt.Errorf("%w: must not exceed %d bytes", errInvalidID, maxIDBytes)
	}
	if !userProvidedIDPattern.MatchString(raw) {
		return fmt.Errorf("%w: must match %s", errInvalidID, userProvidedIDPattern.String())
	}
	return nil
}
