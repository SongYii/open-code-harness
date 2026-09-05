//go:build ignore_fixture

// Command fixtureserver is a real MCP server used only by this package's
// tests. It is built at test time and driven over a real stdio transport, so
// the tests exercise the actual SDK handshake rather than a hand-written
// double of it.
//
// The build tag keeps it out of the ordinary package graph; the test builds
// it explicitly with the tag set.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addRawTool registers a tool whose input schema is exactly the given JSON,
// bypassing the SDK's schema inference so a test can present the schema
// shapes a real third-party server would.
func addRawTool(server *mcp.Server, name, description, schema string) {
	server.AddTool(&mcp.Tool{
		Name:        name,
		Description: description,
		InputSchema: json.RawMessage(schema),
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	})
}

func runServer(server *mcp.Server) {
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Printf("fixture server failed: %v", err)
	}
}

func main() {
	// OCH_FIXTURE_MODE lets one binary cover the failure shapes the tests
	// need without a second program.
	switch os.Getenv("OCH_FIXTURE_MODE") {
	case "exit_before_handshake":
		// A server that dies immediately: Connect must fail closed rather
		// than hang or report a connected session.
		os.Exit(3)
	case "silent":
		// A server that never speaks: Connect must be governed by the
		// caller's context rather than blocking forever.
		select {}
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "och-fixture", Version: "v1"}, nil)

	// Discovery fixtures. Each mode makes the server return a listing shape
	// the discovery tests need to observe, including the hostile ones.
	switch os.Getenv("OCH_FIXTURE_TOOLS") {
	case "flood":
		// One more than the bound, to prove the count limit stops it.
		for i := 0; i <= 256; i++ {
			addRawTool(server, fmt.Sprintf("flood%d", i), "x", `{"type":"object"}`)
		}
		runServer(server)
		return
	case "oversized":
		addRawTool(server, "huge", strings.Repeat("d", 70000), `{"type":"object"}`)
		runServer(server)
		return
	case "unregisterable":
		// A schema that is a valid JSON object but larger than the catalog
		// accepts: it clears discovery's own definition bound and is refused
		// at registration, so this one tool must be dropped rather than
		// failing the whole catalog and with it composition.Open.
		//
		// A schema that is not an object at all would be the sharper case,
		// but this SDK's server side refuses to publish one — AddTool panics
		// with "can't marshal input schema to a JSON object". Only a non-SDK
		// server could send it, so that case is covered as a direct
		// projection test instead.
		addRawTool(server, "bad", "oversized schema",
			`{"type":"object","description":"`+strings.Repeat("p", 40000)+`"}`)
		addRawTool(server, "good", "usable", `{"type":"object"}`)
		runServer(server)
		return
	case "realistic":
		// The shape published MCP tools actually ship, which this project's
		// own schema compiler cannot read.
		addRawTool(server, "search", "search things",
			`{"type":"object","properties":{"query":{"type":"string","description":"what to search for"},"limit":{"type":"number"}},"required":["query"]}`)
		// And one in this project's own dialect, which it can.
		addRawTool(server, "strict", "strict args",
			`{"type":"object","additionalProperties":false,"required":["path"],"properties":{"path":{"type":"string","minLength":1}}}`)
		runServer(server)
		return
	case "hostile_names":
		addRawTool(server, "a/b c", "punctuation", `{"type":"object"}`)
		addRawTool(server, strings.Repeat("z", 300), "very long", `{"type":"object"}`)
		runServer(server)
		return
	}

	type greetArgs struct {
		Name string `json:"name" jsonschema:"the person to greet"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "greet",
		Description: "say hi",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args greetArgs) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Hi " + args.Name}},
		}, nil, nil
	})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Printf("fixture server failed: %v", err)
	}
}
