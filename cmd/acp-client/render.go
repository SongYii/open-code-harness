package main

import (
	"fmt"
	"io"

	acpclient "github.com/SongYii/open-code-harness/internal/client/acp"
)

// render prints one RenderEvent to out. When tty is true, a
// RenderToolCallUpdate reprints its status in place (carriage return plus
// an ANSI clear-to-end-of-line) rather than appending a new line, since a
// tool call's status typically changes a few times in quick succession
// and a live terminal reads better without a scrolling wall of duplicate
// lines; a non-TTY (piped output, CI) always appends a plain new line,
// which has no terminal-control-sequence dependency at all.
func render(out io.Writer, event acpclient.RenderEvent, tty bool) {
	switch event.Kind {
	case acpclient.RenderMessageChunk:
		fmt.Fprint(out, event.Text)
	case acpclient.RenderToolCall:
		fmt.Fprintf(out, "\n[tool] %s (%s): %s\n", event.Tool.Title, event.Tool.Kind, event.Tool.Status)
	case acpclient.RenderToolCallUpdate:
		if tty {
			fmt.Fprintf(out, "\r[tool] %s: %s\x1b[K", event.Tool.Title, event.Tool.Status)
		} else {
			fmt.Fprintf(out, "[tool] %s: %s\n", event.Tool.Title, event.Tool.Status)
		}
	case acpclient.RenderAnomaly:
		fmt.Fprintf(out, "\n[unrecognized: %s] %s\n", event.SessionUpdate, event.Detail)
	}
}
