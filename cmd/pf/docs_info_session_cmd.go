package main

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted"
	diagnostic "github.com/nskaggs/perfuncted/internal/diagnostic"
	"github.com/nskaggs/perfuncted/internal/wl"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

func docsCmd(root *cobra.Command) *cobra.Command {
	var dirFlag string
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate markdown documentation for the CLI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := os.MkdirAll(dirFlag, 0755); err != nil {
				return err
			}
			if err := doc.GenMarkdownTree(root, dirFlag); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "Documentation generated in %s\n", dirFlag)
		},
	}
	cmd.Flags().StringVarP(&dirFlag, "dir", "d", "./docs-cli", "directory to write markdown files")
	return cmd
}

// ── version ─────────────────────────────────────────────────────────────────────────

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if err := writeCLIOutput(out, "pf %s\n", version); err != nil {
				return err
			}
			if err := writeCLIOutput(out, "  commit:  %s\n", commit); err != nil {
				return err
			}
			if err := writeCLIOutput(out, "  date:    %s\n", date); err != nil {
				return err
			}
			return writeCLIOutput(out, "  builtBy: %s\n", builtBy)
		},
	}
}

// ── info ────────────────────────────────────────────────────────────────────────────

func infoCmd(
	openPF sessionOpener,
) *cobra.Command {
	var outputFlag string
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Display resolved capabilities for this environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := parseOutputMode(outputFlag)
			if err != nil {
				return err
			}
			session, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer session.Close()
			report := buildInfoReport(session)
			if mode == outputModeJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			return writeInfoPlain(cmd.OutOrStdout(), session, report)
		},
	}
	cmd.Flags().StringVar(&outputFlag, "output", "plain", "plain|json")
	return cmd
}

func writeInfoPlain(out io.Writer, session *perfuncted.Session, report infoReport) error {
	if err := writeCLIMessage(out, "── Environment ────────────────────────────────────"); err != nil {
		return err
	}
	if err := writeInfoEnvironment(out, report); err != nil {
		return err
	}
	if err := writeCLIMessage(out, "\n── Capabilities ────────────────────────────────────"); err != nil {
		return err
	}
	return writeInfoCapabilities(out, session.Capabilities(), report)
}

func writeInfoEnvironment(out io.Writer, report infoReport) error {
	if err := writeCLIOutput(out, "  Target:           %s\n", report.Target); err != nil {
		return err
	}
	if err := writeCLIOutput(out, "  Compositor:       %s\n", report.Compositor); err != nil {
		return err
	}
	for _, key := range []string{
		"WAYLAND_DISPLAY",
		"DISPLAY",
		"XDG_CURRENT_DESKTOP",
		"XDG_RUNTIME_DIR",
	} {
		value := report.Environment[key]
		if value == "" {
			continue
		}
		if err := writeCLIOutput(out, "  %-18s %s\n", key+":", value); err != nil {
			return err
		}
	}
	return nil
}

func writeInfoCapabilities(out io.Writer, statuses []perfuncted.CapabilityStatus, report infoReport) error {
	for _, status := range statuses {
		entry := report.Capabilities[string(status.Capability)]
		marker := "[ ]"
		if entry.Supported {
			marker = "[✓]"
		}
		if err := writeCLIOutput(
			out,
			"  %s %-10s backend=%s operations=%s",
			marker,
			status.Capability,
			entry.Backend,
			strings.Join(entry.Operations, ","),
		); err != nil {
			return err
		}
		if entry.Reason != "" {
			if err := writeCLIOutput(out, " failure=%s", entry.Reason); err != nil {
				return err
			}
		}
		if err := writeCLIMessage(out); err != nil {
			return err
		}
	}
	return nil
}

// ── session ───────────────────────────────────────────────────────────────────────────

func sessionCmd() *cobra.Command {
	return sessionCmdWithCleaner(perfuncted.CleanupStaleSessions)
}

