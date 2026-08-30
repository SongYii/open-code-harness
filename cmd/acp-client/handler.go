package main

import (
	"context"
	"encoding/json"
	"io"

	acpclient "github.com/SongYii/open-code-harness/internal/client/acp"
)

// clientHandler implements internal/client/acp.Handler by wiring a
// Trajectory's reduced RenderEvents to the terminal and a
// PermissionPrompter to the operator's own stdin/stdout.
type clientHandler struct {
	trajectory *acpclient.Trajectory
	prompter   *acpclient.PermissionPrompter
	out        io.Writer
	tty        bool
}

func (h *clientHandler) HandleSessionUpdate(params json.RawMessage) {
	render(h.out, h.trajectory.Apply(params), h.tty)
}

func (h *clientHandler) HandleRequestPermission(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	return h.prompter.HandleRequestPermission(ctx, params)
}

var _ acpclient.Handler = (*clientHandler)(nil)
