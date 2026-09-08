package domain

import (
	"encoding/hex"
	pathpkg "path"
	"strings"
	"unicode/utf8"
)

func validateWorkspaceInstructionsPayload(event WorkspaceInstructionsRecorded, code ErrorCode) error {
	fail := func(message string) error { return domainError(code, message) }
	if event.FormatVersion != WorkspaceInstructionsFormatV1 {
		return fail("workspace instruction format version is invalid")
	}
	if !hasRequiredText(event.PromptID) || !utf8.ValidString(event.PromptID) || !validSHA256Digest(event.PromptDigest) {
		return fail("workspace instruction prompt identity is invalid")
	}
	if event.Epoch == 0 || !validSHA256Digest(event.EffectiveSetDigest) {
		return fail("workspace instruction epoch or effective digest is invalid")
	}
	if len(event.Discovered) == 0 && len(event.Changes) == 0 && len(event.Diagnostics) == 0 {
		return fail("workspace instruction batch is empty")
	}

	previous := ""
	for _, discovered := range event.Discovered {
		if err := validateInstructionPathScope(discovered.Path, discovered.Scope, code); err != nil {
			return err
		}
		if previous != "" && discovered.Path <= previous {
			return fail("workspace instruction discoveries are not strictly sorted")
		}
		previous = discovered.Path
	}

	previous = ""
	for _, change := range event.Changes {
		if err := validateInstructionPathScope(change.Path, change.Scope, code); err != nil {
			return err
		}
		if previous != "" && change.Path <= previous {
			return fail("workspace instruction changes are not strictly sorted")
		}
		previous = change.Path
		if !utf8.ValidString(change.Content) {
			return fail("workspace instruction content must be valid UTF-8")
		}
		switch change.Action {
		case InstructionActionSet:
			if change.PriorDigest != "" || !validSHA256Digest(change.Digest) {
				return fail("workspace instruction set transition is invalid")
			}
		case InstructionActionReplace:
			if !validSHA256Digest(change.PriorDigest) || !validSHA256Digest(change.Digest) || change.PriorDigest == change.Digest {
				return fail("workspace instruction replace transition is invalid")
			}
		case InstructionActionRemove:
			if !validSHA256Digest(change.PriorDigest) || change.Digest != "" || change.Content != "" {
				return fail("workspace instruction remove transition is invalid")
			}
		default:
			return fail("workspace instruction action is invalid")
		}
	}

	previous = ""
	for _, diagnostic := range event.Diagnostics {
		if err := validateInstructionPath(diagnostic.Path, code); err != nil {
			return err
		}
		if !hasRequiredText(diagnostic.Class) || !utf8.ValidString(diagnostic.Class) {
			return fail("workspace instruction diagnostic class is invalid")
		}
		key := diagnostic.Path + "\x00" + diagnostic.Class
		if previous != "" && key <= previous {
			return fail("workspace instruction diagnostics are not strictly sorted")
		}
		previous = key
	}
	if len(event.Changes) > 0 && event.RenderedMessage == "" {
		return fail("workspace instruction changes require a rendered message")
	}
	if !utf8.ValidString(event.RenderedMessage) {
		return fail("workspace instruction rendered message must be valid UTF-8")
	}
	return nil
}

func validateInstructionPathScope(instructionPath, scope string, code ErrorCode) error {
	if err := validateInstructionPath(instructionPath, code); err != nil {
		return err
	}
	if scope != pathpkg.Dir(instructionPath) {
		return domainError(code, "workspace instruction scope does not match path")
	}
	return nil
}

func validateInstructionPath(instructionPath string, code ErrorCode) error {
	if !utf8.ValidString(instructionPath) ||
		instructionPath == "" ||
		strings.Contains(instructionPath, "\\") ||
		pathpkg.IsAbs(instructionPath) ||
		pathpkg.Clean(instructionPath) != instructionPath ||
		pathpkg.Base(instructionPath) != "AGENTS.md" {
		return domainError(code, "workspace instruction path is invalid")
	}
	return nil
}

func validSHA256Digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
