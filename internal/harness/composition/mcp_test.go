//go:build unix

package composition_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/adapters/mcp"
	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/tools"
)

// buildMCPFixtureServer builds the MCP adapter's own fixture server once, so
// composition can be tested against a real server subprocess rather than a
// double of one.
var buildMCPFixtureServer = sync.OnceValues(func() (string, error) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		return "", err
	}
	binary := filepath.Join(os.TempDir(), "och-composition-mcp-fixture")
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

// fixtureServerConfig returns a server config whose command is a copy of the
// fixture server placed inside the workspace, because localexec's own
// admission refuses an argv0 that resolves outside the workspace.
func fixtureServerConfig(t *testing.T, workspace, name string) mcp.ServerConfig {
	t.Helper()
	source, err := buildMCPFixtureServer()
	if err != nil {
		t.Fatalf("build fixture server: %v", err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read fixture server: %v", err)
	}
	destination := filepath.Join(workspace, "mcp-server-"+name)
	if err := os.WriteFile(destination, data, 0o700); err != nil {
		t.Fatalf("place fixture server: %v", err)
	}
	return mcp.ServerConfig{Name: name, Command: destination}
}

// TestOpenWithNoMCPServersIsUnchanged is the guard that matters most here.
// Almost nobody configures an MCP server, and their assembly must be byte for
// byte the one they have today: same catalog, same four tools, same shutdown.
func TestOpenWithNoMCPServersIsUnchanged(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")
	config.MCPServers = nil

	assembly, err := composition.Open(t.Context(), config)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = assembly.Close() }()

	specs := assembly.Catalog().Specs()
	if len(specs) != len(tools.DefaultWorkspaceSpecs()) {
		t.Fatalf("catalog holds %d specs, want exactly the %d builtins",
			len(specs), len(tools.DefaultWorkspaceSpecs()))
	}
	for _, spec := range specs {
		if spec.Source != tools.SourceBuiltin {
			t.Fatalf("spec %q has source %q in an assembly with no MCP servers", spec.Name, spec.Source)
		}
	}
}

// TestOpenRegistersDiscoveredToolsInTheSameCatalogAsBuiltins is the point of
// the whole slice: one catalog, so external tools inherit the Policy table
// and approval gate rather than needing a second mechanism.
func TestOpenRegistersDiscoveredToolsInTheSameCatalogAsBuiltins(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")
	config.MCPServers = []mcp.ServerConfig{fixtureServerConfig(t, config.WorkspaceRoot, "fixture")}

	assembly, err := composition.Open(t.Context(), config)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = assembly.Close() }()

	specs := assembly.Catalog().Specs()
	builtins, discovered := 0, 0
	for _, spec := range specs {
		switch spec.Source {
		case tools.SourceBuiltin:
			builtins++
		case tools.SourceMCP:
			discovered++
			if !strings.HasPrefix(spec.Name, "mcp_fixture_") {
				t.Errorf("discovered spec %q is not qualified with its server name", spec.Name)
			}
		}
	}
	if builtins != len(tools.DefaultWorkspaceSpecs()) {
		t.Errorf("builtins = %d, want %d — the builtins must not be displaced", builtins, len(tools.DefaultWorkspaceSpecs()))
	}
	if discovered == 0 {
		t.Fatal("no MCP tool reached the catalog")
	}
}

// TestOpenFailsClosedWhenAConfiguredServerCannotBeReached: an operator asked
// for these tools. Starting without them and reporting success would make
// them look absent rather than broken.
func TestOpenFailsClosedWhenAConfiguredServerCannotBeReached(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")
	config.MCPServers = []mcp.ServerConfig{{
		Name:    "missing",
		Command: filepath.Join(config.WorkspaceRoot, "no-such-server"),
	}}

	assembly, err := composition.Open(t.Context(), config)
	if err == nil {
		_ = assembly.Close()
		t.Fatal("Open succeeded with an unreachable MCP server")
	}
	if !strings.Contains(err.Error(), "mcp") {
		t.Fatalf("err = %v, want it to name the MCP failure", err)
	}
}

// TestOpenFailsClosedWhenAServerExitsImmediately covers the other reachable
// failure: the process starts and dies before the handshake.
func TestOpenFailsClosedWhenAServerExitsImmediately(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")
	server := fixtureServerConfig(t, config.WorkspaceRoot, "dying")
	// Selected by argv, not the environment: a confined child's environment
	// is a three-name whitelist, so an env switch cannot reach it. That is
	// the confinement working as designed, and this test would otherwise be
	// silently testing nothing.
	server.Args = []string{"--mode=exit_before_handshake"}
	config.MCPServers = []mcp.ServerConfig{server}

	assembly, err := composition.Open(t.Context(), config)
	if err == nil {
		_ = assembly.Close()
		t.Fatal("Open succeeded against a server that exits before the handshake")
	}
	if !strings.Contains(err.Error(), "mcp") {
		t.Fatalf("err = %v, want it to name the MCP failure", err)
	}
}

// TestOpenRejectsTwoServersConfiguredWithTheSameName catches an operator
// error at startup, and names the configuration rather than a derived tool
// name, which is what NewCatalog would have reported instead.
func TestOpenRejectsTwoServersConfiguredWithTheSameName(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")
	first := fixtureServerConfig(t, config.WorkspaceRoot, "same")
	second := first
	config.MCPServers = []mcp.ServerConfig{first, second}

	assembly, err := composition.Open(t.Context(), config)
	if err == nil {
		_ = assembly.Close()
		t.Fatal("Open accepted two servers with the same name")
	}
	if !strings.Contains(err.Error(), "same") {
		t.Fatalf("err = %v, want it to name the duplicated server", err)
	}
}

// TestOpenTearsDownEarlierServersWhenALaterOneFails keeps a partial failure
// from leaking the servers that already connected.
func TestOpenTearsDownEarlierServersWhenALaterOneFails(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")
	good := fixtureServerConfig(t, config.WorkspaceRoot, "good")
	config.MCPServers = []mcp.ServerConfig{
		good,
		{Name: "broken", Command: filepath.Join(config.WorkspaceRoot, "no-such-server")},
	}

	assembly, err := composition.Open(t.Context(), config)
	if err == nil {
		_ = assembly.Close()
		t.Fatal("Open succeeded despite a broken server")
	}
	if leaked := countRunning(t, "mcp-server-good"); leaked != 0 {
		t.Fatalf("%d fixture server processes survived a failed Open", leaked)
	}
}

// TestCloseStopsEveryConnectedServer holds the lifetime promise: an assembly
// that started server processes must not leave them behind.
func TestCloseStopsEveryConnectedServer(t *testing.T) {
	config := validConfig(t)
	t.Setenv(config.Provider.APIKeyEnv, "contract-key")
	config.MCPServers = []mcp.ServerConfig{fixtureServerConfig(t, config.WorkspaceRoot, "closer")}

	assembly, err := composition.Open(t.Context(), config)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if running := countRunning(t, "mcp-server-closer"); running == 0 {
		t.Fatal("the configured server never started")
	}
	if err := assembly.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if leaked := countRunning(t, "mcp-server-closer"); leaked != 0 {
		t.Fatalf("%d server processes survived Close", leaked)
	}
}

// countRunning reports how many processes have marker in their command line.
func countRunning(t *testing.T, marker string) int {
	t.Helper()
	output, err := exec.Command("ps", "-eo", "args").Output()
	if err != nil {
		t.Skipf("ps unavailable: %v", err)
	}
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, marker) && !strings.Contains(line, "ps -eo") {
			count++
		}
	}
	return count
}
