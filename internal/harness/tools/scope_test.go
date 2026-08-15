package tools

import "testing"

func TestCheckScopeLexicalDeniesEscapesWithoutIO(t *testing.T) {
	const workspace = "/workspace"
	tests := []struct {
		name      string
		workspace string
		requested string
		in        bool
		reason    string
	}{
		{name: "relative file", workspace: workspace, requested: "README.md", in: true, reason: ReasonInWorkspace},
		{name: "relative nested", workspace: workspace, requested: "src/foo.go", in: true, reason: ReasonInWorkspace},
		{name: "dot", workspace: workspace, requested: ".", in: true, reason: ReasonInWorkspace},
		{name: "lexical collapse inside", workspace: workspace, requested: "foo/../bar", in: true, reason: ReasonInWorkspace},
		{name: "abs inside", workspace: workspace, requested: "/workspace/src", in: true, reason: ReasonInWorkspace},
		{name: "abs workspace itself", workspace: workspace, requested: "/workspace", in: true, reason: ReasonInWorkspace},
		{name: "parent escape", workspace: workspace, requested: "../etc/passwd", reason: ReasonLeftoverDotDot},
		{name: "nested parent escape", workspace: workspace, requested: "subdir/../../etc/passwd", reason: ReasonLeftoverDotDot},
		{name: "backslash parent escape", workspace: workspace, requested: `..\..\etc\passwd`, reason: ReasonLeftoverDotDot},
		{name: "abs outside", workspace: workspace, requested: "/etc/passwd", reason: ReasonAbsPrefixMismatch},
		{name: "abs sibling prefix", workspace: workspace, requested: "/workspace-evil/x", reason: ReasonAbsPrefixMismatch},
		{name: "abs cleaned outside", workspace: workspace, requested: "/workspace/../etc/passwd", reason: ReasonAbsPrefixMismatch},
		{name: "nul", workspace: workspace, requested: "foo\x00bar", reason: ReasonNUL},
		{name: "empty", workspace: workspace, requested: "", reason: ReasonEmpty},
		{name: "spaces", workspace: workspace, requested: "   ", reason: ReasonEmpty},
		{name: "empty workspace", workspace: "", requested: "src", reason: ReasonEmptyWorkspace},
		{name: "windows drive", workspace: workspace, requested: `C:\Windows\System32`, reason: ReasonWindowsVolume},
		{name: "windows drive slash", workspace: workspace, requested: "C:/Windows", reason: ReasonWindowsVolume},
		{name: "unc", workspace: workspace, requested: `\\server\share\file`, reason: ReasonWindowsVolume},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := CheckScopeLexical(ScopeRequest{WorkspaceRoot: test.workspace, Requested: test.requested})
			if err != nil {
				t.Fatalf("CheckScopeLexical() error = %v", err)
			}
			if got.InWorkspace != test.in || got.Reason != test.reason {
				t.Fatalf("CheckScopeLexical() = %#v, want in=%t reason=%s", got, test.in, test.reason)
			}
		})
	}
}

func TestCheckScopeLexicalDoesNotImportHostIO(t *testing.T) {
	// The function is lexical: a path that would require Stat still returns a result.
	got, err := CheckScopeLexical(ScopeRequest{WorkspaceRoot: "/workspace", Requested: "missing/no-such-file"})
	if err != nil || !got.InWorkspace {
		t.Fatalf("missing relative path should stay lexical in-workspace, got %#v err=%v", got, err)
	}
}
