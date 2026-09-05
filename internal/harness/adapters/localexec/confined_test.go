package localexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

func confinedRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	workspace := t.TempDir()
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	runner, err := New(resolved)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = runner.Close() })
	return runner, resolved
}

func mustConfined(t *testing.T, runner *Runner, workspace string) *ConfinedCommand {
	t.Helper()
	confined, err := runner.NewConfinedCommand(tools.CommandSpec{
		Argv: []string{"echo", "hello"},
		Cwd:  workspace,
	})
	if err != nil {
		t.Fatalf("NewConfinedCommand: %v", err)
	}
	t.Cleanup(func() { _ = confined.Close() })
	return confined
}

// TestNewConfinedCommandReturnsAnUnstartedCommand is the whole reason this
// entry point exists. Run starts and waits; an MCP stdio transport needs the
// command handed over unstarted, because the SDK's own CommandTransport calls
// Start after taking the pipes.
func TestNewConfinedCommandReturnsAnUnstartedCommand(t *testing.T) {
	runner, workspace := confinedRunner(t)
	confined := mustConfined(t, runner, workspace)

	command := confined.Cmd()
	if command == nil {
		t.Fatal("Cmd() returned nil")
	}
	if command.Process != nil {
		t.Fatal("the command was already started; the caller owns Start")
	}
	if command.Stdout != nil || command.Stderr != nil || command.Stdin != nil {
		t.Fatal("stdio was pre-wired; the caller owns the pipes")
	}
}

// TestConfinedCommandCarriesSetpgid keeps teardown able to signal the whole
// process group. The SDK's own Close ladder ends at Process.Kill(), which
// reaches only the immediate child; an MCP server that spawns its own
// children would leak without this.
func TestConfinedCommandCarriesSetpgid(t *testing.T) {
	runner, workspace := confinedRunner(t)
	confined := mustConfined(t, runner, workspace)

	attr := confined.Cmd().SysProcAttr
	if !sysProcAttrSetsProcessGroup(attr) {
		t.Fatalf("SysProcAttr = %+v, want a process group leader on this platform", attr)
	}
}

// TestConfinedCommandEnvironmentIsAWhitelistNotTheParent mirrors Run's own
// environment discipline: a confined child never inherits os.Environ().
func TestConfinedCommandEnvironmentIsAWhitelistNotTheParent(t *testing.T) {
	t.Setenv("OCH_SECRET_FIXTURE", "must-not-leak")
	runner, workspace := confinedRunner(t)
	confined := mustConfined(t, runner, workspace)

	env := confined.Cmd().Env
	if env == nil {
		t.Fatal("Env is nil, so the child would inherit the parent environment")
	}
	for _, assignment := range env {
		if strings.HasPrefix(assignment, "OCH_SECRET_FIXTURE=") {
			t.Fatalf("child environment leaked %q", assignment)
		}
	}
	// The same three names Run sets, and nothing else.
	names := make([]string, 0, len(env))
	for _, assignment := range env {
		names = append(names, assignment[:strings.IndexByte(assignment, '=')])
	}
	want := map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true}
	for _, name := range names {
		if !want[name] {
			t.Errorf("unexpected child environment name %q", name)
		}
	}
	if len(names) != len(want) {
		t.Errorf("child environment names = %v, want exactly %v", names, want)
	}
}

// TestConfinedCommandAppliesTheSameConfinementRunDoes checks the wrapping by
// argv shape rather than re-testing the sandbox itself: whatever backend is
// available here, the confined path must reach for the same one Run does.
func TestConfinedCommandAppliesTheSameConfinementRunDoes(t *testing.T) {
	runner, workspace := confinedRunner(t)
	confined := mustConfined(t, runner, workspace)

	got := confined.Cmd().Path
	switch {
	case runner.bwrapAvailable:
		if filepath.Base(got) != "bwrap" {
			t.Fatalf("argv0 = %q, want the bwrap wrapper", got)
		}
	case runner.seatbeltAvailable:
		if filepath.Base(got) != "sandbox-exec" {
			t.Fatalf("argv0 = %q, want the Seatbelt wrapper", got)
		}
	default:
		if filepath.Base(got) != "echo" {
			t.Fatalf("argv0 = %q, want the bare command when no backend is available", got)
		}
	}
}

