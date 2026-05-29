// cmd/pf is a thin CLI wrapper over the perfuncted library.
// Each command group maps to a bundle (Screen, Input, Window); subcommands
// map to bundle methods. All backend setup — including nested-session detection
// — flows through perfuncted.New(), keeping the CLI and library in sync.
package main

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/spf13/cobra"
)

type cliConfig struct {
	nested       bool
	traceActions bool
	traceDelay   time.Duration
	maxX         int32
	maxY         int32
	sync         bool
}

func defaultOpenPFFactory(cfg *cliConfig) func() (*perfuncted.Perfuncted, error) {
	return func() (*perfuncted.Perfuncted, error) {
		var traceWriter io.Writer
		if cfg.traceActions || cfg.traceDelay > 0 {
			traceWriter = os.Stderr
		}
		return perfuncted.New(perfuncted.Options{
			Nested:      cfg.nested,
			MaxX:        cfg.maxX,
			MaxY:        cfg.maxY,
			TraceWriter: traceWriter,
			TraceDelay:  cfg.traceDelay,
		})
	}
}

func newRootCmd(openPFFactory func(*cliConfig) func() (*perfuncted.Perfuncted, error)) *cobra.Command {
	cfg := &cliConfig{}
	if envBool(os.Getenv("PF_TRACE_ACTIONS")) {
		cfg.traceActions = true
	}
	if d, err := time.ParseDuration(os.Getenv("PF_TRACE_DELAY")); err == nil && d > 0 {
		cfg.traceDelay = d
	}

	root := &cobra.Command{
		Use:               "pf",
		Short:             "perfuncted — screen automation CLI",
		DisableAutoGenTag: true,
		SilenceUsage:      true,
		SilenceErrors:     true,
	}
	root.PersistentFlags().BoolVar(&cfg.nested, "nested", false,
		"auto-detect and connect to a nested Wayland session in /tmp")
	root.PersistentFlags().Int32Var(&cfg.maxX, "max-x", 0,
		"input coordinate space width (default 1920)")
	root.PersistentFlags().Int32Var(&cfg.maxY, "max-y", 0,
		"input coordinate space height (default 1080)")
	root.PersistentFlags().BoolVar(&cfg.traceActions, "trace-actions", cfg.traceActions,
		"print each API action to stderr as it runs")
	root.PersistentFlags().DurationVar(&cfg.traceDelay, "trace-delay", cfg.traceDelay,
		"sleep after each traced action")
	root.PersistentFlags().BoolVar(&cfg.sync, "sync", false,
		"sync after observable mutating commands when supported")

	openPF := openPFFactory(cfg)
	root.AddCommand(
		screenCmd(openPF, cfg),
		inputCmd(openPF, cfg),
		windowCmd(openPF, cfg),
		outputCmd(openPFFactory, cfg),
		findCmd(openPF),
		runCmd(root, openPFFactory, cfg),
		clipboardCmd(openPF),
		infoCmd(),
		sessionCmd(),
		docsCmd(root),
		versionCmd(),
	)
	return root
}

func envBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
