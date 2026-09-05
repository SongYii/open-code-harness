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
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
