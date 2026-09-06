package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// recordingExternalTools captures what dispatch forwarded, which is the only
// way to prove the model's own argument bytes reached the tool unchanged.
type recordingExternalTools struct {
	name      string
	arguments string
	result    tools.ExternalToolResult
	err       error
	calls     int
}

func (r *recordingExternalTools) Call(_ context.Context, name, arguments string) (tools.ExternalToolResult, error) {
	r.calls++
	r.name = name
	r.arguments = arguments
	return r.result, r.err
}

func externalSpec() domain.ToolSpec {
	return domain.ToolSpec{
		Name:        "mcp_srv_search",
		Description: "search",
		// The realistic shape: full JSON Schema this project's own compiler
		// cannot read, which registration accepts and per-call validation
		// degrades for.
		InputSchema: []byte(`{"type":"object","properties":{"query":{"type":"string","description":"d"}}}`),
		Source:      tools.SourceMCP,
		Risk:        domain.RiskExec,
		Mutates:     true,
	}
}

// TestCatalogPortNeedsRequiresTheExternalPortForAnMCPSpec: an MCP tool needs
// the external port and neither of the workspace ports, however it is
// risk-classified. Deriving the need from RiskExec — which every MCP tool
// carries — would demand a command runner the tool never uses.
func TestCatalogPortNeedsRequiresTheExternalPortForAnMCPSpec(t *testing.T) {
	needsFiles, needsCommands, needsExternal := catalogPortNeeds([]domain.ToolSpec{externalSpec()})
	if !needsExternal {
		t.Error("an MCP spec did not require the external port")
	}
	if needsFiles || needsCommands {
		t.Errorf("an MCP spec required workspace ports (files=%v commands=%v); it touches neither",
			needsFiles, needsCommands)
	}

	// And a builtin still requires exactly what it did before.
	needsFiles, needsCommands, needsExternal = catalogPortNeeds(tools.DefaultWorkspaceSpecs())
	if !needsFiles || !needsCommands {
		t.Error("the builtins no longer require the workspace ports")
	}
	if needsExternal {
		t.Error("the builtins now require the external port")
	}
}

// TestExternalDispatchForwardsRawArgumentsVerbatim proves the model's own
// bytes reach the tool without a fixed-field decode in between.
func TestExternalDispatchForwardsRawArgumentsVerbatim(t *testing.T) {
	external := &recordingExternalTools{result: tools.ExternalToolResult{Text: "ok"}}
	service := &Service{external: external}
	raw := `{"query":"hello","depth":{"nested":[1,2,3]},"unknown_to_this_project":true}`

	text, truncated, code, _, err := service.invokeExternalTool(
		context.Background(), externalSpec(), toolArgs{Raw: raw})
	if err != nil {
		t.Fatalf("invokeExternalTool: %v", err)
	}
	if code != "" || truncated {
		t.Fatalf("code=%q truncated=%v, want a plain success", code, truncated)
	}
	if text != "ok" {
		t.Fatalf("text = %q", text)
	}
	if external.arguments != raw {
		t.Fatalf("forwarded %q, want the model's own bytes %q", external.arguments, raw)
	}
	if external.name != "mcp_srv_search" {
		t.Fatalf("forwarded name %q, want the qualified name", external.name)
	}
}

// TestExternalToolFailureIsATurnEventNotATransportError is the distinction
// that matters most here. A tool that ran and failed must produce a tool
// failure the model can read and react to; only a call that could not reach
// the tool ends the Turn.
func TestExternalToolFailureIsATurnEventNotATransportError(t *testing.T) {
	external := &recordingExternalTools{
		result: tools.ExternalToolResult{Text: "no such file: /etc/nope", IsError: true},
	}
	service := &Service{external: external}

	text, _, code, failureText, err := service.invokeExternalTool(
		context.Background(), externalSpec(), toolArgs{Raw: `{}`})
	if err != nil {
		t.Fatalf("a tool's own failure became a transport error: %v", err)
	}
	if code != CodeExternalToolFailed {
		t.Fatalf("code = %q, want %q", code, CodeExternalToolFailed)
	}
	if failureText != "no such file: /etc/nope" {
		t.Fatalf("failureText = %q, want the tool's own message — it is the only thing that says what went wrong", failureText)
	}
	if text == "" {
		t.Fatal("the tool's own text was discarded")
	}
}

// TestExternalTransportErrorEndsTheTurn is the other half: an unreachable
// tool is not an ordinary event.
func TestExternalTransportErrorEndsTheTurn(t *testing.T) {
	sentinel := errors.New("server connection closed")
	service := &Service{external: &recordingExternalTools{err: sentinel}}

	_, _, _, _, err := service.invokeExternalTool(
		context.Background(), externalSpec(), toolArgs{Raw: `{}`})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the transport error to surface", err)
	}
}

