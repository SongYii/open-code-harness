// Command acpchild is a minimal, controllable ACP v1 agent used only by
// internal/harness/eval's own acp_executor tests to exercise lifecycle
// fault modes a real och -acp binary cannot easily be made to produce on
// demand (a malformed frame, a hang past startup, a non-zero exit right
// after handshake, an oversized stderr stream, a stuck prompt that
// resolves only at a specific escalation rung). Conformance behavior
// itself is still proven against the real och binary elsewhere
// (acp_executor_test.go's own TestRunACPAttempt... suite) — this program
// exists purely to make specific failure shapes deterministic and fast to
// trigger.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func main() {
	mode := flag.String("mode", "normal", "acpchild behavior: normal, hang, malformed-frame, exit-nonzero, huge-stderr, dump-env, cancel-aware, prompt-hang, prompt-hang-survive-close, ignore-signals")
	flag.Parse()

	switch *mode {
	case "dump-env":
		// Not ACP-conformant at all by design: this mode exists solely so
		// a test can inspect exactly what environment this process
		// actually received (BuildChildEnvironment's own allowlist),
		// which the real ACP wire protocol has no method for.
		for _, entry := range os.Environ() {
			fmt.Println(entry)
		}
		os.Exit(0)
	case "hang":
		// A bare select{} would trip Go's own runtime deadlock detector
		// (no goroutine could ever become runnable again) and exit
		// almost immediately instead of actually hanging -- verified
		// directly, not assumed. Sleeping is genuinely still running,
		// never reading stdin or responding to anything, and never
		// exiting on its own.
		time.Sleep(time.Hour)
	case "malformed-frame":
		fmt.Println("this is not a JSON-RPC frame at all")
		os.Exit(0)
	case "huge-stderr":
		writeHugeStderrThenServeNormally()
	case "exit-nonzero":
		serveUntil(func(m message) bool {
			if m.Method == "initialize" {
				respond(m.ID, `{"protocolVersion":1,"agentCapabilities":{"loadSession":false},"agentInfo":{"name":"acpchild","version":"test"}}`)
				return true // stop serving; exit(1) right after the handshake
			}
			return false
		})
		os.Exit(1)
	case "cancel-aware":
		// Responds to a pending session/prompt immediately once
		// session/cancel arrives, proving escalateCancel's mildest
		// session_cancel rung can resolve a prompt without ever
		// signaling the process. It still exits cleanly on stdin EOF
		// (like every other well-behaved mode) so a test's own
		// stdin-close cleanup can actually reap it afterward.
		serveEscalationRung(escalationBehavior{respondToCancel: true})
	case "prompt-hang":
		// Never answers session/prompt, but exits promptly once stdin
		// reaches EOF (no signal involved at all) — proves escalateCancel's
		// stdin_close rung resolves the pending prompt (with a connection-
		// closed error, since the process exited without ever replying).
		serveEscalationRung(escalationBehavior{blockAfterEOF: false})
	case "prompt-hang-survive-close":
		// Never answers session/prompt and keeps running past stdin EOF,
		// but does not ignore any signal, so the default SIGTERM
		// disposition terminates it — proves escalateCancel's sigterm rung
		// is what actually resolves the pending prompt.
		serveEscalationRung(escalationBehavior{blockAfterEOF: true})
	case "ignore-signals":
		// Never answers session/prompt, keeps running past stdin EOF, and
		// ignores SIGTERM/SIGINT — only SIGKILL (which cannot be ignored)
		// can end it, proving escalateCancel's own final rung.
		serveEscalationRung(escalationBehavior{blockAfterEOF: true, ignoreTermAndInt: true})
	default:
		serveUntil(func(message) bool { return false }) // run until stdin EOF
	}
}

// writeHugeStderrThenServeNormally writes well past any reasonable bound
// (16 MiB) to stderr, interleaved with normal ACP request handling, so a
// test can prove the supervisor's own bounded stderr capture truncates
// rather than growing without limit or blocking this child.
func writeHugeStderrThenServeNormally() {
	line := bytes.Repeat([]byte("x"), 4096)
	line = append(line, '\n')
	go func() {
		for i := 0; i < 5000; i++ { // 5000 * 4097 bytes ~= 19.5 MiB
			if _, err := os.Stderr.Write(line); err != nil {
				return
			}
		}
	}()
	serveUntil(func(message) bool { return false })
}

// serveUntil reads NDJSON frames from stdin until EOF or stop returns
// true for a received message, answering initialize/session.new/
// session.prompt with fixed, valid results and exiting 0 once stdin
// closes — the "normal" shutdown path RunACPAttempt's own
// closeAndReapACP depends on.
func serveUntil(stop func(message) bool) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if stop(m) {
			return
		}
		switch m.Method {
		case "initialize":
			respond(m.ID, `{"protocolVersion":1,"agentCapabilities":{"loadSession":true},"agentInfo":{"name":"acpchild","version":"test"}}`)
		case "session/new":
			respond(m.ID, `{"sessionId":"acpchild-session"}`)
		case "session/load":
			respond(m.ID, `{}`)
		case "session/prompt":
			respond(m.ID, `{"stopReason":"end_turn"}`)
		}
	}
}

// escalationBehavior parameterizes exactly the three axes
// escalateCancel's own four rungs need a controllable child to vary:
// whether session/cancel gets an immediate reply for the pending prompt,
// whether the process keeps running (rather than exiting) once stdin
// reaches EOF, and whether it ignores the signals SIGTERM/SIGINT would
// otherwise deliver by default.
type escalationBehavior struct {
	respondToCancel  bool
	blockAfterEOF    bool
	ignoreTermAndInt bool
}

// serveEscalationRung implements every acpchild mode used by Task 13's
// escalateCancel tests from one shared shape: initialize and session/new
// always answer normally; session/prompt is always left unanswered here
// (a resolved prompt is exactly what a real Turn's own end_turn response
// looks like, which is not the shape these tests need); session/cancel
// answers the currently pending prompt only when respondToCancel is set.
func serveEscalationRung(behavior escalationBehavior) {
	if behavior.ignoreTermAndInt {
		signal.Ignore(syscall.SIGTERM, syscall.SIGINT)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var pendingPromptID json.RawMessage
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		switch m.Method {
		case "initialize":
			respond(m.ID, `{"protocolVersion":1,"agentCapabilities":{"loadSession":false},"agentInfo":{"name":"acpchild","version":"test"}}`)
		case "session/new":
			respond(m.ID, `{"sessionId":"acpchild-session"}`)
		case "session/prompt":
			if behavior.respondToCancel {
				// Remembered so the matching session/cancel notification
				// below can reply to THIS request's own id -- a
				// notification carries no id of its own.
				pendingPromptID = m.ID
				continue
			}
			// deliberately never respond
		case "session/cancel":
			if behavior.respondToCancel && len(pendingPromptID) > 0 {
				respond(pendingPromptID, `{"stopReason":"cancelled"}`)
				pendingPromptID = nil
			}
		}
	}
	if behavior.blockAfterEOF {
		// select{} would trip Go's own deadlock detector (verified
		// directly, not assumed, in the "hang" mode above); sleeping is
		// genuinely still running and reachable only by a real signal.
		time.Sleep(time.Hour)
	}
}

func respond(id json.RawMessage, resultJSON string) {
	payload, err := json.Marshal(message{JSONRPC: "2.0", ID: id, Result: json.RawMessage(resultJSON)})
	if err != nil {
		return
	}
	_, _ = os.Stdout.Write(append(payload, '\n'))
}
