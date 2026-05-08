package main

import (
	"context"
	"fmt"
	"os"

	"github.com/nskaggs/perfuncted"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:]))
}

func run(ctx context.Context, args []string) int {
	return runWithFactory(ctx, args, defaultOpenPFFactory)
}

func runWithFactory(ctx context.Context, args []string, openPFFactory func(*cliConfig) func() (*perfuncted.Perfuncted, error)) int {
	cmd := newRootCmd(openPFFactory)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return 1
	}
	return 0
}