// TestExternalFailureWithNoMessageStillSaysSomething: a tool that fails
// silently must not produce an empty failure.
func TestExternalFailureWithNoMessageStillSaysSomething(t *testing.T) {
	service := &Service{external: &recordingExternalTools{
		result: tools.ExternalToolResult{Text: "   ", IsError: true},
	}}
	_, _, code, failureText, err := service.invokeExternalTool(
		context.Background(), externalSpec(), toolArgs{Raw: `{}`})
	if err != nil {
		t.Fatalf("invokeExternalTool: %v", err)
	}
	if code != CodeExternalToolFailed {
		t.Fatalf("code = %q", code)
	}
	if failureText != ToolTextExternalFailed {
		t.Fatalf("failureText = %q, want the generic stand-in", failureText)
	}
}

// TestExternalResultIsBoundedLikeEveryOtherToolResult: an external tool's
// output is externally supplied and gets this project's own size bound.
func TestExternalResultIsBoundedLikeEveryOtherToolResult(t *testing.T) {
	huge := make([]byte, MaxToolResultBytes+4096)
	for i := range huge {
		huge[i] = 'x'
	}
	service := &Service{external: &recordingExternalTools{
		result: tools.ExternalToolResult{Text: string(huge)},
	}}

	text, truncated, code, _, err := service.invokeExternalTool(
		context.Background(), externalSpec(), toolArgs{Raw: `{}`})
	if err != nil {
		t.Fatalf("invokeExternalTool: %v", err)
	}
	if code != "" {
		t.Fatalf("code = %q, want a truncated success rather than a failure", code)
	}
	if !truncated {
		t.Fatal("an oversized external result was not marked truncated")
	}
	// The Tool runtime contract's documented shape for every truncated tool
	// result is "prefix + \n[truncated]" — the marker is appended after the
	// bound, not squeezed inside it, and the builtins do the same. An
	// external result must be bounded the same way rather than inventing its
	// own convention.
	if !strings.HasSuffix(text, TruncationMarker) {
		t.Fatalf("a truncated external result does not carry the marker: %q", text[max(0, len(text)-40):])
	}
	if prefix := strings.TrimSuffix(text, TruncationMarker); len(prefix) != MaxToolResultBytes {
		t.Fatalf("truncated prefix is %d bytes, want exactly the %d-byte bound", len(prefix), MaxToolResultBytes)
	}
}

// TestInvokeToolRoutesAnExternalSpecBySourceNotByName goes through invokeTool
// rather than calling the forwarding function directly, so the dispatch
// branch itself is load-bearing.
//
// It exists because a mutation removing that branch left the direct-call test
// green: a test that skips the entry point cannot prove the entry point
// works. An MCP tool's name is chosen by the operator and the server, so the
// closed name switch can never match it — without the source branch the call
// falls through to "unknown tool".
func TestInvokeToolRoutesAnExternalSpecBySourceNotByName(t *testing.T) {
	external := &recordingExternalTools{result: tools.ExternalToolResult{Text: "routed"}}
	service := &Service{external: external}

	text, _, code, _, err := service.invokeTool(
		context.Background(), externalSpec(), toolArgs{Raw: `{"query":"x"}`}, "")
	if err != nil {
		t.Fatalf("invokeTool: %v", err)
	}
	if code == CodeUnknownTool {
		t.Fatal("invokeTool fell through to the unknown-tool default; the source branch is missing")
	}
	if code != "" {
		t.Fatalf("code = %q, want a plain success", code)
	}
	if text != "routed" {
		t.Fatalf("text = %q, want the external tool's own result", text)
	}
	if external.calls != 1 {
		t.Fatalf("the external port was called %d times, want 1", external.calls)
	}
}

// stubFiles is the smallest FileSystem that lets a builtin branch run far
// enough to prove it was taken. It reads back a fixed byte, which is all the
// routing assertion needs.
type stubFiles struct{}

func (stubFiles) Resolve(context.Context, string, string) (string, error) { return "/abs", nil }
func (stubFiles) Read(context.Context, string, int) ([]byte, bool, error) {
	return []byte("builtin"), false, nil
}
func (stubFiles) Write(context.Context, string, []byte) error { return nil }
func (stubFiles) List(context.Context, string, int, int) ([]string, bool, error) {
	return nil, false, nil
}

// TestInvokeToolStillRoutesBuiltinsByName keeps the new source branch from
// swallowing the four builtins.
func TestInvokeToolStillRoutesBuiltinsByName(t *testing.T) {
	external := &recordingExternalTools{result: tools.ExternalToolResult{Text: "must not be used"}}
	service := &Service{external: external, files: stubFiles{}}

	builtin := tools.DefaultWorkspaceSpecs()[0] // read_file
	text, _, code, _, err := service.invokeTool(
		context.Background(), builtin, toolArgs{Raw: `{"path":"x"}`, Path: "x"}, "/abs")
	if err != nil {
		t.Fatalf("invokeTool: %v", err)
	}
	if code != "" {
		t.Fatalf("code = %q, want the builtin to have run", code)
	}
	if text != "builtin" {
		t.Fatalf("text = %q, want the builtin filesystem's own result", text)
	}
	if external.calls != 0 {
		t.Fatalf("a builtin was dispatched to the external port %d times", external.calls)
	}
}
