// Command och is the stock launcher, without third-party registrations.
package main

import (
	"context"
	"fmt"
	"github.com/SongYii/open-code-harness/sdk/och"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := och.Run(ctx, os.Args[1:], och.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, och.Extensions{}); err != nil {
		fmt.Fprintln(os.Stderr, "och:", err)
		os.Exit(1)
	}
}
