// Package och exposes the experimental startup-composition SDK. Source
// compatibility is not yet promised; pin a tested revision. Compile a custom
// main importing only sdk packages; domain, storage and service remain private.
// Experimental API status does not relax durable-event integrity or replay
// compatibility requirements.
package och

import (
	"context"
	"io"

	"github.com/SongYii/open-code-harness/internal/launcher"
	"github.com/SongYii/open-code-harness/sdk/contextpolicy"
)

type Streams struct {
	In       io.ReadCloser
	Out, Err io.Writer
}
type Extensions struct{ ContextPolicies []contextpolicy.Registration }

// Run shares och's flags, commands, ACP transport and shutdown. The caller
// owns signal handling. Extensions are resolved before resources are opened
// and are immutable for the run; do not mutate registrations concurrently.
func Run(ctx context.Context, args []string, streams Streams, extensions Extensions) error {
	registrations := append([]contextpolicy.Registration(nil), extensions.ContextPolicies...)
	return launcher.Run(ctx, args, launcher.Streams{In: streams.In, Out: streams.Out, Err: streams.Err}, registrations)
}
