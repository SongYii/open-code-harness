package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Context mechanism Scenario markers. These are checked-in, non-secret
// fixture protocol coordinates that select which real behavior below runs.
// A marker is never verifier success by itself: every Context criterion is
// satisfied from canonical audit evidence, and the fixture only ever acts as
// an external observer of the HTTP envelope OCH actually produced.
const (
	contextOverflowMarker    = "OCH_EVAL_CONTEXT_OVERFLOW"
	contextPruneMarker       = "OCH_EVAL_CONTEXT_PRUNE"
	contextUsageAnchorMarker = "OCH_EVAL_CONTEXT_USAGE_ANCHOR"
)

// Sentinels the verifiers match. Each is bounded and stable so verifier
// detail text can name it without ever dumping model output.
const (
	contextPostCompactionSentinel = "OCH_EVAL_CONTEXT_POST_COMPACTION_OK"
	contextPruneSuccessSentinel   = "OCH_EVAL_CONTEXT_PRUNE_PROJECTION_OK"
	contextFixtureContractFailure = "OCH_EVAL_CONTEXT_FIXTURE_CONTRACT_FAILED"
)

// contextPruneToolCallID and contextPruneFixturePath name the single tool
// call the pruning Scenario issues. The verifier checks the projected frame
// still carries this exact call ID.
const (
	contextPruneToolCallID  = "call_context_prune"
	contextPruneFixturePath = "large-tool-result.txt"
)

// summaryPromptMarker is the versioned prompt asset's own first-line marker.
// Classification keys on it rather than on an incidental sentence, so a
// reworded prompt body cannot silently reclassify a summarizer request as a
// conversation one.
const summaryPromptMarker = "och_context_summary_v1"

// projectedToolResultOpenMarker and projectedToolResultCloseMarker mirror
// contextengine's own Tool Result projection framing. They are duplicated
// here deliberately: this fixture is an out-of-process observer of the wire
// envelope, and importing the Context Engine into the fixture would let a
// change to the framing pass unnoticed on both sides at once.
const (
	projectedToolResultOpenMarker  = "[tool result projected by Open Code Harness]"
	projectedToolResultCloseMarker = "[end projected tool result]"
)

// maxFixtureRequestBytes bounds what this handler will read from one
// request. A Context Scenario deliberately builds large histories; the bound
// keeps a runaway body from becoming this process's problem.
const maxFixtureRequestBytes = 8 << 20

// contextFixtureRequest is the subset of the OpenAI-compatible request this
// handler classifies on. It is decoded per request and never retained: the
// handler holds no cross-request state at all, so Attempts may run in
// parallel and a Scenario's behavior is a pure function of the envelope OCH
// sent.
type contextFixtureRequest struct {
	Messages []contextFixtureMessage `json:"messages"`
}

type contextFixtureMessage struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id"`
}

// contextMechanismFixtureScript answers every Context mechanism Scenario.
//
// Classification order matters and is fixed: summarizer, then tool
// continuation, then the latest real user message's marker, then a plain
// completion. Selecting on the latest user message rather than the whole
// body is the point — a multi-turn Scenario still carries every earlier
// message, so a body-wide search would let an earlier turn's marker keep
// selecting a later turn's branch.
func contextMechanismFixtureScript(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxFixtureRequestBytes))
	_ = r.Body.Close()
	if err != nil {
		writeFixtureContractFailure(w, "request body could not be read")
		return
	}
	var request contextFixtureRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writeFixtureContractFailure(w, "request body is not valid JSON")
		return
	}

	switch {
	case isSummarizerRequest(request):
		writeContextSummaryResponse(w, request)
	case latestToolResult(request) != nil:
		writeContextToolContinuation(w, request)
	default:
		writeContextMarkerResponse(w, request)
	}
}

