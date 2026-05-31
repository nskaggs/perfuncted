package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/ctxutil"
)

var (
	version = ""
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:]))
}

func run(ctx context.Context, args []string) int {
	return runWithFactory(ctx, args, defaultOpenPFFactory)
}

func runWithFactory(ctx context.Context, args []string, openPFFactory func(*cliConfig) func() (*perfuncted.Perfuncted, error)) int {
	ctx = ctxutil.Default(ctx)
	cmd := newRootCmd(openPFFactory)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return 1
	}
	return 0
}
