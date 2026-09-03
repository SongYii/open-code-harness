package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

// fixtureScheme marks a Subject.Provider.NormalizedEndpoint this command
// resolves to one of its own embedded, deterministic loopback scripts
// (the plan's own "real loopback model scripts") rather than a real
// network address: fixture://<script-name>. It is never written by
// anything outside this command's own checked-in fixture Subject
// documents, and it is rewritten to a real http://127.0.0.1:<port>/v1
// address in memory before any Attempt runs — the checked-in document
// itself never changes.
const fixtureScheme = "fixture"

// fixtureScripts is this command's own compiled catalog of loopback
// scripts, keyed by name (the fixture:// URL's host). Each is real,
// runnable Go code answering real HTTP requests on a real loopback
// socket -- not static response data -- so a checked-in fixture Subject
// document only ever names one, never carries response content itself.
var fixtureScripts = map[string]http.HandlerFunc{
	"smart": smartFixtureScript,
	// context-mechanism is deliberately a separate script rather than more
	// branches on smart: it classifies by parsing the request and consulting
	// only the latest user message, which is the opposite of smart's own
	// whole-body substring convention. Mixing the two would make both
	// harder to reason about.
	"context-mechanism": contextMechanismFixtureScript,
}

// toolApprovalTriggerMarker is the exact substring the checked-in
// tool-approval-failure Scenario's own prompt text carries (design's own
// "real loopback model script", not response data: the marker only
// selects which real behavior below runs). Any other request -- lacking
// the marker, or already carrying a tool result message -- falls through
// to a plain assistant answer.
const toolApprovalTriggerMarker = "TRIGGER_WRITE_FILE_APPROVAL"

// Task 16's own tool/workspace suite markers. Each names exactly one
// checked-in Scenario's own prompt text and selects exactly one further
// real behavior below -- never chained with another marker in the same
// conversation: a later request in a multi-turn Scenario still carries
// every earlier message (and so every earlier marker) in its own body,
// so any Scenario using more than one of these would need ordering logic
// this dispatcher does not have. Every Scenario that uses one of these
// markers uses exactly one, matching toolApprovalTriggerMarker's own
// established convention.
const (
	readFileTriggerMarker    = "TRIGGER_READ_FILE"
	execRedactionMarker      = "TRIGGER_EXEC_REDACTION"
	readMissingTriggerMarker = "TRIGGER_READ_MISSING"
	readOutsideTriggerMarker = "TRIGGER_READ_OUTSIDE"
)