func sessionCmdWithCleaner(cleanStaleSessions func(time.Duration)) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Session diagnostics and utilities",
	}
	cmd.AddCommand(
		sessionTypeCmd(),
		sessionCheckCmd(),
		sessionStartCmd(),
		sessionCleanupCmd(cleanStaleSessions),
	)
	return cmd
}

func sessionTypeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "type",
		Short: "Print whether the current session is nested or host",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			detection := perfuncted.DetectSession()
			out := cmd.OutOrStdout()
			if err := writeCLIOutput(out, "session: %s\n", detection.Kind); err != nil {
				return err
			}
			if err := writeCLIOutput(out, "  xdg_runtime_dir: %s\n", diagnosticValue(detection.XDGRuntimeDir)); err != nil {
				return err
			}
			if err := writeCLIOutput(out, "  wayland_display: %s\n", detection.WaylandDisplay); err != nil {
				return err
			}
			return writeCLIOutput(out, "  dbus_address: %s\n", diagnosticValue(detection.DBusAddress))
		},
	}
}

func sessionCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Check if the current runtime environment is ready for automation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeSessionCheck(cmd.OutOrStdout())
		},
	}
}

func writeSessionCheck(out io.Writer) error {
	if err := writeCLIMessage(out, "── Environment Variable Checks ──────────────────"); err != nil {
		return err
	}
	xdg := os.Getenv("XDG_RUNTIME_DIR")
	if err := writeRuntimeDirCheck(out, xdg); err != nil {
		return err
	}

	wd := os.Getenv("WAYLAND_DISPLAY")
	if err := writeWaylandDisplayCheck(out, wd, xdg); err != nil {
		return err
	}

	if err := writeDBusCheck(out, os.Getenv("DBUS_SESSION_BUS_ADDRESS")); err != nil {
		return err
	}

	if err := writeCLIMessage(out, "\n── System Resource Checks ────────────────────────"); err != nil {
		return err
	}
	return writeUinputCheck(out)
}

func writeRuntimeDirCheck(out io.Writer, runtimeDir string) error {
	if runtimeDir == "" {
		return writeCLIMessage(out, "  [✗] XDG_RUNTIME_DIR is not set")
	}
	info, err := os.Stat(runtimeDir)
	if err == nil && info.IsDir() {
		return writeCLIMessage(out, "  [✓] XDG_RUNTIME_DIR=<set>")
	}
	return writeCLIMessage(out, "  [✗] XDG_RUNTIME_DIR=<set> (not found)")
}

func writeWaylandDisplayCheck(out io.Writer, display, runtimeDir string) error {
	if display == "" {
		return writeCLIMessage(out, "  [✗] WAYLAND_DISPLAY is not set")
	}
	return writeWaylandCheck(out, display, runtimeDir)
}

func writeDBusCheck(out io.Writer, address string) error {
	if address != "" {
		return writeCLIMessage(out, "  [✓] DBUS_SESSION_BUS_ADDRESS=<set>")
	}
	return writeCLIMessage(out, "  [✗] DBUS_SESSION_BUS_ADDRESS is not set")
}

func writeUinputCheck(out io.Writer) error {
	info, err := os.Stat("/dev/uinput")
	if err == nil {
		return writeCLIOutput(out, "  [✓] /dev/uinput accessible (mode %v)\n", info.Mode())
	}
	return writeCLIOutput(out, "  [✗] /dev/uinput not accessible: %v\n", err)
}

func writeWaylandCheck(out io.Writer, display, runtimeDir string) error {
	sock := wl.ResolveSocketPath(display, runtimeDir)
	if sock == "" {
		return writeCLIOutput(out, "  [✗] WAYLAND_DISPLAY=%s (socket unresolved without XDG_RUNTIME_DIR)\n", display)
	}
	if info, err := os.Stat(sock); err == nil && info.Mode()&os.ModeSocket != 0 {
		return writeCLIOutput(out, "  [✓] WAYLAND_DISPLAY=%s (socket reachable)\n", display)
	}
	return writeCLIOutput(out, "  [✗] WAYLAND_DISPLAY=%s (socket missing)\n", display)
}

