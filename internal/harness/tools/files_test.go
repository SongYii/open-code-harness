package tools

import (
	"strings"
	"testing"
)

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

func TestFilesystemErrorCodes(t *testing.T) {
	codes := []ErrorCode{
		CodeFilesystemNotObserved,
		CodeFilesystemStaleVersion,
		CodeFilesystemEditNotFound,
		CodeFilesystemAmbiguousEdit,
		CodeFilesystemNotRegularFile,
		CodeFilesystemNotText,
		CodeFilesystemTooLarge,
	}
	for _, code := range codes {
		err := &Error{Code: code}
		if !IsCode(err, code) {
			t.Fatalf("IsCode(%q) = false", code)
		}
		message := err.Error()
		if strings.Contains(message, "/tmp/") || strings.Contains(message, "v1") {
			t.Fatalf("Error() = %q, contains path or version", message)
		}
	}
}
