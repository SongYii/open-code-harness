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

// CheckScopeLexical is a no-I/O jail. Leftover "..", NUL, foreign Windows
// volumes, and absolute prefix mismatch deny. A volume on Requested is an
// escape only when it does not match the workspace volume; a Windows
// workspace itself is not denied. Symlinks are not followed.
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
	if requested, workspace := windowsVolume(req.Requested), windowsVolume(req.WorkspaceRoot); requested != "" && !strings.EqualFold(requested, workspace) {
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

func slashNormalize(value string) string {
	// ToSlash only maps os.PathSeparator; treat '\' as a separator on Unix too
	// so model-emitted Windows escapes stay lexical.
	return strings.ReplaceAll(filepath.ToSlash(value), "\\", "/")
}

func slashClean(value string) string {
	volume, rest := splitWindowsVolume(value)
	rest = slashNormalize(rest)
	if volume == "" {
		return path.Clean(rest)
	}
	if rest == "" {
		rest = "/"
	} else if !strings.HasPrefix(rest, "/") {
		rest = "/" + rest
	}
	// Root the remainder so C:/proj/../Windows becomes C:/Windows, not "Windows".
	return volume + path.Clean(rest)
}

func isLexicalAbs(value string) bool {
	if windowsVolume(value) != "" {
		return true
	}
	normalized := slashNormalize(value)
	return path.IsAbs(normalized) || filepath.IsAbs(value)
}

func hasLeftoverDotDot(cleaned string) bool {
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return true
	}
	if volume, rest := splitWindowsVolume(cleaned); volume != "" {
		return rest == "/.." || strings.HasPrefix(rest, "/../")
	}
	return false
}

func windowsVolume(value string) string {
	vol, _ := splitWindowsVolume(value)
	return vol
}

func splitWindowsVolume(value string) (string, string) {
	if name := filepath.VolumeName(value); name != "" {
		return normalizeWindowsVolume(name), value[len(name):]
	}
	normalized := slashNormalize(value)
	if len(normalized) >= 2 && normalized[1] == ':' && isDriveLetter(normalized[0]) {
		return strings.ToUpper(normalized[:1]) + ":", normalized[2:]
	}
	if !strings.HasPrefix(normalized, "//") {
		return "", normalized
	}
	parts := strings.Split(strings.TrimPrefix(normalized, "//"), "/")
	nonempty := make([]string, 0, 2)
	for _, part := range parts {
		if part != "" {
			nonempty = append(nonempty, part)
		}
	}
	switch len(nonempty) {
	case 0:
		return "//", normalized[2:]
	case 1:
		vol := "//" + strings.ToLower(nonempty[0])
		return vol, strings.TrimPrefix(strings.ToLower(normalized), strings.ToLower(vol))
	default:
		vol := "//" + strings.ToLower(nonempty[0]) + "/" + strings.ToLower(nonempty[1])
		if len(normalized) >= len(vol) && strings.EqualFold(normalized[:len(vol)], vol) {
			return vol, normalized[len(vol):]
		}
		return vol, normalized[2:]
	}
}

func normalizeWindowsVolume(name string) string {
	if len(name) == 2 && name[1] == ':' && isDriveLetter(name[0]) {
		return strings.ToUpper(name[:1]) + ":"
	}
	return slashNormalize(name)
}

func isDriveLetter(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func absInside(cleaned, workspace string) bool {
	// Windows volumes are case-insensitive; POSIX prefixes stay exact.
	if windowsVolume(cleaned) != "" || windowsVolume(workspace) != "" {
		cleaned = strings.ToLower(cleaned)
		workspace = strings.ToLower(workspace)
	}
	if cleaned == workspace {
		return true
	}
	prefix := workspace
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return strings.HasPrefix(cleaned, prefix)
}
