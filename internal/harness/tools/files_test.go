package tools

import (
	"strings"
	"testing"
)

// TestMutationGuardValidate pins the four legal combinations.
//
// A guard is the whole safety mechanism: "create this only if nothing is
// there" and "replace this only if it still looks like what I read" are
// different promises, and each needs exactly the fields that make it
// checkable. A create guard carrying a version would be asserting something
// about a file it claims does not exist, and a replace guard without one
// would be an unconditional overwrite wearing a guard's name.
func TestMutationGuardValidate(t *testing.T) {
	tests := []struct {
		guard MutationGuard
		ok    bool
	}{
		{MutationGuard{Kind: GuardCreateIfAbsent}, true},
		{MutationGuard{Kind: GuardCreateIfAbsent, Version: "v1"}, false},
		{MutationGuard{Kind: GuardReplaceIfVersion, Version: "v1"}, true},
		{MutationGuard{Kind: GuardReplaceIfVersion}, false},
		{MutationGuard{Kind: "invented"}, false},
	}
	for _, test := range tests {
		if got := test.guard.Validate() == nil; got != test.ok {
			t.Fatalf("Validate(%#v) ok = %t, want %t", test.guard, got, test.ok)
		}
	}
}

// TestFilesystemErrorCodes proves each new code is a real member of the
// vocabulary and that the message stays secret-free.
//
// These errors travel to a model in a Tool Result. A path or a version in the
// text would leak workspace layout and file content digests through a channel
// whose whole contract is that it carries a stable code and nothing else.
func TestFilesystemErrorCodes(t *testing.T) {
	codes := []ErrorCode{
		CodeFSNotObserved,
		CodeFSStaleVersion,
		CodeFSEditNotFound,
		CodeFSAmbiguousEdit,
		CodeFSNotRegularFile,
		CodeFSNotText,
		CodeFSTooLarge,
	}
	for _, code := range codes {
		err := &Error{Code: code}
		if !IsCode(err, code) {
			t.Fatalf("IsCode(%q) = false; the code is not part of the vocabulary", code)
		}
		message := err.Error()
		if !strings.Contains(message, string(code)) {
			t.Fatalf("Error() = %q, want it to name the code %q", message, code)
		}
		for _, leaked := range []string{"/", `\`, "sha256", "v1"} {
			if strings.Contains(strings.TrimPrefix(message, "tools/"), leaked) {
				t.Fatalf("Error() = %q leaks %q; a code is all this channel may carry", message, leaked)
			}
		}
	}
}

// TestFilesystemErrorCodesAreDistinct. Two codes collapsing into one would
// make a caller unable to tell "you never read this file" from "you read it
// and it changed underneath you" — a distinction the whole mechanism exists
// to preserve.
func TestFilesystemErrorCodesAreDistinct(t *testing.T) {
	seen := map[ErrorCode]bool{}
	for _, code := range []ErrorCode{
		CodeInvalidSpec, CodeInvalidArgs, CodeScopeDenied, CodeDuplicateName,
		CodeFSNotObserved, CodeFSStaleVersion, CodeFSEditNotFound, CodeFSAmbiguousEdit,
		CodeFSNotRegularFile, CodeFSNotText, CodeFSTooLarge,
	} {
		if seen[code] {
			t.Fatalf("code %q is declared twice", code)
		}
		seen[code] = true
		if !IsCode(&Error{Code: code}, code) {
			t.Fatalf("IsCode(%q) = false", code)
		}
	}
}

// TestAnUnknownCodeIsNeverAccepted keeps validErrorCode fail-closed: an
// invented code must not become usable simply by being spelled.
func TestAnUnknownCodeIsNeverAccepted(t *testing.T) {
	if IsCode(&Error{Code: "fs_invented"}, "fs_invented") {
		t.Fatal("an undeclared code was accepted")
	}
}

// TestMaxEditFileBytesIsOneMebibyte pins the bound the design declares, so a
// later change to it is a deliberate edit rather than a drift.
func TestMaxEditFileBytesIsOneMebibyte(t *testing.T) {
	if MaxEditFileBytes != 1<<20 {
		t.Fatalf("MaxEditFileBytes = %d, want %d", MaxEditFileBytes, 1<<20)
	}
}
