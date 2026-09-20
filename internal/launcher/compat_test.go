package launcher

import (
	"context"
	"os"
)

func run(args []string) error {
	return Run(context.Background(), args, Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}, Extensions{})
}
