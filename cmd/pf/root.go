// cmd/pf is a thin CLI wrapper over the perfuncted library.
// Each command group maps to a bundle (Screen, Input, Window); subcommands
// map to bundle methods. All backend setup — including nested-session detection
// — flows through perfuncted.Open(), keeping the CLI and library in sync.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/spf13/cobra"
)

type cliConfig struct {
	nested                  bool
	traceActions            bool
	traceDelay              time.Duration
	sync                    bool
	inputBackend            string
	rememberInputPermission bool
	required                []perfuncted.Capability
	optional                []perfuncted.Capability
}

type sessionOpener func(context.Context) (*perfuncted.Session, error)

type cliOpenFactory func(*cliConfig) sessionOpener

func writeCLIOutput(w io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(w, format, args...)
	return err
}

func writeCLIMessage(w io.Writer, args ...any) error {
	_, err := fmt.Fprintln(w, args...)
	return err
}

func defaultOpenPFFactory(cfg *cliConfig) sessionOpener {
	return func(ctx context.Context) (*perfuncted.Session, error) {
		var opts []perfuncted.Option
		if cfg.nested {
			opts = append(opts, perfuncted.WithNested(perfuncted.SessionConfig{}))
		}
		if len(cfg.required) > 0 {
			opts = append(opts, perfuncted.Require(cfg.required...))
		}
		optional := cfg.optional
		if cfg.inputBackend == "remote-desktop" {
			// Portal input is required by definition and must not also be
			// requested as optional by broad info/diagnostic commands.
			optional = make([]perfuncted.Capability, 0, len(cfg.optional))
			for _, cap := range cfg.optional {
				if cap != perfuncted.CapabilityInput {
					optional = append(optional, cap)
				}
			}
		}
		if len(optional) > 0 {
			opts = append(opts, perfuncted.Optional(optional...))
		}
		if cfg.traceActions || cfg.traceDelay > 0 {
			opts = append(opts, perfuncted.WithTrace(true), perfuncted.WithTraceOut(os.Stderr))
		}
		if cfg.traceDelay > 0 {
			opts = append(opts, perfuncted.WithTraceDelay(cfg.traceDelay))
		}
		switch cfg.inputBackend {
		case "", "auto":
			if cfg.rememberInputPermission {
				return nil, fmt.Errorf("--remember-input-permission requires --input-backend remote-desktop")
			}
		case "remote-desktop":
			opts = append(opts, perfuncted.WithRemoteDesktopInput(perfuncted.RemoteDesktopInputOptions{PersistPermission: cfg.rememberInputPermission}))
		default:
			return nil, fmt.Errorf("unknown --input-backend %q (supported: auto, remote-desktop)", cfg.inputBackend)
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
	root.PersistentFlags().StringVar(&cfg.inputBackend, "input-backend", "auto", "input backend: auto or remote-desktop (authorized Wayland portal/EIS)")
	root.PersistentFlags().BoolVar(&cfg.rememberInputPermission, "remember-input-permission", false, "request persistent authorization for remote-desktop input")

	screenOpen := openRequired(
		openPFFactory,
		cfg,
		perfuncted.CapabilityScreen,
	)
	inputWindowOpen := openRequired(openPFFactory, cfg, perfuncted.CapabilityInput, perfuncted.CapabilityWindows)
	inputOutputOpen := openRequired(openPFFactory, cfg, perfuncted.CapabilityInput, perfuncted.CapabilityOutputs)
	inputDialogOpen := openRequired(openPFFactory, cfg, perfuncted.CapabilityInput, perfuncted.CapabilityWindows, perfuncted.CapabilityClipboard)
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
	accessibilityOpen := openRequired(
		openPFFactory,
		cfg,
		perfuncted.CapabilityAccessibility,
	)
	accessibilityWindowOpen := openRequired(
		openPFFactory,
		cfg,
		perfuncted.CapabilityAccessibility,
		perfuncted.CapabilityWindows,
	)
	allCapabilities := []perfuncted.Capability{
		perfuncted.CapabilityScreen,
		perfuncted.CapabilityInput,
		perfuncted.CapabilityWindows,
		perfuncted.CapabilityOutputs,
		perfuncted.CapabilityClipboard,
		perfuncted.CapabilityAccessibility,
	}
	infoOpen := openOptional(openPFFactory, cfg, allCapabilities...)
	runOpen := openOptional(openPFFactory, cfg, allCapabilities...)
	root.AddCommand(
		screenCmd(screenOpen),
		inputCmd(inputOpen, cfg, inputWindowOpen, inputOutputOpen, inputDialogOpen),
		windowCmd(windowOpen, cfg),
		outputCmd(outputOpen),
		findCmd(screenOpen),
		runCmd(runOpen, cfg),
		clipboardCmd(clipboardOpen),
		accessibilityCmd(accessibilityOpen, accessibilityWindowOpen),
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
