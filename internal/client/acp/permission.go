package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// permissionRequestParams is this project's own agent's real
// session/request_permission wire shape
// (internal/harness/adapters/acp/protocol.go's permissionParams, read
// directly), not the full ACP specification's shape in the abstract.
type permissionRequestParams struct {
	ToolCall permissionToolCall `json:"toolCall"`
	Options  []permissionOption `json:"options"`
}

type permissionToolCall struct {
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type permissionResult struct {
	Outcome permissionOutcome `json:"outcome"`
}

type permissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId"`
}

// PermissionPrompter asks an operator to answer session/request_permission
// calls over an injected io.Reader/io.Writer pair — never os.Stdin/Stdout
// directly — so it is fully unit-testable without a real terminal.
type PermissionPrompter struct {
	in  *bufio.Reader
	out io.Writer
}

// NewPermissionPrompter constructs a PermissionPrompter reading answers
// from in and writing prompt text to out.
func NewPermissionPrompter(in io.Reader, out io.Writer) *PermissionPrompter {
	return &PermissionPrompter{in: bufio.NewReader(in), out: out}
}

// HandleRequestPermission implements the second half of Handler: it
// decides an option (Decide) and wraps the choice into the
// {"outcome":{"outcome":"selected","optionId":...}} result shape this
// project's own agent expects back.
func (p *PermissionPrompter) HandleRequestPermission(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	optionID, err := p.Decide(ctx, params)
	if err != nil {
		return nil, err
	}
	return json.Marshal(permissionResult{Outcome: permissionOutcome{Outcome: "selected", OptionID: optionID}})
}

// Decide asks the operator to choose one of the offered options and
// returns its optionId. It returns a non-nil error only when params
// itself is unusable (malformed JSON, or zero offered options) — there is
// no sensible fallback for either. Any other failure, including the
// operator's input stream reaching EOF mid-answer, resolves to a
// deterministic fail-closed default (failClosed) rather than an error:
// the underlying ACP call must always get answered, never left hanging.
func (p *PermissionPrompter) Decide(ctx context.Context, params json.RawMessage) (string, error) {
	var req permissionRequestParams
	if err := json.Unmarshal(params, &req); err != nil {
		return "", fmt.Errorf("acp: malformed session/request_permission params: %w", err)
	}
	if len(req.Options) == 0 {
		return "", fmt.Errorf("acp: session/request_permission offered no options")
	}
	if allowID, rejectID, ok := knownTwoOptionShape(req.Options); ok {
		return p.decideYesNo(req.ToolCall, allowID, rejectID), nil
	}
	return p.decideGeneric(req.ToolCall, req.Options), nil
}

// knownTwoOptionShape reports whether options is exactly this project's
// own agent's real, verified shape: two options, "allow-once" and
// "reject-once", in either order. Any other shape (a different count,
// different optionIds, or an agent this client was not specifically
// tuned against) falls through to the generic numbered-choice path rather
// than being forced into a yes/no answer it may not fit.
func knownTwoOptionShape(options []permissionOption) (allowID, rejectID string, ok bool) {
	if len(options) != 2 {
		return "", "", false
	}
	var allow, reject string
	for _, opt := range options {
		switch opt.OptionID {
		case "allow-once":
			allow = opt.OptionID
		case "reject-once":
			reject = opt.OptionID
		}
	}
	if allow == "" || reject == "" {
		return "", "", false
	}
	return allow, reject, true
}

func (p *PermissionPrompter) decideYesNo(toolCall permissionToolCall, allowID, rejectID string) string {
	fmt.Fprintf(p.out, "%s (%s) — allow? [y/n] ", toolCall.Title, toolCall.Kind)
	for {
		line, err := p.in.ReadString('\n')
		switch strings.TrimSpace(strings.ToLower(line)) {
		case "y", "yes":
			return allowID
		case "n", "no":
			return rejectID
		}
		if err != nil {
			return rejectID // the fail-closed default is exactly rejectID in this shape
		}
		fmt.Fprint(p.out, "please answer y or n: ")
	}
}

func (p *PermissionPrompter) decideGeneric(toolCall permissionToolCall, options []permissionOption) string {
	fmt.Fprintf(p.out, "%s (%s):\n", toolCall.Title, toolCall.Kind)
	for i, opt := range options {
		fmt.Fprintf(p.out, "  %d) %s\n", i+1, opt.Name)
	}
	fmt.Fprint(p.out, "choose a number: ")
	for {
		line, err := p.in.ReadString('\n')
		if n, convErr := strconv.Atoi(strings.TrimSpace(line)); convErr == nil && n >= 1 && n <= len(options) {
			return options[n-1].OptionID
		}
		if err != nil {
			return failClosedOption(options)
		}
		fmt.Fprintf(p.out, "enter a number from 1 to %d: ", len(options))
	}
}

// failClosedOption picks a reject-flavored option if the offered set names
// one, else the last-listed option — used only when the operator's input
// stream is exhausted before a valid answer arrived.
func failClosedOption(options []permissionOption) string {
	for _, opt := range options {
		if opt.Kind == "reject_once" || opt.Kind == "reject" {
			return opt.OptionID
		}
	}
	return options[len(options)-1].OptionID
}
