package engine

import (
	"errors"
	"testing"
)

func TestIsCodeTraversesJoinedTreesAndTypedNilBranches(t *testing.T) {
	var typedNil *Error
	err := errors.Join(
		&Error{Code: CodeDelivery},
		errors.Join(typedNil, &Error{Code: CodeModelStream, Cause: errors.New("raw provider text")}),
	)
	if !IsCode(err, CodeModelStream) {
		t.Fatal("IsCode() did not find a matching nested sibling after a mismatching branch")
	}
	if IsCode(err, CodeCanceled) {
		t.Fatal("IsCode() reported an absent code")
	}
	if IsCode(typedNil, CodeModelStream) {
		t.Fatal("IsCode() matched a direct typed-nil error")
	}
	if got := (&Error{Code: CodeModelStream, Cause: errors.New("secret")}).Error(); got != "engine/model_stream" {
		t.Fatalf("Error() = %q, want stable code without cause text", got)
	}
}
