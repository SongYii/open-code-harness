package tools

import (
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
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

// TestDefaultWorkspaceSpecsIncludeEditFile. A literal edit is the difference
// between an agent that rewrites a file it half-remembers and one that changes
// the part it means to, so it belongs in the default set rather than behind
// configuration.
func TestDefaultWorkspaceSpecsIncludeEditFile(t *testing.T) {
	specs := DefaultWorkspaceSpecs()
	if len(specs) != 5 {
		t.Fatalf("default specs = %d, want 5", len(specs))
	}
	var edit *domain.ToolSpec
	for i := range specs {
		if specs[i].Name == NameEditFile {
			edit = &specs[i]
		}
	}
	if edit == nil {
		t.Fatal("edit_file is not in the default workspace specs")
	}
	if edit.Risk != domain.RiskWrite || !edit.Mutates {
		t.Fatalf("edit_file spec = %+v, want RiskWrite and Mutates", *edit)
	}
	if edit.Source != SourceBuiltin {
		t.Fatalf("edit_file source = %q, want builtin", edit.Source)
	}
}

// TestEditFileSchemaIsClosedAndBounded. The schema is the only thing standing
// between a model-invented field and this package's own argument struct.
func TestEditFileSchemaIsClosedAndBounded(t *testing.T) {
	var edit domain.ToolSpec
	for _, spec := range DefaultWorkspaceSpecs() {
		if spec.Name == NameEditFile {
			edit = spec
		}
	}

	tests := []struct {
		name string
		args string
		ok   bool
	}{
		{"minimal", `{"path":"a.txt","old_string":"a","new_string":"b"}`, true},
		{"replace_all omitted is legal", `{"path":"a.txt","old_string":"a","new_string":"b"}`, true},
		{"replace_all true", `{"path":"a.txt","old_string":"a","new_string":"b","replace_all":true}`, true},
		{"replace_all false", `{"path":"a.txt","old_string":"a","new_string":"b","replace_all":false}`, true},
		{"replace_all is not a string", `{"path":"a.txt","old_string":"a","new_string":"b","replace_all":"yes"}`, false},
		{"replace_all is not a number", `{"path":"a.txt","old_string":"a","new_string":"b","replace_all":1}`, false},
		{"empty new_string is legal deletion", `{"path":"a.txt","old_string":"a","new_string":""}`, true},
		{"empty old_string matches everywhere", `{"path":"a.txt","old_string":"","new_string":"b"}`, false},
		{"missing old_string", `{"path":"a.txt","new_string":"b"}`, false},
		{"missing new_string", `{"path":"a.txt","old_string":"a"}`, false},
		{"missing path", `{"old_string":"a","new_string":"b"}`, false},
		{"unknown field", `{"path":"a.txt","old_string":"a","new_string":"b","regex":true}`, false},
	}
	for _, test := range tests {
		err := ValidateArgs(edit, test.args)
		if ok := err == nil; ok != test.ok {
			t.Fatalf("%s: ValidateArgs = %v, want ok=%t", test.name, err, test.ok)
		}
	}
}

// TestABooleanLeafIsAllowedButABooleanToolSchemaIsNot.
//
// replace_all needs a boolean property, which the compiler did not previously
// accept anywhere. Allowing it at a leaf is not the same as allowing a tool
// whose whole argument object is a boolean: the root has to stay an object, or
// the closed-field guarantee everything else depends on has nothing to close.
func TestABooleanLeafIsAllowedButABooleanToolSchemaIsNot(t *testing.T) {
	leaf := domain.ToolSpec{
		Name: "leaf", Source: SourceBuiltin, Risk: domain.RiskRead,
		InputSchema: []byte(`{"type":"object","additionalProperties":false,"required":["flag"],"properties":{"flag":{"type":"boolean"}}}`),
	}
	if err := ValidateArgs(leaf, `{"flag":true}`); err != nil {
		t.Fatalf("a boolean leaf was rejected: %v", err)
	}
	if err := ValidateArgs(leaf, `{"flag":"true"}`); err == nil {
		t.Fatal("a string was accepted for a boolean leaf")
	}

	root := domain.ToolSpec{
		Name: "root", Source: SourceBuiltin, Risk: domain.RiskRead,
		InputSchema: []byte(`{"type":"boolean"}`),
	}
	if _, err := NewCatalog([]domain.ToolSpec{root}); err == nil {
		t.Fatal("a tool whose whole argument schema is a boolean was accepted")
	}
}

// TestABooleanLeafRejectsBorrowedKeywords keeps the new leaf from becoming a
// place where string or integer constraints are silently ignored.
func TestABooleanLeafRejectsBorrowedKeywords(t *testing.T) {
	for _, schema := range []string{
		`{"type":"object","additionalProperties":false,"properties":{"flag":{"type":"boolean","minLength":1}}}`,
		`{"type":"object","additionalProperties":false,"properties":{"flag":{"type":"boolean","maximum":2}}}`,
		`{"type":"object","additionalProperties":false,"properties":{"flag":{"type":"boolean","items":{"type":"string"}}}}`,
		`{"type":"object","additionalProperties":false,"properties":{"flag":{"type":"boolean","properties":{}}}}`,
	} {
		spec := domain.ToolSpec{
			Name: "leaf", Source: SourceBuiltin, Risk: domain.RiskRead,
			InputSchema: []byte(schema),
		}
		if _, err := NewCatalog([]domain.ToolSpec{spec}); err == nil {
			t.Fatalf("a boolean leaf borrowed a keyword it cannot honour: %s", schema)
		}
	}
}
