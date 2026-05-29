package main

import (
	"encoding/json"
	"fmt"
	"image"
	"os"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/wl"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

func docsCmd(root *cobra.Command) *cobra.Command {
	var dirFlag string
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate markdown documentation for the CLI",
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
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "pf %s\n", version)
			fmt.Fprintf(cmd.OutOrStdout(), "  commit:  %s\n", commit)
			fmt.Fprintf(cmd.OutOrStdout(), "  date:    %s\n", date)
			fmt.Fprintf(cmd.OutOrStdout(), "  builtBy: %s\n", builtBy)
		},
	}
}

// ── info ────────────────────────────────────────────────────────────────────────────

func infoCmd() *cobra.Command {
	var outputFlag string
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Probe and display supported backends for this environment",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := parseOutputMode(outputFlag)
			if err != nil {
				return err
			}
			if mode == outputModeJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(buildInfoReport())
			}
			out := cmd.OutOrStdout()
			kind := compositor.Detect()

			fmt.Fprintln(out, "── Environment ────────────────────────────────────")
			fmt.Fprintf(out, "  Compositor:       %s\n", kind)
			if d := os.Getenv("WAYLAND_DISPLAY"); d != "" {
				fmt.Fprintf(out, "  WAYLAND_DISPLAY:  %s\n", d)
			}
			if d := os.Getenv("DISPLAY"); d != "" {
				fmt.Fprintf(out, "  DISPLAY:          %s\n", d)
			}
			if d := os.Getenv("XDG_CURRENT_DESKTOP"); d != "" {
				fmt.Fprintf(out, "  XDG_CURRENT_DESKTOP: %s\n", d)
			}

			fmt.Fprintln(out, "\n── Screen ──────────────────────────────────────────")
			for _, r := range screen.Probe() {
				fmt.Fprintf(out, "%s %-26s %s\n", probeMarker(r.Selected, r.Available), r.Name, r.Reason)
			}

			fmt.Fprintln(out, "\n── Window ──────────────────────────────────────────")
			for _, r := range window.Probe() {
				fmt.Fprintf(out, "%s %-26s %s\n", probeMarker(r.Selected, r.Available), r.Name, r.Reason)
			}

			fmt.Fprintln(out, "\n── Input ───────────────────────────────────────────")
			for _, r := range input.Probe() {
				fmt.Fprintf(out, "%s %-26s %s\n", probeMarker(r.Selected, r.Available), r.Name, r.Reason)
			}

			fmt.Fprintln(out, "\n── Capability matrix ───────────────────────────────")
			switch kind {
			case compositor.KDE:
				fmt.Fprintln(out, "  screen capture   ✓  KWin.ScreenShot2, ext capture when advertised, portal fallback")
				fmt.Fprintln(out, "  window list      ✓  KWin scripting")
				fmt.Fprintln(out, "  window control   ✓  KWin scripting")
				fmt.Fprintln(out, "  input injection  ✓  /dev/uinput")
				fmt.Fprintln(out, "  pixel scanning   ✓")
			case compositor.Wlroots:
				fmt.Fprintln(out, "  screen capture   ✓  wlr-screencopy / ext-image-copy-capture")
				fmt.Fprintln(out, "  window list      ✓  wlr-foreign-toplevel")
				fmt.Fprintln(out, "  window control   ✓  wlr-foreign-toplevel")
				fmt.Fprintln(out, "  input injection  ✓  wl-virtual")
				fmt.Fprintln(out, "  pixel scanning   ✓")
			case compositor.GNOME:
				fmt.Fprintln(out, "  screen capture   ✓  gnome-shell screenshot (unsafe mode), portal fallback")
				fmt.Fprintln(out, "  window list      ✓  gnome-shell Eval (unsafe mode)")
				fmt.Fprintln(out, "  window control   ✓  gnome-shell Eval (unsafe mode)")
				fmt.Fprintln(out, "  input injection  ✓  /dev/uinput")
				fmt.Fprintln(out, "  clipboard        ✓  wl-copy/wl-paste")
				fmt.Fprintln(out, "  pixel scanning   ✓")
			case compositor.X11:
				fmt.Fprintln(out, "  screen capture   ✓  XGetImage")
				fmt.Fprintln(out, "  window list      ✓  EWMH")
				fmt.Fprintln(out, "  window control   ✓  EWMH")
				fmt.Fprintln(out, "  input injection  ✓  XTEST")
				fmt.Fprintln(out, "  pixel scanning   ✓")
			default:
				fmt.Fprintln(out, "  Unknown compositor — run inside a nested sway session.")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outputFlag, "output", "plain", "plain|json")
	return cmd
}

