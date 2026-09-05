//go:build unix

package mcp

import (
	"errors"
	"strings"
	"testing"
)

func connectedFixture(t *testing.T, mode string) *Server {
	t.Helper()
	factory := fixtureFactory(t, "OCH_FIXTURE_TOOLS="+mode)
	server, err := Connect(t.Context(), ServerConfig{Name: "srv", Command: "unused"}, factory)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if _, err := server.Discover(t.Context()); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return server
}

// TestCallForwardsArgumentsVerbatimAndReturnsTheToolsText is the ordinary
// path: the model's own argument JSON reaches the server unchanged and the
// tool's text comes back.
func TestCallForwardsArgumentsVerbatimAndReturnsTheToolsText(t *testing.T) {
	server := connectedFixture(t, "")

	names := server.QualifiedNames()
	if len(names) == 0 {
		t.Fatal("the fixture contributed no tools")
	}
	result, err := server.Call(t.Context(), names[0], `{"name":"world"}`)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("the tool reported a failure: %q", result.Text)
	}
	if !strings.Contains(result.Text, "world") {
		t.Fatalf("Text = %q, want it to carry the argument through to the tool", result.Text)
	}
}

// TestCallRefusesANameNoServerProvides keeps one server from answering for a
// tool it never offered.
func TestCallRefusesANameNoServerProvides(t *testing.T) {
	server := connectedFixture(t, "")

	_, err := server.Call(t.Context(), "mcp_srv_not_a_real_tool", `{}`)
	if !errors.Is(err, ErrInvalidServerConfig) {
		t.Fatalf("err = %v, want ErrInvalidServerConfig", err)
	}
}

// TestCallRefusesADisconnectedServer.
func TestCallRefusesADisconnectedServer(t *testing.T) {
	var server *Server
	if _, err := server.Call(t.Context(), "mcp_srv_x", `{}`); !errors.Is(err, ErrInvalidServerConfig) {
		t.Fatalf("err = %v, want ErrInvalidServerConfig", err)
	}
}

// TestQualifiedNamesAreOnlyKnownAfterDiscovery: the mapping from a Catalog
// name back to the server's own name is produced by discovery, so a server
// that was never asked for its tools cannot route a call.
func TestQualifiedNamesAreOnlyKnownAfterDiscovery(t *testing.T) {
	factory := fixtureFactory(t)
	server, err := Connect(t.Context(), ServerConfig{Name: "srv", Command: "unused"}, factory)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = server.Close() }()

	if names := server.QualifiedNames(); len(names) != 0 {
		t.Fatalf("QualifiedNames() = %v before discovery, want none", names)
	}
	if _, err := server.Discover(t.Context()); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if names := server.QualifiedNames(); len(names) == 0 {
		t.Fatal("QualifiedNames() is still empty after discovery")
	}
}

// TestCallSendsEmptyArgumentsAsAbsentRatherThanAsEmptyString: a tool taking
// no arguments must not be handed a malformed payload.
func TestCallSendsEmptyArgumentsAsAbsentRatherThanAsEmptyString(t *testing.T) {
	server := connectedFixture(t, "")
	names := server.QualifiedNames()

	// The fixture's greet tool tolerates a missing name; what matters is that
	// the call reaches it rather than failing to encode.
	if _, err := server.Call(t.Context(), names[0], "   "); err != nil {
		t.Fatalf("Call with blank arguments: %v", err)
	}
}
