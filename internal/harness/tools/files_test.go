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
	codes := []struct {
		code ErrorCode
		want string
	}{
		{CodeFilesystemNotObserved, "fs_not_observed"},
		{CodeFilesystemNotFound, "fs_not_found"},
		{CodeFilesystemStaleVersion, "fs_stale_version"},
		{CodeFilesystemEditNotFound, "fs_edit_not_found"},
		{CodeFilesystemAmbiguousEdit, "fs_ambiguous_edit"},
		{CodeFilesystemNotRegularFile, "fs_not_regular_file"},
		{CodeFilesystemNotText, "fs_not_text"},
		{CodeFilesystemTooLarge, "fs_too_large"},
	}
	for _, test := range codes {
		err := &Error{Code: test.code}
		if !IsCode(err, test.code) {
			t.Fatalf("IsCode(%q) = false", test.code)
		}
		if got := string(test.code); got != test.want {
			t.Fatalf("wire code = %q, want %q", got, test.want)
		}
		message := err.Error()
		if strings.Contains(message, "/tmp/") || strings.Contains(message, "v1") {
			t.Fatalf("Error() = %q, contains path or version", message)
		}
	}
}
