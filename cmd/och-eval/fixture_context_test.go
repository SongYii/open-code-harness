package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func contextFixtureBody(t *testing.T, messages ...contextFixtureMessage) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"model": "fixture", "stream": true, "messages": messages})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return string(data)
}

func callContextFixture(t *testing.T, body string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	contextMechanismFixtureScript(recorder, request)
	return recorder, recorder.Body.String()
}

func userMessage(text string) contextFixtureMessage {
	return contextFixtureMessage{Role: "user", Content: text}
}

// summaryPrompt renders what renderSummaryPrompt actually sends: the
// versioned asset marker first, optionally a PREVIOUS CHECKPOINT section,
// then source material.
func summaryPrompt(previousSummary string) contextFixtureMessage {
	var builder strings.Builder
	builder.WriteString("<!-- " + summaryPromptMarker + " -- design §11.1 -->\nSummarize the conversation below.\n")
	if previousSummary != "" {
		builder.WriteString("\n\n## PREVIOUS CHECKPOINT\n\n" + previousSummary)
	}
	builder.WriteString("\n\n## SOURCE MATERIAL\n\nturn 1: hello\n")
	return userMessage(builder.String())
}

func TestContextFixtureClassifiesRequests(t *testing.T) {
	projectedResult := projectedToolResultOpenMarker +
		"\nevent_id: event-1\noriginal_bytes: 40000\nsha256: " + strings.Repeat("a", 64) +
		"\ncontent_head:\nhead\ncontent_tail:\ntail\n" + projectedToolResultCloseMarker

	cases := []struct {
		name       string
		messages   []contextFixtureMessage
		wantStatus int
		wantBody   string
		absentBody string
	}{
		{
			name:       "plain prompt gets a bounded completion",
			messages:   []contextFixtureMessage{userMessage("hello")},
			wantStatus: http.StatusOK,
			wantBody:   `"ok"`,
		},
		{
			name:       "summarizer request is classified by its version marker",
			messages:   []contextFixtureMessage{summaryPrompt("")},
			wantStatus: http.StatusOK,
			wantBody:   "fixture_chunk_depth: 1",
		},
		{
			name:       "summary carries every required heading",
			messages:   []contextFixtureMessage{summaryPrompt("")},
			wantStatus: http.StatusOK,
			wantBody:   "## Continuation",
		},
		{
			name:       "rolling summary advances the fixture depth",
			messages:   []contextFixtureMessage{summaryPrompt("## Established Facts\nfixture_chunk_depth: 2\n")},
			wantStatus: http.StatusOK,
			wantBody:   "fixture_chunk_depth: 3",
		},
		{
			name:       "malformed prior depth is a bounded contract failure",
			messages:   []contextFixtureMessage{summaryPrompt("## Established Facts\nfixture_chunk_depth: not-a-number\n")},
			wantStatus: http.StatusOK,
			wantBody:   contextFixtureContractFailure,
		},
		{
			name:       "overflow marker is rejected before any checkpoint exists",
			messages:   []contextFixtureMessage{userMessage("please continue " + contextOverflowMarker)},
			wantStatus: http.StatusBadRequest,
			wantBody:   "context_length_exceeded",
		},
		{
			name: "overflow marker succeeds once a checkpoint is rendered",
			messages: []contextFixtureMessage{
				userMessage("## Objective\nprior\n## Continuation\ncarry on"),
				userMessage("please continue " + contextOverflowMarker),
			},
			wantStatus: http.StatusOK,
			wantBody:   contextPostCompactionSentinel,
		},
		{
			name:       "prune marker issues the large read_file call",
			messages:   []contextFixtureMessage{userMessage("read it " + contextPruneMarker)},
			wantStatus: http.StatusOK,
			wantBody:   contextPruneFixturePath,
		},
		{
			name: "projected tool result is accepted",
			messages: []contextFixtureMessage{
				userMessage("read it " + contextPruneMarker),
				{Role: "tool", ToolCallID: contextPruneToolCallID, Content: projectedResult},
			},
			wantStatus: http.StatusOK,
			wantBody:   contextPruneSuccessSentinel,
		},
		{
			name: "an unprojected tool result is refused",
			messages: []contextFixtureMessage{
				userMessage("read it " + contextPruneMarker),
				{Role: "tool", ToolCallID: contextPruneToolCallID, Content: strings.Repeat("x", 4000)},
			},
			wantStatus: http.StatusOK,
			wantBody:   contextFixtureContractFailure,
			absentBody: contextPruneSuccessSentinel,
		},
		{
			name: "a projected result that lost its call id is refused",
			messages: []contextFixtureMessage{
				userMessage("read it " + contextPruneMarker),
				{Role: "tool", ToolCallID: "call_something_else", Content: projectedResult},
			},
			wantStatus: http.StatusOK,
			wantBody:   contextFixtureContractFailure,
			absentBody: contextPruneSuccessSentinel,
		},
		{
			name:       "usage anchor marker reports a high fixed provider input",
			messages:   []contextFixtureMessage{userMessage("anchor this " + contextUsageAnchorMarker)},
			wantStatus: http.StatusOK,
			wantBody:   `"prompt_tokens":60000`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder, body := callContextFixture(t, contextFixtureBody(t, testCase.messages...))
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, testCase.wantStatus, body)
			}
			if !strings.Contains(body, testCase.wantBody) {
				t.Fatalf("body does not contain %q: %s", testCase.wantBody, body)
			}
			if testCase.absentBody != "" && strings.Contains(body, testCase.absentBody) {
				t.Fatalf("body unexpectedly contains %q: %s", testCase.absentBody, body)
			}
		})
	}
}