// TestConfinedCommandCloseReleasesItsTemporaryDirectory: Run scopes its temp
// directory to one call with a defer. A long-lived process outlives any call,
// so the handle owns it instead — and must actually release it.
func TestConfinedCommandCloseReleasesItsTemporaryDirectory(t *testing.T) {
	runner, workspace := confinedRunner(t)
	confined, err := runner.NewConfinedCommand(tools.CommandSpec{
		Argv: []string{"echo"},
		Cwd:  workspace,
	})
	if err != nil {
		t.Fatalf("NewConfinedCommand: %v", err)
	}
	dir := confined.TempDir()
	if dir == "" {
		t.Fatal("TempDir() is empty")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("temp dir does not exist before Close: %v", err)
	}
	if err := confined.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("temp dir %q survived Close (err=%v)", dir, err)
	}
	// Close is idempotent: a supervisor may close on both an error path and
	// a deferred cleanup.
	if err := confined.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestConfinedCommandRejectsTheSameSpecsRunDoes keeps the two entry points
// from diverging on admission: a workspace escape or an empty argv must fail
// here exactly as it fails in Run.
//
// A nonexistent argv0 *inside* the workspace is deliberately absent from this
// table. Run accepts it at construction too and fails at start, because
// resolveArgv0 resolves the parent rather than requiring the leaf to exist.
// Rejecting it here would make the confined path stricter than Run, which is
// a divergence in the other direction.
func TestConfinedCommandRejectsTheSameSpecsRunDoes(t *testing.T) {
	runner, workspace := confinedRunner(t)
	for name, spec := range map[string]tools.CommandSpec{
		"empty argv":             {Argv: nil, Cwd: workspace},
		"empty argv0":            {Argv: []string{""}, Cwd: workspace},
		"empty cwd":              {Argv: []string{"echo"}, Cwd: ""},
		"cwd outside":            {Argv: []string{"echo"}, Cwd: filepath.Dir(workspace)},
		"argv0 absolute outside": {Argv: []string{"/bin/sh"}, Cwd: workspace},
		"argv0 relative escape":  {Argv: []string{"../escape"}, Cwd: workspace},
	} {
		t.Run(name, func(t *testing.T) {
			confined, err := runner.NewConfinedCommand(spec)
			if err == nil {
				_ = confined.Close()
				t.Fatalf("NewConfinedCommand accepted %+v", spec)
			}
		})
	}
}

// TestConfinedCommandRunsAndIsReapable proves the handed-over command is
// actually usable: the caller starts it, it runs under whatever confinement
// is available, and it exits.
func TestConfinedCommandRunsAndIsReapable(t *testing.T) {
	runner, workspace := confinedRunner(t)
	confined, err := runner.NewConfinedCommand(tools.CommandSpec{
		Argv: []string{"echo", "confined-hello"},
		Cwd:  workspace,
	})
	if err != nil {
		t.Fatalf("NewConfinedCommand: %v", err)
	}
	defer func() { _ = confined.Close() }()

	command := confined.Cmd()
	output, err := command.Output()
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if !strings.Contains(string(output), "confined-hello") {
		t.Fatalf("output = %q, want it to contain the echoed text", output)
	}
	if err := confined.Register(command.Process.Pid); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

// TestStartBracketIsAvailableToTheCaller records the answer the plan's Step 3
// demands rather than leaving it inherited. Run holds the platform's
// pre-Start resource bracket around its own cmd.Start. On this path the
// caller owns Start — for MCP it is the SDK's own Connect — so the bracket is
// exposed instead of applied here, and it must be releasable exactly once.
func TestStartBracketIsAvailableToTheCaller(t *testing.T) {
	runner, workspace := confinedRunner(t)
	confined := mustConfined(t, runner, workspace)

	release := confined.StartBracket()
	if release == nil {
		t.Fatal("StartBracket returned nil; the caller has no way to apply the bracket")
	}
	release()

	// Taking it again must not deadlock on the runner's own bracket mutex.
	confined.StartBracket()()
}

// TestRunIsUnchangedByTheExtraction pins the behavior most easily broken by
// factoring construction out of Run.
func TestRunIsUnchangedByTheExtraction(t *testing.T) {
	runner, workspace := confinedRunner(t)
	result, err := runner.Run(t.Context(), tools.CommandSpec{
		Argv: []string{"echo", "still-works"},
		Cwd:  workspace,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result.Output, "still-works") {
		t.Fatalf("Run output = %q", result.Output)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}
}
