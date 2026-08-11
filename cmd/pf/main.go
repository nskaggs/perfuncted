package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nskaggs/perfuncted/internal/contextutil"
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
	code := run(ctx, os.Args[1:])
	os.Exit(code) //nolint:gocritic // exitAfterDefer: defer stop() runs before os.Exit in process exit
}

func run(ctx context.Context, args []string) int {
	return runWithFactory(ctx, args, defaultOpenPFFactory)
}

func runWithFactory(
	ctx context.Context,
	args []string,
	openPFFactory cliOpenFactory,
) int {
	ctx = contextutil.Default(ctx)
	cmd := newRootCmd(openPFFactory) //nolint:contextcheck // cobra command doesn't accept context at construction time
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return 1
	}
	return 0
}