func probeMarker(selected, available bool) string {
	switch {
	case selected:
		return "  [✓]"
	case available:
		return "  [·]"
	default:
		return "  [ ]"
	}
}

// ── session ───────────────────────────────────────────────────────────────────────────

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Session diagnostics and utilities",
	}

	typeCmd := &cobra.Command{
		Use:   "type",
		Short: "Print whether the current session is nested or host",
		Run: func(cmd *cobra.Command, _ []string) {
			kind, details := perfuncted.DetectSession()
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "session: %s\n", kind)
			for k, v := range details {
				fmt.Fprintf(out, "  %s: %s\n", k, v)
			}
		},
	}

	check := &cobra.Command{
		Use:   "check",
		Short: "Check if the current runtime environment is ready for automation",
		Run: func(cmd *cobra.Command, _ []string) {
			out := cmd.OutOrStdout()
			fmt.Fprintln(out, "── Environment Variable Checks ──────────────────")

			xdg := os.Getenv("XDG_RUNTIME_DIR")
			if xdg == "" {
				fmt.Fprintln(out, "  [✗] XDG_RUNTIME_DIR is not set")
			} else if info, err := os.Stat(xdg); err == nil && info.IsDir() {
				fmt.Fprintf(out, "  [✓] XDG_RUNTIME_DIR=%s\n", xdg)
			} else {
				fmt.Fprintf(out, "  [✗] XDG_RUNTIME_DIR=%s (not found)\n", xdg)
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
						fmt.Fprintf(out, "  [✗] WAYLAND_DISPLAY=%s (socket missing at %s)\n", wd, sock)
					}
				}
			}

			if addr := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); addr != "" {
				fmt.Fprintf(out, "  [✓] DBUS_SESSION_BUS_ADDRESS=%s\n", addr)
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

			sess, err := perfuncted.StartSession(cfg)
			if err != nil {
				return err
			}

			fmt.Printf("export XDG_RUNTIME_DIR=%s\n", sess.XDGRuntimeDir())
			fmt.Printf("export WAYLAND_DISPLAY=%s\n", sess.WaylandDisplay())
			fmt.Printf("export DBUS_SESSION_BUS_ADDRESS=%s\n", sess.DBusAddress())
			fmt.Fprintf(os.Stderr, "session: running (XDG=%s, pid sway=%d)\n", sess.XDGRuntimeDir(), sess.SwayPID())
			fmt.Fprintf(os.Stderr, "session: press Ctrl+C to stop\n")

			<-cmd.Context().Done()

			fmt.Fprintf(os.Stderr, "\nsession: stopping...\n")
			sess.Stop()
			fmt.Fprintf(os.Stderr, "session: stopped\n")
			return nil
		},
	}
	startCmd.Flags().IntVar(&startResX, "res-x", 1024, "horizontal resolution")
	startCmd.Flags().IntVar(&startResY, "res-y", 768, "vertical resolution")
	startCmd.Flags().StringVar(&startSwayConf, "sway-config", "", "path to custom sway config (default: embedded)")

	cmd.AddCommand(typeCmd, check, startCmd)
	return cmd
}
