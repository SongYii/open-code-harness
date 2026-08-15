package application

import (
	"errors"
	"fmt"
	"testing"
)

func TestApplicationErrorMethodsAreNilSafe(t *testing.T) {
	var typedNil *Error
	if got := typedNil.Error(); got != "<nil>" {
		t.Fatalf("typed nil Error() = %q, want <nil>", got)
	}
	if cause := typedNil.Unwrap(); cause != nil {
		t.Fatalf("typed nil Unwrap() = %v, want nil", cause)
	}
}

func TestIsCategoryTraversesNestedJoinsAfterTypedNilBranches(t *testing.T) {
	var typedNil *Error
	want := &Error{Category: CategoryPersistence, Code: "append_failed"}
	err := errors.Join(
		fmt.Errorf("typed nil: %w", typedNil),
		errors.Join(errors.New("ordinary"), fmt.Errorf("later: %w", want)),
	)
	if !IsCategory(err, CategoryPersistence) {
		t.Fatal("IsCategory() did not find a matching later sibling")
	}
	if IsCategory(err, CategoryModel) {
		t.Fatal("IsCategory() found an absent category")
	}
}

func TestVersionConflictMatcherIsNilSafeAcrossCompleteErrorTree(t *testing.T) {
	var typedNil *VersionConflictError
	conflict := &VersionConflictError{SessionID: "session-1", ExpectedVersion: 1, ActualVersion: 2}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil},
		{name: "direct typed nil", err: typedNil},
		{name: "typed nil only join", err: errors.Join(typedNil, errors.New("ordinary"))},
		{name: "later sibling", err: errors.Join(typedNil, fmt.Errorf("wrapped: %w", conflict)), want: true},
		{name: "nested join", err: fmt.Errorf("outer: %w", errors.Join(errors.New("ordinary"), conflict)), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsVersionConflict(test.err); got != test.want {
				t.Fatalf("IsVersionConflict() = %t, want %t", got, test.want)
			}
		})
	}
	if got := typedNil.Error(); got != "<nil>" {
		t.Fatalf("typed nil VersionConflictError.Error() = %q, want <nil>", got)
	}
}