// smartFixtureScript answers every checked-in smoke Scenario's request
// from one real, deterministic loopback server: a plain-text answer by
// default (smoke-prompt, and the plain prompt action every context-
// compaction Scenario also issues before its own compact action, which
// never reaches the network at all), one specific tool call the first
// time a request's latest user message carries that tool's own trigger
// marker, and a plain acknowledgement once the request's own message
// history already carries that tool's result (the follow-up call after
// the tool resolved, whichever way).
func smartFixtureScript(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	switch {
	case requestCarriesToolResult(body):
		writeSSELine(w, `{"choices":[{"delta":{"content":"acknowledged"},"finish_reason":null}]}`)
		writeSSELine(w, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
		writeSSELine(w, "[DONE]")
	case bytes.Contains(body, []byte(toolApprovalTriggerMarker)):
		writeToolCallSSE(w, "call_write", "write_file", `{"path":"denied.txt","content":"should not be written"}`)
	case bytes.Contains(body, []byte(readFileTriggerMarker)):
		writeToolCallSSE(w, "call_read", "read_file", `{"path":"input.txt"}`)
	case bytes.Contains(body, []byte(execRedactionMarker)):
		writeToolCallSSE(w, "call_exec", "exec", `{"argv":["echo","API_KEY=sk-test-should-not-appear-in-evidence"]}`)
	case bytes.Contains(body, []byte(readMissingTriggerMarker)):
		writeToolCallSSE(w, "call_missing", "read_file", `{"path":"does-not-exist.txt"}`)
	case bytes.Contains(body, []byte(readOutsideTriggerMarker)):
		writeToolCallSSE(w, "call_outside", "read_file", `{"path":"../outside.txt"}`)
	default:
		writeSSELine(w, `{"choices":[{"delta":{"content":"ok"},"finish_reason":null}]}`)
		writeSSELine(w, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
		writeSSELine(w, "[DONE]")
	}
	if flusher != nil {
		flusher.Flush()
	}
}

// writeToolCallSSE writes the two-frame tool_calls response shape every
// triggered branch above shares: one delta naming the tool call, one
// delta carrying finish_reason "tool_calls".
func writeToolCallSSE(w http.ResponseWriter, callID, toolName, argumentsJSON string) {
	writeSSELine(w, fmt.Sprintf(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":%s,"function":{"name":%s,"arguments":%s}}]},"finish_reason":null}]}`,
		jsonQuote(callID), jsonQuote(toolName), jsonQuote(argumentsJSON)))
	writeSSELine(w, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
	writeSSELine(w, "[DONE]")
}

func requestCarriesToolResult(body []byte) bool {
	var payload struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	for _, message := range payload.Messages {
		if message.Role == "tool" {
			return true
		}
	}
	return false
}

func writeSSELine(w http.ResponseWriter, payload string) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
}

func jsonQuote(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// resolveFixtureSubjects starts one embedded server per referenced fixture
// script and returns runtime-only endpoint overrides. It never mutates the
// frozen Subject documents whose exact bytes and digests identify the Cell.
// Fixture placeholder credentials are scoped to the servers' lifetime and
// cleanup restores the caller's prior environment exactly.
func resolveFixtureSubjects(subjects map[eval.SubjectID]eval.Subject) (overrides map[eval.SubjectID]string, cleanup func(), err error) {
	type savedEnvironment struct {
		value string
		set   bool
	}

	servers := make(map[string]*httptest.Server)
	savedCredentials := make(map[string]savedEnvironment)
	cleanup = func() {
		for _, server := range servers {
			server.Close()
		}
		for name, saved := range savedCredentials {
			if saved.set {
				_ = os.Setenv(name, saved.value)
			} else {
				_ = os.Unsetenv(name)
			}
		}
	}
	overrides = make(map[eval.SubjectID]string)

	for id, subject := range subjects {
		endpoint, parseErr := url.Parse(subject.Provider.NormalizedEndpoint)
		if subject.Provider.Lane != eval.ProviderLaneFixture {
			continue
		}
		if parseErr != nil || endpoint.Scheme != fixtureScheme || endpoint.Host == "" ||
			endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
			endpoint.Path != "" {
			cleanup()
			return nil, func() {}, fmt.Errorf("subject %q: fixture lane requires fixture://<script-name>", id)
		}
		scriptName := endpoint.Host
		server, ok := servers[scriptName]
		if !ok {
			handler, known := fixtureScripts[scriptName]
			if !known {
				cleanup()
				return nil, func() {}, fmt.Errorf("subject %q: unknown fixture script %q", id, scriptName)
			}
			server = httptest.NewServer(handler)
			servers[scriptName] = server
		}
		if _, saved := savedCredentials[subject.Provider.CredentialEnvVar]; !saved {
			if subject.Provider.CredentialEnvVar == "" {
				cleanup()
				return nil, func() {}, fmt.Errorf("subject %q: empty credential env var name", id)
			}
			value, set := os.LookupEnv(subject.Provider.CredentialEnvVar)
			savedCredentials[subject.Provider.CredentialEnvVar] = savedEnvironment{value: value, set: set}
			if envErr := os.Setenv(subject.Provider.CredentialEnvVar, "fixture-placeholder-key"); envErr != nil {
				cleanup()
				return nil, func() {}, fmt.Errorf("subject %q: set fixture credential: %w", id, envErr)
			}
		}
		overrides[id] = server.URL
	}
	return overrides, cleanup, nil
}

// jsonEncode is a small shared helper so every subcommand emits its one
// report document the same way: compact, deterministic key order (Go's
// own struct-field-declaration order), newline-terminated.
func jsonEncode(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
