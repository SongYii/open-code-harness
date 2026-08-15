package tools

import (
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	ReasonInWorkspace       = "in_workspace"
	ReasonEmpty             = "empty"
	ReasonNUL               = "nul"
	ReasonInvalidUTF8       = "invalid_utf8"
	ReasonLeftoverDotDot    = "leftover_dotdot"
	ReasonAbsPrefixMismatch = "abs_prefix_mismatch"
	ReasonWindowsVolume     = "windows_volume"
	ReasonEmptyWorkspace    = "empty_workspace"
)

type ScopeRequest struct {
	WorkspaceRoot string
	Requested     string
}

type ScopeResult struct {
	Clean       string
	InWorkspace bool
	Reason      string
}

// CheckScopeLexical is a no-I/O jail. Leftover "..", NUL, Windows volumes, and
// absolute prefix mismatch deny. Symlinks are not followed.
func CheckScopeLexical(req ScopeRequest) (ScopeResult, error) {
	if !utf8.ValidString(req.WorkspaceRoot) || !utf8.ValidString(req.Requested) {
		return ScopeResult{Reason: ReasonInvalidUTF8}, nil
	}
	if strings.IndexByte(req.WorkspaceRoot, 0) >= 0 || strings.IndexByte(req.Requested, 0) >= 0 {
		return ScopeResult{Reason: ReasonNUL}, nil
	}
	if strings.TrimSpace(req.WorkspaceRoot) == "" {
		return ScopeResult{Reason: ReasonEmptyWorkspace}, nil
	}
	if req.Requested == "" || strings.TrimSpace(req.Requested) == "" {
		return ScopeResult{Reason: ReasonEmpty}, nil
	}
	if hasWindowsVolume(req.WorkspaceRoot) || hasWindowsVolume(req.Requested) {
		return ScopeResult{Clean: slashClean(req.Requested), Reason: ReasonWindowsVolume}, nil
	}

	workspace := slashClean(req.WorkspaceRoot)
	cleaned := slashClean(req.Requested)
	if hasLeftoverDotDot(workspace) || hasLeftoverDotDot(cleaned) {
		return ScopeResult{Clean: cleaned, Reason: ReasonLeftoverDotDot}, nil
	}

	if isLexicalAbs(req.Requested) || isLexicalAbs(cleaned) {
		if !absInside(cleaned, workspace) {
			return ScopeResult{Clean: cleaned, Reason: ReasonAbsPrefixMismatch}, nil
		}
		return ScopeResult{Clean: cleaned, InWorkspace: true, Reason: ReasonInWorkspace}, nil
	}

	return ScopeResult{Clean: cleaned, InWorkspace: true, Reason: ReasonInWorkspace}, nil
}

func slashClean(value string) string {
	normalized := filepath.ToSlash(value)
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	return path.Clean(normalized)
}

func isLexicalAbs(value string) bool {
	normalized := filepath.ToSlash(value)
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	return path.IsAbs(normalized) || filepath.IsAbs(value)
}

func hasLeftoverDotDot(cleaned string) bool {
	return cleaned == ".." || strings.HasPrefix(cleaned, "../")
}

func hasWindowsVolume(value string) bool {
	if name := filepath.VolumeName(value); name != "" {
		return true
	}
	if len(value) >= 2 && value[1] == ':' && isDriveLetter(value[0]) {
		return true
	}
	slashed := filepath.ToSlash(value)
	if strings.HasPrefix(slashed, "//") {
		return true
	}
	return strings.HasPrefix(value, `\\`)
}

func isDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func absInside(cleaned, workspace string) bool {
	if cleaned == workspace {
		return true
	}
	prefix := workspace
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return strings.HasPrefix(cleaned, prefix)
}
