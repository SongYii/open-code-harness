package tools

import (
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func TestValidateArgsDefaultWorkspaceSpecs(t *testing.T) {
	specs := specIndex(t)
	tests := []struct {
		name    string
		tool    string
		raw     string
		wantErr bool
	}{
		{name: "read path", tool: NameReadFile, raw: `{"path":"README.md"}`},
		{name: "read missing path", tool: NameReadFile, raw: `{}`, wantErr: true},
		{name: "read empty path", tool: NameReadFile, raw: `{"path":""}`, wantErr: true},
		{name: "read extra field", tool: NameReadFile, raw: `{"path":"a","extra":1}`, wantErr: true},
		{name: "read not object", tool: NameReadFile, raw: `[]`, wantErr: true},
		{name: "read trailing", tool: NameReadFile, raw: `{"path":"a"}{"x":1}`, wantErr: true},
		{name: "write ok", tool: NameWriteFile, raw: `{"path":"a.txt","content":"hi"}`},
		{name: "write empty content", tool: NameWriteFile, raw: `{"path":"a.txt","content":""}`},
		{name: "write missing content", tool: NameWriteFile, raw: `{"path":"a.txt"}`, wantErr: true},
		{name: "write content too long", tool: NameWriteFile, raw: `{"path":"a.txt","content":"` + strings.Repeat("a", MaxWriteContentBytes+1) + `"}`, wantErr: true},
		{name: "write content at bound", tool: NameWriteFile, raw: `{"path":"a.txt","content":"` + strings.Repeat("a", MaxWriteContentBytes) + `"}`},
		{name: "list omitted depth", tool: NameListDir, raw: `{"path":"src"}`},
		{name: "list depth 1", tool: NameListDir, raw: `{"path":"src","depth":1}`},
		{name: "list depth 2", tool: NameListDir, raw: `{"path":"src","depth":2}`},
		{name: "list depth 0", tool: NameListDir, raw: `{"path":"src","depth":0}`, wantErr: true},
		{name: "list depth 3", tool: NameListDir, raw: `{"path":"src","depth":3}`, wantErr: true},
		{name: "list depth 1.5", tool: NameListDir, raw: `{"path":"src","depth":1.5}`, wantErr: true},
		{name: "list depth string", tool: NameListDir, raw: `{"path":"src","depth":"1"}`, wantErr: true},
		{name: "exec argv", tool: NameExec, raw: `{"argv":["go","test"]}`},
		{name: "exec cwd", tool: NameExec, raw: `{"argv":["true"],"cwd":"subdir"}`},
		{name: "exec empty argv", tool: NameExec, raw: `{"argv":[]}`, wantErr: true},
		{name: "exec missing argv", tool: NameExec, raw: `{"cwd":"."}`, wantErr: true},
		{name: "exec timeout field", tool: NameExec, raw: `{"argv":["true"],"timeout":30}`, wantErr: true},
		{name: "exec command string", tool: NameExec, raw: `{"command":"true"}`, wantErr: true},
		{name: "invalid utf8", tool: NameReadFile, raw: "{\x80}", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateArgs(specs[test.tool], test.raw)
			if test.wantErr {
				if !IsCode(err, CodeInvalidArgs) {
					t.Fatalf("ValidateArgs() error = %v, want %s", err, CodeInvalidArgs)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateArgs() error = %v", err)
			}
		})
	}
}

func specIndex(t *testing.T) map[string]domain.ToolSpec {
	t.Helper()
	out := make(map[string]domain.ToolSpec)
	for _, spec := range DefaultWorkspaceSpecs() {
		out[spec.Name] = spec
	}
	return out
}
