//go:build unix

package mcp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// buildFixtureServer builds the real MCP server in testdata once per run and
// returns its path. The tests drive an actual subprocess over an actual stdio
// transport, so what they exercise is the SDK's own handshake rather than a
// double of it.
var buildFixtureServer = sync.OnceValues(func() (string, error) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		return "", err
	}
	binary := filepath.Join(os.TempDir(), "och-mcp-fixture-server")
	build := exec.Command("go", "build",
		"-tags", "ignore_fixture",
		"-o", binary,
		"./internal/harness/adapters/mcp/testdata/fixtureserver")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		return "", errors.New(string(output))
	}
	return binary, nil
})

func fixtureServerPath(t *testing.T) string {
	t.Helper()
	path, err := buildFixtureServer()
	if err != nil {
		t.Fatalf("build fixture server: %v", err)
	}
	return path
}

// fakeCommand is the port's test implementation. Confinement itself belongs
// to localexec and is tested there; this package's job is that it asks for a
// command through the port and never constructs one itself.
type fakeCommand struct {
	cmd            *exec.Cmd
	bracketTaken   int
	bracketDone    int
	registeredPID  int
	closed         int
	failOnRegister error
}

func (c *fakeCommand) Cmd() *exec.Cmd { return c.cmd }

func (c *fakeCommand) StartBracket() func() {
	c.bracketTaken++
	return func() { c.bracketDone++ }
}

func (c *fakeCommand) Register(pid int) error {
	c.registeredPID = pid
	return c.failOnRegister
}

func (c *fakeCommand) Close() error {
	c.closed++
	return nil
}

type fakeFactory struct {
	command *fakeCommand
	err     error
	calls   int
}

func (f *fakeFactory) NewCommand(config ServerConfig) (Command, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.command, nil
}

func fixtureFactory(t *testing.T, env ...string) *fakeFactory {
	t.Helper()
	command := exec.Command(fixtureServerPath(t))
	command.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	// The real localexec-backed factory always hands over a process group
	// leader, and teardown signals the group. A fake that omitted it would
	// exercise a configuration production never uses.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &fakeFactory{command: &fakeCommand{cmd: command}}
}

func TestConnectCompletesTheRealHandshakeAgainstARealServer(t *testing.T) {
	factory := fixtureFactory(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	server, err := Connect(ctx, ServerConfig{Name: "fixture", Command: "unused"}, factory)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = server.Close() }()

	if server.Name() != "fixture" {
		t.Fatalf("Name() = %q", server.Name())
	}
	if server.Session() == nil {
		t.Fatal("Session() is nil after a successful connect")
	}
	// The handshake really happened: the server's own identity came back.
	if got := server.Session().InitializeResult().ServerInfo.Name; got != "och-fixture" {
		t.Fatalf("ServerInfo.Name = %q, want the fixture server's own identity", got)
	}
}

// TestConnectTakesAndReleasesTheStartBracketAroundTheSDKsOwnStart pins the
// answer Task 2 recorded: the SDK calls Start, so the bracket must wrap the
// SDK's Connect rather than a Start this package makes.
func TestConnectTakesAndReleasesTheStartBracketAroundTheSDKsOwnStart(t *testing.T) {
	factory := fixtureFactory(t)
	server, err := Connect(t.Context(), ServerConfig{Name: "fixture", Command: "unused"}, factory)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = server.Close() }()

	if factory.command.bracketTaken != 1 {
		t.Fatalf("StartBracket taken %d times, want 1", factory.command.bracketTaken)
	}
	if factory.command.bracketDone != 1 {
		t.Fatalf("bracket released %d times, want exactly 1", factory.command.bracketDone)
	}
}

// TestConnectRegistersTheStartedProcessForQuotaEnforcement: the pid does not
// exist until the SDK starts the process, so enrollment happens after.
func TestConnectRegistersTheStartedProcessForQuotaEnforcement(t *testing.T) {
	factory := fixtureFactory(t)
	server, err := Connect(t.Context(), ServerConfig{Name: "fixture", Command: "unused"}, factory)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = server.Close() }()

	if factory.command.registeredPID == 0 {
		t.Fatal("the started process was never registered for quota enforcement")
	}
	if got, want := factory.command.registeredPID, factory.command.cmd.Process.Pid; got != want {
		t.Fatalf("registered pid %d, want %d", got, want)
	}
}

