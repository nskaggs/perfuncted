// cmd/pf is a thin CLI wrapper over the perfuncted library.
// Each command group maps to a bundle (Screen, Input, Window); subcommands
// map to bundle methods. All backend setup — including nested-session detection
// — flows through perfuncted.Open(), keeping the CLI and library in sync.
package main

import (
	"context"
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
	sync         bool
	required     []perfuncted.Capability
	optional     []perfuncted.Capability
}

type sessionOpener func(context.Context) (*perfuncted.Session, error)

type cliOpenFactory func(*cliConfig) sessionOpener

func defaultOpenPFFactory(cfg *cliConfig) sessionOpener {
	return func(ctx context.Context) (*perfuncted.Session, error) {
		var opts []perfuncted.Option
		if cfg.nested {
			opts = append(opts, perfuncted.WithNested(perfuncted.SessionConfig{}))
		}
		if len(cfg.required) > 0 {
			opts = append(opts, perfuncted.Require(cfg.required...))
		}
		if len(cfg.optional) > 0 {
			opts = append(opts, perfuncted.Optional(cfg.optional...))
		}
		if cfg.traceActions || cfg.traceDelay > 0 {
			opts = append(opts, perfuncted.WithTrace(true), perfuncted.WithTraceOut(os.Stderr))
		}
		if cfg.traceDelay > 0 {
			opts = append(opts, perfuncted.WithTraceDelay(cfg.traceDelay))
		}
		return perfuncted.Open(ctx, opts...)
	}
}

func openRequired(
	factory cliOpenFactory,
	cfg *cliConfig,
	capabilities ...perfuncted.Capability,
) sessionOpener {
	return func(ctx context.Context) (*perfuncted.Session, error) {
		scoped := *cfg
		scoped.required = append([]perfuncted.Capability(nil), capabilities...)
		scoped.optional = nil
		return factory(&scoped)(ctx)
	}
}

func openOptional(
	factory cliOpenFactory,
	cfg *cliConfig,
	capabilities ...perfuncted.Capability,
) sessionOpener {
	return func(ctx context.Context) (*perfuncted.Session, error) {
		scoped := *cfg
		scoped.required = nil
		scoped.optional = append([]perfuncted.Capability(nil), capabilities...)
		return factory(&scoped)(ctx)
	}
}

func newRootCmd(openPFFactory cliOpenFactory) *cobra.Command {
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
		"start and target a new nested Wayland session")
	root.PersistentFlags().BoolVar(&cfg.traceActions, "trace-actions", cfg.traceActions,
		"print each API action to stderr as it runs")
	root.PersistentFlags().DurationVar(&cfg.traceDelay, "trace-delay", cfg.traceDelay,
		"sleep after each traced action")
	root.PersistentFlags().BoolVar(&cfg.sync, "sync", false,
		"sync after observable mutating commands when supported")

	screenOpen := openRequired(
		openPFFactory,
		cfg,
		perfuncted.CapabilityScreen,
	)
	inputOpen := openRequired(
		openPFFactory,
		cfg,
		perfuncted.CapabilityInput,
	)
	windowOpen := openRequired(
		openPFFactory,
		cfg,
		perfuncted.CapabilityWindows,
	)
	outputOpen := openRequired(
		openPFFactory,
		cfg,
		perfuncted.CapabilityOutputs,
	)
	clipboardOpen := openRequired(
		openPFFactory,
		cfg,
		perfuncted.CapabilityClipboard,
	)
	allCapabilities := []perfuncted.Capability{
		perfuncted.CapabilityScreen,
		perfuncted.CapabilityInput,
		perfuncted.CapabilityWindows,
		perfuncted.CapabilityOutputs,
		perfuncted.CapabilityClipboard,
	}
	infoOpen := openOptional(openPFFactory, cfg, allCapabilities...)
	runOpen := openOptional(openPFFactory, cfg, allCapabilities...)
	root.AddCommand(
		screenCmd(screenOpen),
		inputCmd(inputOpen, cfg),
		windowCmd(windowOpen, cfg),
		outputCmd(outputOpen),
		findCmd(screenOpen),
		runCmd(runOpen),
		clipboardCmd(clipboardOpen),
		infoCmd(infoOpen),
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
