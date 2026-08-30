package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const twoOptionParams = `{
	"toolCall": {"toolCallId": "tc1", "title": "Write file", "kind": "edit", "status": "pending"},
	"options": [
		{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
		{"optionId": "reject-once", "name": "Reject once", "kind": "reject_once"}
	]
}`

const threeOptionParams = `{
	"toolCall": {"toolCallId": "tc1", "title": "Run command", "kind": "execute", "status": "pending"},
	"options": [
		{"optionId": "allow-always", "name": "Allow for this session", "kind": "allow_always"},
		{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
		{"optionId": "reject-once", "name": "Reject once", "kind": "reject_once"}
	]
}`

func TestDecideRealTwoOptionShapeAnswersYes(t *testing.T) {
	var out bytes.Buffer
	p := NewPermissionPrompter(strings.NewReader("y\n"), &out)
	got, err := p.Decide(context.Background(), json.RawMessage(twoOptionParams))
	if err != nil {
		t.Fatalf("Decide() err = %v", err)
	}
	if got != "allow-once" {
		t.Fatalf("Decide() = %q, want allow-once", got)
	}
	if !strings.Contains(out.String(), "Write file") || !strings.Contains(out.String(), "edit") {
		t.Fatalf("prompt output = %q, want the tool call's title and kind", out.String())
	}
}

func TestDecideRealTwoOptionShapeAnswersNo(t *testing.T) {
	p := NewPermissionPrompter(strings.NewReader("n\n"), &bytes.Buffer{})
	got, err := p.Decide(context.Background(), json.RawMessage(twoOptionParams))
	if err != nil {
		t.Fatalf("Decide() err = %v", err)
	}
	if got != "reject-once" {
		t.Fatalf("Decide() = %q, want reject-once", got)
	}
}

func TestDecideReplaysThePromptOnAnUnrecognizedYesNoAnswer(t *testing.T) {
	var out bytes.Buffer
	p := NewPermissionPrompter(strings.NewReader("maybe\ny\n"), &out)
	got, err := p.Decide(context.Background(), json.RawMessage(twoOptionParams))
	if err != nil {
		t.Fatalf("Decide() err = %v", err)
	}
	if got != "allow-once" {
		t.Fatalf("Decide() = %q, want allow-once after the bad first answer", got)
	}
	if !strings.Contains(out.String(), "please answer y or n") {
		t.Fatalf("prompt output = %q, want a re-prompt after the unrecognized answer", out.String())
	}
}

func TestDecideGenericShapeAnswersByNumber(t *testing.T) {
	var out bytes.Buffer
	p := NewPermissionPrompter(strings.NewReader("2\n"), &out)
	got, err := p.Decide(context.Background(), json.RawMessage(threeOptionParams))
	if err != nil {
		t.Fatalf("Decide() err = %v", err)
	}
	if got != "allow-once" {
		t.Fatalf("Decide() = %q, want allow-once (option 2)", got)
	}
	if !strings.Contains(out.String(), "1) Allow for this session") || !strings.Contains(out.String(), "2) Allow once") || !strings.Contains(out.String(), "3) Reject once") {
		t.Fatalf("prompt output = %q, want every option listed with its number and name", out.String())
	}
}

func TestDecideGenericShapeRePromptsOnOutOfRangeAndNonNumericInput(t *testing.T) {
	p := NewPermissionPrompter(strings.NewReader("0\n99\nabc\n3\n"), &bytes.Buffer{})
	got, err := p.Decide(context.Background(), json.RawMessage(threeOptionParams))
	if err != nil {
		t.Fatalf("Decide() err = %v", err)
	}
	if got != "reject-once" {
		t.Fatalf("Decide() = %q, want reject-once (option 3) after three bad answers", got)
	}
}

func TestDecideEOFWhilePendingResolvesToTheRejectOptionForTheRealShape(t *testing.T) {
	p := NewPermissionPrompter(strings.NewReader(""), &bytes.Buffer{})
	got, err := p.Decide(context.Background(), json.RawMessage(twoOptionParams))
	if err != nil {
		t.Fatalf("Decide() err = %v, want a resolved option, not an error", err)
	}
	if got != "reject-once" {
		t.Fatalf("Decide() = %q, want reject-once on EOF", got)
	}
}

func TestDecideEOFWhilePendingResolvesToTheRejectOptionForTheGenericShape(t *testing.T) {
	p := NewPermissionPrompter(strings.NewReader("not a number"), &bytes.Buffer{}) // no trailing newline: EOF arrives mid-answer
	got, err := p.Decide(context.Background(), json.RawMessage(threeOptionParams))
	if err != nil {
		t.Fatalf("Decide() err = %v, want a resolved option, not an error", err)
	}
	if got != "reject-once" {
		t.Fatalf("Decide() = %q, want reject-once (the offered reject_once option) on EOF", got)
	}
}

func TestDecideRejectsMalformedParams(t *testing.T) {
	p := NewPermissionPrompter(strings.NewReader("y\n"), &bytes.Buffer{})
	if _, err := p.Decide(context.Background(), json.RawMessage(`not json`)); err == nil {
		t.Fatal("Decide() err = nil, want an error for malformed params")
	}
}

func TestDecideRejectsZeroOptions(t *testing.T) {
	p := NewPermissionPrompter(strings.NewReader("y\n"), &bytes.Buffer{})
	params := `{"toolCall":{"title":"x","kind":"y"},"options":[]}`
	if _, err := p.Decide(context.Background(), json.RawMessage(params)); err == nil {
		t.Fatal("Decide() err = nil, want an error for zero offered options")
	}
}

func TestHandleRequestPermissionWrapsTheChoiceInTheAgentsResultShape(t *testing.T) {
	p := NewPermissionPrompter(strings.NewReader("y\n"), &bytes.Buffer{})
	raw, err := p.HandleRequestPermission(context.Background(), json.RawMessage(twoOptionParams))
	if err != nil {
		t.Fatalf("HandleRequestPermission() err = %v", err)
	}
	want := `{"outcome":{"outcome":"selected","optionId":"allow-once"}}`
	if string(raw) != want {
		t.Fatalf("HandleRequestPermission() = %s, want %s", raw, want)
	}
}