func TestConnectFailsClosedWhenTheFactoryFails(t *testing.T) {
	sentinel := errors.New("no sandbox available")
	factory := &fakeFactory{err: sentinel}

	server, err := Connect(t.Context(), ServerConfig{Name: "fixture", Command: "x"}, factory)
	if err == nil {
		_ = server.Close()
		t.Fatal("Connect succeeded without a command")
	}
	if !errors.Is(err, ErrConnect) || !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want it to wrap both ErrConnect and the factory's own error", err)
	}
}

// TestConnectClosesTheCommandWhenTheHandshakeFails is the leak guard: a
// server that dies before the handshake must leave nothing behind.
func TestConnectClosesTheCommandWhenTheHandshakeFails(t *testing.T) {
	factory := fixtureFactory(t, "OCH_FIXTURE_MODE=exit_before_handshake")

	server, err := Connect(t.Context(), ServerConfig{Name: "fixture", Command: "unused"}, factory)
	if err == nil {
		_ = server.Close()
		t.Fatal("Connect succeeded against a server that exits immediately")
	}
	if !errors.Is(err, ErrConnect) {
		t.Fatalf("err = %v, want ErrConnect", err)
	}
	if factory.command.closed != 1 {
		t.Fatalf("command closed %d times after a failed handshake, want 1", factory.command.closed)
	}
}

// TestConnectHonoursItsContextAgainstASilentServer keeps a server that never
// speaks from hanging composition.Open forever.
func TestConnectHonoursItsContextAgainstASilentServer(t *testing.T) {
	factory := fixtureFactory(t, "OCH_FIXTURE_MODE=silent")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		server, err := Connect(ctx, ServerConfig{Name: "fixture", Command: "unused"}, factory)
		if server != nil {
			_ = server.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Connect succeeded against a server that never responds")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Connect did not return after its context expired")
	}
	if factory.command.closed != 1 {
		t.Fatalf("command closed %d times, want 1", factory.command.closed)
	}
}

func TestServerConfigValidateRejectsUnusableEntries(t *testing.T) {
	for name, config := range map[string]ServerConfig{
		"empty name":     {Name: "", Command: "x"},
		"padded name":    {Name: " srv ", Command: "x"},
		"empty command":  {Name: "srv", Command: ""},
		"blank command":  {Name: "srv", Command: "   "},
		"nothing at all": {},
	} {
		t.Run(name, func(t *testing.T) {
			if err := config.Validate(); !errors.Is(err, ErrInvalidServerConfig) {
				t.Fatalf("Validate() = %v, want ErrInvalidServerConfig", err)
			}
		})
	}
}

func TestConnectRefusesAnInvalidConfigBeforeAskingForACommand(t *testing.T) {
	factory := &fakeFactory{}
	if _, err := Connect(t.Context(), ServerConfig{Name: "", Command: "x"}, factory); !errors.Is(err, ErrInvalidServerConfig) {
		t.Fatalf("err = %v, want ErrInvalidServerConfig", err)
	}
	if factory.calls != 0 {
		t.Fatalf("the factory was asked for a command %d times despite an invalid config", factory.calls)
	}
}

func TestConnectRefusesANilFactory(t *testing.T) {
	if _, err := Connect(t.Context(), ServerConfig{Name: "srv", Command: "x"}, nil); !errors.Is(err, ErrInvalidServerConfig) {
		t.Fatalf("err = %v, want ErrInvalidServerConfig", err)
	}
}

func TestServerCloseReleasesTheCommandEvenWhenTheSessionErrors(t *testing.T) {
	factory := fixtureFactory(t)
	server, err := Connect(t.Context(), ServerConfig{Name: "fixture", Command: "unused"}, factory)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := server.Close(); err != nil && !strings.Contains(err.Error(), "unresponsive") {
		t.Fatalf("Close: %v", err)
	}
	if factory.command.closed != 1 {
		t.Fatalf("command closed %d times, want 1", factory.command.closed)
	}
	// Close is idempotent for a supervisor that defers it and also calls it.
	if err := server.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if factory.command.closed != 1 {
		t.Fatalf("command closed %d times after a second Close, want 1", factory.command.closed)
	}
}
