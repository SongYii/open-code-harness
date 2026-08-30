// Command acp-client is a minimal, standalone Agent Client Protocol (ACP)
// v1 client: it spawns an agent over stdio, drives one interactive
// session, renders a live trajectory to stdout, and answers permission
// requests from its own stdin. It is not specific to this repository's own
// agent - the -agent flag names any ACP v1 agent binary - though it is
// built and tested against this repository's own och -acp.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	acpclient "github.com/SongYii/open-code-harness/internal/client/acp"
	"golang.org/x/term"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "acp-client:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("acp-client", flag.ContinueOnError)
	var agentPath, cwd, resume string
	flags.StringVar(&agentPath, "agent", "", "path to the agent binary to spawn (required)")
	flags.StringVar(&cwd, "cwd", "", "absolute path to the session workspace (required)")
	flags.StringVar(&resume, "resume", "", "resume an existing session id instead of creating a new one")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// Everything after a literal "--" is the agent's own argv, verbatim
	// (flag.FlagSet stops parsing at "--" and returns what follows via
	// Args, which is exactly the passthrough this needs: without it, an
	// agent flag like "-acp" would be parsed as one of ours instead of
	// forwarded).
	agentArgs := flags.Args()
	if agentPath == "" {
		return fmt.Errorf("acp-client: -agent is required")
	}
	if cwd == "" {
		return fmt.Errorf("acp-client: -cwd is required")
	}

	cmd := exec.Command(agentPath, agentArgs...)
	cmd.Stderr = stderr
	agentStdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("acp-client: agent stdout pipe: %w", err)
	}
	agentStdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("acp-client: agent stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("acp-client: start agent: %w", err)
	}

	// One bufio.Reader shared by prompt-line reading (below) and
	// PermissionPrompter's own answer reading: two independent
	// bufio.Readers each wrapping stdin directly would each read ahead
	// into their own buffer and could steal bytes meant for the other.
	// Sharing one is safe because the two never read concurrently -
	// PermissionPrompter.Decide only runs while a prompt is in flight,
	// which is exactly when promptLoop is not itself blocked reading the
	// next line.
	sharedInput := bufio.NewReader(stdin)
	handler := &clientHandler{
		trajectory: acpclient.NewTrajectory(),
		prompter:   acpclient.NewPermissionPrompter(sharedInput, stdout),
		out:        stdout,
		tty:        isTerminal(stdout),
	}
	client, err := acpclient.NewClient(agentStdout, agentStdin, handler)
	if err != nil {
		return err
	}
	// One fixed cleanup order on every exit path, error or not: close the
	// agent's stdin first (so it sees EOF and can shut down on its own),
	// then the Connection, then reap the process.
	defer func() {
		_ = agentStdin.Close()
		_ = client.Close()
		_ = cmd.Wait()
	}()

	ctx := context.Background()
	if _, _, err := client.Initialize(ctx); err != nil {
		return fmt.Errorf("acp-client: %w", err)
	}

	var sessionID string
	if resume != "" {
		sessionID = resume
		if err := client.LoadSession(ctx, sessionID, cwd); err != nil {
			return fmt.Errorf("acp-client: %w", err)
		}
	} else {
		sessionID, err = client.NewSession(ctx, cwd)
		if err != nil {
			return fmt.Errorf("acp-client: %w", err)
		}
	}
	fmt.Fprintf(stdout, "session %s ready\n", sessionID)

	return promptLoop(client, sessionID, sharedInput, stdout)
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// errOperatorExit signals promptLoop to stop without error: the operator
// asked to exit via a second consecutive signal during an in-flight
// prompt's cancellation window.
var errOperatorExit = errors.New("acp-client: operator exit")

// promptLoop reads one line at a time as the next prompt and runs it to
// completion before reading the next, until in reaches EOF or a signal
// during an in-flight prompt asks to exit (runOnePrompt). It does not
// itself intercept a signal while idle (blocked reading the next line):
// an operator's Ctrl-C with nothing in flight gets Go's own default
// SIGINT handling, an immediate exit, which needs no code here at all.
func promptLoop(client *acpclient.Client, sessionID string, in *bufio.Reader, stdout io.Writer) error {
	for {
		fmt.Fprint(stdout, "> ")
		line, readErr := in.ReadString('\n')
		text := strings.TrimSpace(line)
		if text != "" {
			if err := runOnePrompt(client, sessionID, text, stdout); err != nil {
				if errors.Is(err, errOperatorExit) {
					return nil
				}
				return err
			}
		}
		if readErr != nil {
			return nil
		}
	}
}

type promptResult struct {
	stopReason string
	err        error
}

// runOnePrompt runs one prompt to completion. It intercepts
// SIGINT/SIGTERM only for the duration of this one call - a signal while
// this prompt is in flight cancels it via session/cancel; a second signal
// arriving before that cancellation settles returns errOperatorExit
// rather than waiting any further.
func runOnePrompt(client *acpclient.Client, sessionID, text string, stdout io.Writer) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	promptCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resultCh := make(chan promptResult, 1)
	go func() {
		stopReason, err := client.Prompt(promptCtx, sessionID, text)
		resultCh <- promptResult{stopReason, err}
	}()

	select {
	case res := <-resultCh:
		return reportPrompt(stdout, res)
	case <-sigCh:
		_ = client.Cancel(context.Background(), sessionID)
		select {
		case res := <-resultCh:
			return reportPrompt(stdout, res)
		case <-sigCh:
			return errOperatorExit
		}
	}
}

func reportPrompt(stdout io.Writer, res promptResult) error {
	if res.err != nil {
		return fmt.Errorf("acp-client: %w", res.err)
	}
	fmt.Fprintf(stdout, "\n[%s]\n", res.stopReason)
	return nil
}
