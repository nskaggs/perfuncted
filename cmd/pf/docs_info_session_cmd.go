package main

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted"
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
			fmt.Fprintf(cmd.OutOrStdout(), "Documentation generated in %s\n", dirFlag)
			return nil
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
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "pf %s\n", version)
			fmt.Fprintf(cmd.OutOrStdout(), "  commit:  %s\n", commit)
			fmt.Fprintf(cmd.OutOrStdout(), "  date:    %s\n", date)
			fmt.Fprintf(cmd.OutOrStdout(), "  builtBy: %s\n", builtBy)
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
			out := cmd.OutOrStdout()

			fmt.Fprintln(out, "── Environment ────────────────────────────────────")
			fmt.Fprintf(out, "  Target:           %s\n", report.Target)
			fmt.Fprintf(out, "  Compositor:       %s\n", report.Compositor)
			for _, key := range []string{
				"WAYLAND_DISPLAY",
				"DISPLAY",
				"XDG_CURRENT_DESKTOP",
				"XDG_RUNTIME_DIR",
			} {
				if value := report.Environment[key]; value != "" {
					fmt.Fprintf(out, "  %-18s %s\n", key+":", value)
				}
			}

			fmt.Fprintln(out, "\n── Capabilities ────────────────────────────────────")
			for _, status := range session.Capabilities() {
				entry := report.Capabilities[string(status.Capability)]
				marker := "[ ]"
				if entry.Supported {
					marker = "[✓]"
				}
				fmt.Fprintf(
					out,
					"  %s %-10s backend=%s operations=%s",
					marker,
					status.Capability,
					entry.Backend,
					strings.Join(entry.Operations, ","),
				)
				if entry.Reason != "" {
					fmt.Fprintf(out, " failure=%s", entry.Reason)
				}
				fmt.Fprintln(out)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outputFlag, "output", "plain", "plain|json")
	return cmd
}

// ── session ───────────────────────────────────────────────────────────────────────────

func sessionCmd() *cobra.Command {
	return sessionCmdWithCleaner(perfuncted.CleanupStaleSessions)
}

func sessionCmdWithCleaner(cleanStaleSessions func(time.Duration)) *cobra.Command {
	const minimumCleanupAge = 5 * time.Minute

	cmd := &cobra.Command{
		Use:   "session",
		Short: "Session diagnostics and utilities",
	}

	typeCmd := &cobra.Command{
		Use:   "type",
		Short: "Print whether the current session is nested or host",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			detection := perfuncted.DetectSession()
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "session: %s\n", detection.Kind)
			fmt.Fprintf(out, "  xdg_runtime_dir: %s\n", diagnosticValue(detection.XDGRuntimeDir))
			fmt.Fprintf(out, "  wayland_display: %s\n", detection.WaylandDisplay)
			fmt.Fprintf(out, "  dbus_address: %s\n", diagnosticValue(detection.DBusAddress))
		},
	}

	check := &cobra.Command{
		Use:   "check",
		Short: "Check if the current runtime environment is ready for automation",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "── Environment Variable Checks ──────────────────")

			xdg := os.Getenv("XDG_RUNTIME_DIR")
			if xdg == "" {
				fmt.Fprintln(out, "  [✗] XDG_RUNTIME_DIR is not set")
			} else if info, err := os.Stat(xdg); err == nil && info.IsDir() {
				fmt.Fprintln(out, "  [✓] XDG_RUNTIME_DIR=<set>")
			} else {
				fmt.Fprintln(out, "  [✗] XDG_RUNTIME_DIR=<set> (not found)")
			}

			wd := os.Getenv("WAYLAND_DISPLAY")
			if wd == "" {
				fmt.Fprintln(out, "  [✗] WAYLAND_DISPLAY is not set")
			} else {
				sock := wl.ResolveSocketPath(wd, xdg)
				if sock == "" {
					fmt.Fprintf(out, "  [✗] WAYLAND_DISPLAY=%s (socket unresolved without XDG_RUNTIME_DIR)\n", wd)
				} else {
					if info, err := os.Stat(sock); err == nil && info.Mode()&os.ModeSocket != 0 {
						fmt.Fprintf(out, "  [✓] WAYLAND_DISPLAY=%s (socket reachable)\n", wd)
					} else {
						fmt.Fprintf(out, "  [✗] WAYLAND_DISPLAY=%s (socket missing)\n", wd)
					}
				}
			}

			if addr := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); addr != "" {
				fmt.Fprintln(out, "  [✓] DBUS_SESSION_BUS_ADDRESS=<set>")
			} else {
				fmt.Fprintln(out, "  [✗] DBUS_SESSION_BUS_ADDRESS is not set")
			}

			fmt.Fprintln(out, "\n── System Resource Checks ────────────────────────")
			if info, err := os.Stat("/dev/uinput"); err == nil {
				fmt.Fprintf(out, "  [✓] /dev/uinput accessible (mode %v)\n", info.Mode())
			} else {
				fmt.Fprintf(out, "  [✗] /dev/uinput not accessible: %v\n", err)
			}

			fmt.Fprintln(out, "\n  Run `pf info` for the full backend capability matrix.")
		},
	}

	var startResX, startResY int
	var startSwayConf string
	startCmd := &cobra.Command{
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

			session, err := perfuncted.Open(
				cmd.Context(),
				perfuncted.WithHeadless(cfg),
			)
			if err != nil {
				return err
			}
			defer session.Close()
			runtime := environmentMap(session.Env())

			fmt.Fprintf(
				cmd.OutOrStdout(),
				"export XDG_RUNTIME_DIR=%s\n",
				runtime["XDG_RUNTIME_DIR"],
			)
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"export WAYLAND_DISPLAY=%s\n",
				runtime["WAYLAND_DISPLAY"],
			)
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"export DBUS_SESSION_BUS_ADDRESS=%s\n",
				runtime["DBUS_SESSION_BUS_ADDRESS"],
			)
			fmt.Fprintf(
				cmd.ErrOrStderr(),
				"session: running (XDG=%s)\n",
				runtime["XDG_RUNTIME_DIR"],
			)
			fmt.Fprintln(cmd.ErrOrStderr(), "session: press Ctrl+C to stop")

			<-cmd.Context().Done()

			fmt.Fprintln(cmd.ErrOrStderr(), "\nsession: stopping...")
			if err := session.Close(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "session: stopped")
			return nil
		},
	}
	startCmd.Flags().IntVar(&startResX, "res-x", 1024, "horizontal resolution")
	startCmd.Flags().IntVar(&startResY, "res-y", 768, "vertical resolution")
	startCmd.Flags().StringVar(&startSwayConf, "sway-config", "", "path to custom sway config (default: embedded)")

	var cleanupMaxAge time.Duration
	cleanupCmd := &cobra.Command{
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
			fmt.Fprintf(cmd.OutOrStdout(), "stale session cleanup pass completed (max age %s)\n", cleanupMaxAge)
			return nil
		},
	}
	cleanupCmd.Flags().DurationVar(&cleanupMaxAge, "max-age", 24*time.Hour, "age threshold for malformed owner metadata (minimum 5m)")

	cmd.AddCommand(typeCmd, check, startCmd, cleanupCmd)
	return cmd
}

func diagnosticValue(value string) string {
	if value == "" {
		return ""
	}
	return "<set>"
}
