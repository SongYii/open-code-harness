// Command acp-web-bridge is a dumb NDJSON-line-to-WebSocket-frame relay,
// not a second ACP client: it spawns an ACP v1 agent over stdio (mirroring
// cmd/acp-client's own -agent/-cwd shape) and carries its wire bytes,
// unparsed, to exactly one browser WebSocket connection at a time. Every
// ACP v1 semantic — initialize, session/new, session/prompt, permission
// handling, trajectory rendering — is implemented independently in the
// browser's own TypeScript, served from this binary's embedded frontend
// assets. See docs/superpowers/specs/2026-08-31-web-trajectory-ui-design.md.
package main

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/SongYii/open-code-harness/internal/client/acpweb"
)

//go:embed web/dist
var embeddedAssets embed.FS

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "acp-web-bridge:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("acp-web-bridge", flag.ContinueOnError)
	var agentPath, cwd, resume, listen string
	flags.StringVar(&agentPath, "agent", "", "path to the agent binary to spawn (required)")
	flags.StringVar(&cwd, "cwd", "", "absolute path to the session workspace (required)")
	flags.StringVar(&resume, "resume", "", "resume an existing session id instead of creating a new one")
	flags.StringVar(&listen, "listen", "127.0.0.1:0", "127.0.0.1:port to listen on; the host is fixed and cannot be changed")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// Everything after a literal "--" is the agent's own argv, exactly as
	// cmd/acp-client already established.
	agentArgs := flags.Args()
	if agentPath == "" {
		return fmt.Errorf("acp-web-bridge: -agent is required")
	}
	if cwd == "" {
		return fmt.Errorf("acp-web-bridge: -cwd is required")
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("acp-web-bridge: invalid -listen: %w", err)
	}
	if host != "127.0.0.1" {
		return fmt.Errorf("acp-web-bridge: -listen host must be 127.0.0.1, got %q — this is hardcoded, not configurable", host)
	}

	assets, err := fs.Sub(embeddedAssets, "web/dist")
	if err != nil {
		return fmt.Errorf("acp-web-bridge: embedded assets: %w", err)
	}

	relay, _, err := acpweb.NewRelay(agentPath, agentArgs)
	if err != nil {
		return err
	}

	token, err := acpweb.GenerateToken()
	if err != nil {
		return err
	}

	server := acpweb.NewServer(relay, assets, acpweb.Config{Cwd: cwd, Resume: resume}, token)

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("acp-web-bridge: listen: %w", err)
	}
	server.SetSelfOrigin("http://" + listener.Addr().String())

	fmt.Fprintf(stderr, "acp-web-bridge: ready at http://%s/?token=%s\n", listener.Addr().String(), token)

	httpServer := &http.Server{Handler: server.Handler()}
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- httpServer.Serve(listener) }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	var serveErr error
	select {
	case serveErr = <-serveErrCh:
	case <-sigCh:
		_ = httpServer.Close()
		serveErr = <-serveErrCh
	}
	if serveErr != nil && serveErr != http.ErrServerClosed {
		fmt.Fprintln(stderr, "acp-web-bridge: http server:", serveErr)
	}

	// Fixed cleanup order matching cmd/acp-client: close the agent's stdin
	// first (so it sees EOF and can shut down on its own), then wait for
	// it to exit — both performed by Relay.Close.
	if closeErr := relay.Close(); closeErr != nil && serveErr == nil {
		return closeErr
	}
	return serveErr
}