func sessionStartCmd() *cobra.Command {
	var startResX, startResY int
	var startSwayConf string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a headless sway session and print env vars",
		Args:  cobra.NoArgs,
		Long: `Start a new isolated headless sway session (dbus, sway, wl-paste)
and print the environment variables needed to connect to it.

The session runs until this process is interrupted (Ctrl+C) or killed.
Use the printed env vars in another terminal to connect:

  eval $(pf session start)
  kwrite /tmp/test.txt &
  pf screen grab --out /tmp/shot.png`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := perfuncted.SessionConfig{}
			if startResX > 0 && startResY > 0 {
				cfg.Resolution = image.Pt(startResX, startResY)
			}
			cfg.SwayConfigPath = startSwayConf

			session, err := perfuncted.Open(cmd.Context(), perfuncted.WithHeadless(cfg))
			if err != nil {
				return err
			}
			defer session.Close()
			runtime := diagnostic.EnvironmentMap(session.Env())
			if err := writeCLIOutput(cmd.OutOrStdout(), "export XDG_RUNTIME_DIR=%s\n", runtime["XDG_RUNTIME_DIR"]); err != nil {
				return err
			}
			if err := writeCLIOutput(cmd.OutOrStdout(), "export WAYLAND_DISPLAY=%s\n", runtime["WAYLAND_DISPLAY"]); err != nil {
				return err
			}
			if err := writeCLIOutput(cmd.OutOrStdout(), "export DBUS_SESSION_BUS_ADDRESS=%s\n", runtime["DBUS_SESSION_BUS_ADDRESS"]); err != nil {
				return err
			}
			if err := writeCLIOutput(cmd.ErrOrStderr(), "session: running (XDG=%s)\n", runtime["XDG_RUNTIME_DIR"]); err != nil {
				return err
			}
			if err := writeCLIMessage(cmd.ErrOrStderr(), "session: press Ctrl+C to stop"); err != nil {
				return err
			}
			<-cmd.Context().Done()
			if err := writeCLIMessage(cmd.ErrOrStderr(), "\nsession: stopping..."); err != nil {
				return err
			}
			if err := session.Close(); err != nil {
				return err
			}
			return writeCLIMessage(cmd.ErrOrStderr(), "session: stopped")
		},
	}
	cmd.Flags().IntVar(&startResX, "res-x", 1024, "horizontal resolution")
	cmd.Flags().IntVar(&startResY, "res-y", 768, "vertical resolution")
	cmd.Flags().StringVar(&startSwayConf, "sway-config", "", "path to custom sway config (default: embedded)")
	return cmd
}

func sessionCleanupCmd(cleanStaleSessions func(time.Duration)) *cobra.Command {
	const minimumCleanupAge = 5 * time.Minute
	var cleanupMaxAge time.Duration
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Safely clean stale managed session runtimes",
		Long: `Clean stale Perfuncted runtime directories through the library's
ownership-aware cleanup path. Live owner processes are retained, and recorded
child process IDs are only terminated when their XDG runtime directory matches
the stale session being removed. Dead owner PIDs are reaped immediately;
missing owner files retain a five-minute creation grace, while --max-age
governs malformed owner metadata.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cleanupMaxAge < minimumCleanupAge {
				return fmt.Errorf("--max-age must be at least %s", minimumCleanupAge)
			}
			cleanStaleSessions(cleanupMaxAge)
			return writeCLIOutput(cmd.OutOrStdout(), "stale session cleanup pass completed (max age %s)\n", cleanupMaxAge)
		},
	}
	cmd.Flags().DurationVar(&cleanupMaxAge, "max-age", 24*time.Hour, "age threshold for malformed owner metadata (minimum 5m)")
	return cmd
}

func diagnosticValue(value string) string {
	if value == "" {
		return ""
	}
	return "<set>"
}