// isSummarizerRequest identifies the Context Engine's own summarization call
// by the versioned prompt marker at the start of its sole current input.
func isSummarizerRequest(request contextFixtureRequest) bool {
	messages := request.Messages
	if len(messages) == 0 {
		return false
	}
	latest := messages[len(messages)-1]
	if latest.Role != "user" {
		return false
	}
	// The marker lives in the prompt asset's own first line; requiring it
	// near the start rather than anywhere in the text keeps a conversation
	// that merely quotes the marker from being mistaken for a summarizer
	// call.
	head := latest.Content
	if len(head) > 512 {
		head = head[:512]
	}
	return strings.Contains(head, summaryPromptMarker)
}

// latestToolResult returns the most recent tool message, or nil.
func latestToolResult(request contextFixtureRequest) *contextFixtureMessage {
	for index := len(request.Messages) - 1; index >= 0; index-- {
		if request.Messages[index].Role == "tool" {
			return &request.Messages[index]
		}
	}
	return nil
}

// latestUserMarker returns the Scenario marker carried by the latest real
// user message, or "" when that message carries none. Only the latest user
// message is consulted, never history.
func latestUserMarker(request contextFixtureRequest) string {
	for index := len(request.Messages) - 1; index >= 0; index-- {
		message := request.Messages[index]
		if message.Role != "user" {
			continue
		}
		for _, marker := range []string{contextOverflowMarker, contextPruneMarker, contextUsageAnchorMarker} {
			if strings.Contains(message.Content, marker) {
				return marker
			}
		}
		return ""
	}
	return ""
}

// requestCarriesCheckpoint reports whether the envelope already contains a
// rendered checkpoint message. The overflow branch keys on this rather than
// on a request counter: the retry genuinely differs by its own prepared
// envelope, which is the fact the Scenario is about.
func requestCarriesCheckpoint(request contextFixtureRequest) bool {
	for _, message := range request.Messages {
		if strings.Contains(message.Content, "## Objective") &&
			strings.Contains(message.Content, "## Continuation") {
			return true
		}
		if strings.Contains(message.Content, "prior conversation was reset") {
			return true
		}
	}
	return false
}

func writeContextMarkerResponse(w http.ResponseWriter, request contextFixtureRequest) {
	switch latestUserMarker(request) {
	case contextOverflowMarker:
		if requestCarriesCheckpoint(request) {
			writeContextText(w, contextPostCompactionSentinel, nil)
			return
		}
		writeContextOverflowRejection(w)
	case contextPruneMarker:
		writeToolCallSSE(w, contextPruneToolCallID, "read_file",
			fmt.Sprintf(`{"path":%s}`, jsonQuote(contextPruneFixturePath)))
	case contextUsageAnchorMarker:
		// A deliberately high fixed provider input count, reported as real
		// usage on an otherwise short answer. The Subject profile makes the
		// plain next-request wire estimate stay below trigger while this
		// non-lowering anchor crosses it.
		writeContextText(w, "anchored", &contextFixtureUsage{InputTokens: 60000, OutputTokens: 2})
	default:
		writeContextText(w, "ok", nil)
	}
}

// writeContextToolContinuation answers the request that carries the pruning
// Scenario's Tool Result. It succeeds only when the received result is the
// complete projected frame and still names the original Tool Call ID —
// making the fixture an independent witness that projection really reached
// the wire, alongside the audit evidence that explains how.
func writeContextToolContinuation(w http.ResponseWriter, request contextFixtureRequest) {
	result := latestToolResult(request)
	if result == nil {
		writeFixtureContractFailure(w, "tool continuation without a tool result")
		return
	}
	if latestUserMarker(request) != contextPruneMarker {
		writeContextText(w, "acknowledged", nil)
		return
	}
	if result.ToolCallID != contextPruneToolCallID {
		writeFixtureContractFailure(w, "projected tool result lost its original tool call id")
		return
	}
	if !strings.Contains(result.Content, projectedToolResultOpenMarker) ||
		!strings.Contains(result.Content, projectedToolResultCloseMarker) ||
		!strings.Contains(result.Content, "original_bytes:") ||
		!strings.Contains(result.Content, "sha256:") {
		writeFixtureContractFailure(w, "tool result was not the projected frame")
		return
	}
	writeContextText(w, contextPruneSuccessSentinel, nil)
}

