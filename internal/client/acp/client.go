package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// protocolVersion is the only ACP protocol version this client speaks.
const protocolVersion = 1

// AgentInfo is the agent identity Initialize reads back.
type AgentInfo struct {
	Name    string
	Version string
}

// Capabilities is the subset of agentCapabilities this slice reads.
type Capabilities struct {
	LoadSession bool
}

var errNilHandler = errors.New("acp: NewClient requires a non-nil Handler")

// Client is the ACP v1 client role: it owns one Connection to a spawned
// agent and exposes exactly the method surface this slice needs. NewClient
// constructs the Connection itself, rather than accepting an
// already-built one, specifically so a Client can never exist without a
// real Handler wired in from the start — a session/load's replayed
// session/update notifications can arrive before session/load's own
// response resolves, and there must never be a window where those would
// be silently dropped for want of a handler.
type Client struct {
	conn *Connection
}

// NewClient starts the read loop immediately, exactly like NewConnection.
func NewClient(r io.ReadCloser, w io.Writer, handler Handler) (*Client, error) {
	if handler == nil {
		return nil, errNilHandler
	}
	return &Client{conn: NewConnection(r, w, handler)}, nil
}

// Close stops the underlying Connection.
func (c *Client) Close() error { return c.conn.Close() }

type initializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities"`
}

// clientCapabilities is deliberately empty: no fs key, no terminal key,
// present or absent-but-false — an explicit decline of both, per the
// design's §1.3 decision. This project's own agent
// (internal/harness/adapters/acp's initializeParams) parses no client
// capabilities today, so there is neither a compatibility cost nor a
// present use for declaring either.
type clientCapabilities struct{}

type initializeResult struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities agentCapabilities `json:"agentCapabilities"`
	AgentInfo         agentInfo         `json:"agentInfo"`
}

type agentCapabilities struct {
	LoadSession bool `json:"loadSession"`
}

type agentInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Initialize performs the ACP handshake.
func (c *Client) Initialize(ctx context.Context) (AgentInfo, Capabilities, error) {
	raw, err := c.conn.Call(ctx, "initialize", initializeParams{ProtocolVersion: protocolVersion})
	if err != nil {
		return AgentInfo{}, Capabilities{}, fmt.Errorf("acp: initialize: %w", err)
	}
	var result initializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return AgentInfo{}, Capabilities{}, fmt.Errorf("acp: initialize: decode result: %w", err)
	}
	return AgentInfo{Name: result.AgentInfo.Name, Version: result.AgentInfo.Version},
		Capabilities{LoadSession: result.AgentCapabilities.LoadSession},
		nil
}

type sessionNewParams struct {
	Cwd string `json:"cwd"`
}

type sessionNewResult struct {
	SessionID string `json:"sessionId"`
}

// NewSession creates a fresh session at cwd.
func (c *Client) NewSession(ctx context.Context, cwd string) (string, error) {
	raw, err := c.conn.Call(ctx, "session/new", sessionNewParams{Cwd: cwd})
	if err != nil {
		return "", fmt.Errorf("acp: session/new: %w", err)
	}
	var result sessionNewResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("acp: session/new: decode result: %w", err)
	}
	return result.SessionID, nil
}

type sessionLoadParams struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
}

// LoadSession resumes an existing session at cwd. Any session/update
// notifications the agent replays as part of loading arrive at this
// Client's Handler in wire order, which may be before this call itself
// returns — the same single read loop delivers both, strictly in the
// order the agent sent them.
func (c *Client) LoadSession(ctx context.Context, sessionID, cwd string) error {
	if _, err := c.conn.Call(ctx, "session/load", sessionLoadParams{SessionID: sessionID, Cwd: cwd}); err != nil {
		return fmt.Errorf("acp: session/load: %w", err)
	}
	return nil
}

type promptParams struct {
	SessionID string        `json:"sessionId"`
	Prompt    []promptBlock `json:"prompt"`
}

type promptBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type promptResult struct {
	StopReason string `json:"stopReason"`
}

// Prompt sends one prompt and blocks for its terminal response, or until
// ctx is done. It does not itself inspect session/update traffic arriving
// while the prompt is in flight — that is the Handler's job, wired in at
// NewClient — Prompt's only contract is "block until this specific
// prompt's terminal response, or ctx is done, then return the stop
// reason".
func (c *Client) Prompt(ctx context.Context, sessionID, text string) (string, error) {
	raw, err := c.conn.Call(ctx, "session/prompt", promptParams{
		SessionID: sessionID,
		Prompt:    []promptBlock{{Type: "text", Text: text}},
	})
	if err != nil {
		return "", fmt.Errorf("acp: session/prompt: %w", err)
	}
	var result promptResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("acp: session/prompt: decode result: %w", err)
	}
	return result.StopReason, nil
}

type sessionCancelParams struct {
	SessionID string `json:"sessionId"`
}

// Cancel sends session/cancel as a fire-and-forget notification, matching
// this project's own agent-side cancellation semantics (acp-v1.md): the
// in-flight Prompt call observes the resulting "cancelled" stop reason on
// its own pending response, not a separate signal from Cancel.
func (c *Client) Cancel(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.conn.Notify("session/cancel", sessionCancelParams{SessionID: sessionID})
}