// TestContextFixtureIgnoresMarkersInHistory is the reason this fixture parses
// the request instead of searching the whole body. A multi-turn Scenario
// still carries every earlier message, so a marker that already selected a
// branch once must not keep selecting it for every later turn.
func TestContextFixtureIgnoresMarkersInHistory(t *testing.T) {
	_, body := callContextFixture(t, contextFixtureBody(t,
		userMessage("earlier turn "+contextOverflowMarker),
		contextFixtureMessage{Role: "assistant", Content: "ok"},
		userMessage("a later, ordinary prompt"),
	))
	if strings.Contains(body, "context_length_exceeded") {
		t.Fatalf("a historical marker selected the latest request's branch: %s", body)
	}
	if !strings.Contains(body, `"ok"`) {
		t.Fatalf("latest ordinary prompt did not get a plain completion: %s", body)
	}
}

// TestContextFixtureSummaryTakesPrecedenceOverHistoryMarkers proves the
// classification order: a summarizer request must be answered as one even
// when the conversation it is summarizing contains Scenario markers.
func TestContextFixtureSummaryTakesPrecedenceOverHistoryMarkers(t *testing.T) {
	_, body := callContextFixture(t, contextFixtureBody(t,
		userMessage("earlier turn "+contextPruneMarker),
		summaryPrompt(""),
	))
	if !strings.Contains(body, "## Objective") {
		t.Fatalf("summarizer request was not answered with a summary: %s", body)
	}
	if strings.Contains(body, contextPruneFixturePath) {
		t.Fatalf("a historical marker leaked into a summarizer response: %s", body)
	}
}

func TestContextFixtureRejectsMalformedRequests(t *testing.T) {
	for _, body := range []string{"", "not json at all", `{"messages":`} {
		recorder, response := callContextFixture(t, body)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want a bounded contract failure", recorder.Code)
		}
		if !strings.Contains(response, contextFixtureContractFailure) {
			t.Fatalf("malformed body %q did not produce the contract failure sentinel: %s", body, response)
		}
	}
}

// TestContextFixtureNeverEchoesTheRequest pins section 7.5: the handler must
// not return the raw body or an Authorization header in any response,
// including its failure paths.
func TestContextFixtureNeverEchoesTheRequest(t *testing.T) {
	secret := "sk-fixture-should-never-be-echoed"
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(contextFixtureBody(t, userMessage("hello "+secret))))
	request.Header.Set("Authorization", "Bearer "+secret)
	contextMechanismFixtureScript(recorder, request)
	if strings.Contains(recorder.Body.String(), secret) {
		t.Fatalf("the fixture echoed request content: %s", recorder.Body.String())
	}
}

// TestContextFixtureIsConcurrencySafe pins the no-shared-state rule: the
// handler holds no cross-request counters, so parallel Attempts cannot
// influence each other's classification.
func TestContextFixtureIsConcurrencySafe(t *testing.T) {
	bodies := []string{
		contextFixtureBody(t, userMessage("plain")),
		contextFixtureBody(t, summaryPrompt("")),
		contextFixtureBody(t, userMessage("overflow "+contextOverflowMarker)),
		contextFixtureBody(t, userMessage("anchor "+contextUsageAnchorMarker)),
	}
	want := make([]string, len(bodies))
	for index, body := range bodies {
		_, response := callContextFixture(t, body)
		want[index] = response
	}

	var group sync.WaitGroup
	for round := 0; round < 40; round++ {
		for index, body := range bodies {
			group.Add(1)
			go func(index int, body string) {
				defer group.Done()
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
				contextMechanismFixtureScript(recorder, request)
				if recorder.Body.String() != want[index] {
					t.Errorf("concurrent response for case %d differed from the sequential one", index)
				}
			}(index, body)
		}
	}
	group.Wait()
}