// writeContextSummaryResponse returns a structurally valid
// och_context_summary_v1 document whose Established Facts section carries
// fixture_chunk_depth. Depth makes rolling behavior observable without any
// server state: a final checkpoint with SummaryChunks == N >= 2 proves each
// later summarizer request received the previous chunk's own output.
func writeContextSummaryResponse(w http.ResponseWriter, request contextFixtureRequest) {
	depth, err := nextFixtureChunkDepth(request)
	if err != nil {
		writeFixtureContractFailure(w, err.Error())
		return
	}
	summary := strings.Join([]string{
		"## Objective", "Exercise the Context Engine's own summarization path.",
		"## User Constraints", "None.",
		"## Established Facts", fmt.Sprintf("fixture_chunk_depth: %d", depth),
		"## Work Completed", "Prior turns ran successfully.",
		"## Files and Commands", "None.",
		"## Open Work", "None.",
		"## Risks and Unknowns", "None.",
		"## Continuation", "Proceed with the next turn.",
	}, "\n")
	writeContextText(w, summary, nil)
}

// nextFixtureChunkDepth reads the prior depth out of the rendered PREVIOUS
// CHECKPOINT section, when one is present, and returns depth+1. A request
// with no previous checkpoint is depth 1. A malformed or non-numeric prior
// depth is a fixture-contract failure rather than a silent restart at 1,
// because silently restarting would make a broken rolling chain look like a
// healthy first chunk.
func nextFixtureChunkDepth(request contextFixtureRequest) (int, error) {
	if len(request.Messages) == 0 {
		return 1, nil
	}
	prompt := request.Messages[len(request.Messages)-1].Content
	marker := "fixture_chunk_depth:"
	index := strings.Index(prompt, marker)
	if index < 0 {
		return 1, nil
	}
	rest := prompt[index+len(marker):]
	if end := strings.IndexAny(rest, "\r\n"); end >= 0 {
		rest = rest[:end]
	}
	previous, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil || previous < 1 {
		return 0, fmt.Errorf("previous fixture_chunk_depth is malformed")
	}
	return previous + 1, nil
}

// contextFixtureUsage is fixture data, never an inferred cost.
type contextFixtureUsage struct {
	InputTokens  uint64
	OutputTokens uint64
}

func writeContextText(w http.ResponseWriter, text string, usage *contextFixtureUsage) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	writeSSELine(w, fmt.Sprintf(`{"choices":[{"delta":{"content":%s},"finish_reason":null}]}`, jsonQuote(text)))
	if usage != nil {
		writeSSELine(w, fmt.Sprintf(
			`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
			usage.InputTokens, usage.OutputTokens, usage.InputTokens+usage.OutputTokens))
	} else {
		writeSSELine(w, `{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	}
	writeSSELine(w, "[DONE]")
	if flusher != nil {
		flusher.Flush()
	}
}

// writeContextOverflowRejection returns the OpenAI-compatible
// context_length_exceeded shape. It is a real provider rejection, which is
// what makes the recovery path under test the production one.
func writeContextOverflowRejection(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = io.WriteString(w, `{"error":{"message":"This model's maximum context length is exceeded by this request.","type":"invalid_request_error","code":"context_length_exceeded"}}`)
}

// writeFixtureContractFailure returns a bounded, stable sentinel a verifier
// rejects. It never echoes the request: the handler must not log or return
// Authorization headers or raw bodies.
func writeFixtureContractFailure(w http.ResponseWriter, reason string) {
	writeContextText(w, contextFixtureContractFailure+": "+reason, nil)
}
