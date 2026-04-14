// cmd/pf is a thin CLI wrapper over the perfuncted library.
// Each command group maps to a bundle (Screen, Input, Window); subcommands
// map to bundle methods. All backend setup — including nested-session detection
// — flows through perfuncted.New(), keeping the CLI and library in sync.
//
// Usage examples:
//
//	pf screen grab --rect 0,0,100,100 --out /tmp/shot.png
//	pf screen hash --rect 0,0,100,100
//	pf screen pixel --x 960 --y 540
//	pf input move --x 500 --y 300
//	pf input click --x 500 --y 300
//	pf input type "hello world"
//	pf input key ctrl+s
//	pf window list
//	pf window activate "kwrite"
//	pf window active
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/session"
	"github.com/nskaggs/perfuncted/window"
)

func main() {
	var (
		nested     bool
		maxX, maxY int32
	)

	root := &cobra.Command{
		Use:               "pf",
		Short:             "perfuncted — screen automation CLI",
		DisableAutoGenTag: true,
	}
	root.PersistentFlags().BoolVar(&nested, "nested", false,
		"auto-detect and connect to a nested Wayland session in /tmp")
	root.PersistentFlags().Int32Var(&maxX, "max-x", 0,
		"input coordinate space width (default 1920)")
	root.PersistentFlags().Int32Var(&maxY, "max-y", 0,
		"input coordinate space height (default 1080)")

	// openPF is the single gateway to all backends.
	openPF := func() (*perfuncted.Perfuncted, error) {
		return perfuncted.New(perfuncted.Options{
			Nested: nested,
			MaxX:   maxX,
			MaxY:   maxY,
		})
	}

	root.AddCommand(
		screenCmd(openPF),
		inputCmd(openPF),
		windowCmd(openPF),
		findCmd(openPF),
		clipboardCmd(openPF),
		infoCmd(),
		sessionCmd(),
		docsCmd(root),
	)
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── docs ────────────────────────────────────────────────────────────────────────────

func docsCmd(root *cobra.Command) *cobra.Command {
	var dirFlag, readmeFlag string
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate markdown documentation for the CLI",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := os.MkdirAll(dirFlag, 0755); err != nil {
				return err
			}
			if err := doc.GenMarkdownTree(root, dirFlag); err != nil {
				return err
			}
			fmt.Printf("Documentation generated in %s\n", dirFlag)
			if readmeFlag != "" {
				if err := updateReadmeCLI(root, readmeFlag); err != nil {
					return fmt.Errorf("update readme: %w", err)
				}
				fmt.Printf("README CLI section updated in %s\n", readmeFlag)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&dirFlag, "dir", "d", "./docs-cli", "directory to write markdown files")
	cmd.Flags().StringVar(&readmeFlag, "readme", "", "path to README.md whose CLI section to regenerate")
	return cmd
}

// updateReadmeCLI rewrites the block between <!-- pf-cli-start --> and
// <!-- pf-cli-end --> in the given README with a compact command listing
// derived from the live cobra command tree.
func updateReadmeCLI(root *cobra.Command, readmePath string) error {
	skip := map[string]bool{"completion": true, "help": true, "docs": true}

	var buf bytes.Buffer
	buf.WriteString("```\n")
	first := true
	for _, grp := range root.Commands() {
		if skip[grp.Name()] || grp.Hidden {
			continue
		}
		if !grp.HasSubCommands() {
			fmt.Fprintf(&buf, "%-40s# %s\n", "pf "+grp.Name()+"  ", grp.Short)
			first = false
			continue
		}
		if !first {
			buf.WriteByte('\n')
		}
		first = false
		for _, sub := range grp.Commands() {
			if sub.Hidden || sub.Name() == "help" {
				continue
			}
			fmt.Fprintf(&buf, "%-40s# %s\n", "pf "+grp.Name()+" "+sub.Name()+"  ", sub.Short)
		}
	}
	buf.WriteString("```\n")

	data, err := os.ReadFile(readmePath)
	if err != nil {
		return err
	}
	const startMarker = "<!-- pf-cli-start -->\n"
	const endMarker = "<!-- pf-cli-end -->"
	start := bytes.Index(data, []byte(startMarker))
	end := bytes.Index(data, []byte(endMarker))
	if start < 0 || end < 0 || end <= start {
		return fmt.Errorf("README missing <!-- pf-cli-start --> / <!-- pf-cli-end --> sentinels")
	}
	var out bytes.Buffer
	out.Write(data[:start+len(startMarker)])
	out.Write(buf.Bytes())
	out.Write(data[end:])
	return os.WriteFile(readmePath, out.Bytes(), 0644)
}

// ── info ────────────────────────────────────────────────────────────────────────────

func infoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Probe and display supported backends for this environment",
		Run: func(_ *cobra.Command, _ []string) {
			kind := compositor.Detect()

			fmt.Println("── Environment ────────────────────────────────────")
			fmt.Printf("  Compositor:       %s\n", kind)
			if d := os.Getenv("WAYLAND_DISPLAY"); d != "" {
				fmt.Printf("  WAYLAND_DISPLAY:  %s\n", d)
			}
			if d := os.Getenv("DISPLAY"); d != "" {
				fmt.Printf("  DISPLAY:          %s\n", d)
			}
			if d := os.Getenv("XDG_CURRENT_DESKTOP"); d != "" {
				fmt.Printf("  XDG_CURRENT_DESKTOP: %s\n", d)
			}

			fmt.Println("\n── Screen ──────────────────────────────────────────")
			for _, r := range screen.Probe() {
				fmt.Printf("%s %-26s %s\n", probeMarker(r.Selected, r.Available), r.Name, r.Reason)
			}

			fmt.Println("\n── Window ──────────────────────────────────────────")
			for _, r := range window.Probe() {
				fmt.Printf("%s %-26s %s\n", probeMarker(r.Selected, r.Available), r.Name, r.Reason)
			}

			fmt.Println("\n── Input ───────────────────────────────────────────")
			for _, r := range input.Probe() {
				fmt.Printf("%s %-26s %s\n", probeMarker(r.Selected, r.Available), r.Name, r.Reason)
			}

			fmt.Println("\n── Capability matrix ───────────────────────────────")
			switch kind {
			case compositor.KDE:
				fmt.Println("  screen capture   ✓  KWin.ScreenShot2, portal fallback")
				fmt.Println("  window list      ✓  KWin scripting")
				fmt.Println("  window control   ✓  KWin scripting")
				fmt.Println("  input injection  ✓  /dev/uinput")
				fmt.Println("  pixel scanning   ✓")
			case compositor.Wlroots:
				fmt.Println("  screen capture   ✓  wlr-screencopy / ext-image-copy-capture")
				fmt.Println("  window list      ✓  wlr-foreign-toplevel")
				fmt.Println("  window control   ✓  wlr-foreign-toplevel")
				fmt.Println("  input injection  ✓  wl-virtual")
				fmt.Println("  pixel scanning   ✓")
			case compositor.GNOME:
				fmt.Println("  screen capture   ~  portal only (may require consent)")
				fmt.Println("  window list      ✓  gnome-shell Eval")
				fmt.Println("  window control   ✓  gnome-shell Eval")
				fmt.Println("  input injection  ✓  /dev/uinput")
				fmt.Println("  clipboard        ✓  wl-copy/wl-paste")
				fmt.Println("  pixel scanning   ✓")
			case compositor.X11:
				fmt.Println("  screen capture   ✓  XGetImage")
				fmt.Println("  window list      ✓  EWMH")
				fmt.Println("  window control   ✓  EWMH")
				fmt.Println("  input injection  ✓  XTEST")
				fmt.Println("  pixel scanning   ✓")
			default:
				fmt.Println("  Unknown compositor — run inside a nested sway session.")
			}
		},
	}
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
		Run: func(_ *cobra.Command, _ []string) {
			kind, details := perfuncted.DetectSession()
			fmt.Printf("session: %s\n", kind)
			for k, v := range details {
				fmt.Printf("  %s: %s\n", k, v)
			}
		},
	}

	check := &cobra.Command{
		Use:   "check",
		Short: "Check if the current runtime environment is ready for automation",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println("── Environment Variable Checks ──────────────────")

			xdg := os.Getenv("XDG_RUNTIME_DIR")
			if xdg == "" {
				fmt.Println("  [✗] XDG_RUNTIME_DIR is not set")
			} else if info, err := os.Stat(xdg); err == nil && info.IsDir() {
				fmt.Printf("  [✓] XDG_RUNTIME_DIR=%s\n", xdg)
			} else {
				fmt.Printf("  [✗] XDG_RUNTIME_DIR=%s (not found)\n", xdg)
			}

			wd := os.Getenv("WAYLAND_DISPLAY")
			if wd == "" {
				fmt.Println("  [✗] WAYLAND_DISPLAY is not set")
			} else {
				sock := filepath.Join(xdg, wd)
				if info, err := os.Stat(sock); err == nil && info.Mode()&os.ModeSocket != 0 {
					fmt.Printf("  [✓] WAYLAND_DISPLAY=%s (socket reachable)\n", wd)
				} else {
					fmt.Printf("  [✗] WAYLAND_DISPLAY=%s (socket missing at %s)\n", wd, sock)
				}
			}

			if addr := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); addr != "" {
				fmt.Printf("  [✓] DBUS_SESSION_BUS_ADDRESS=%s\n", addr)
			} else {
				fmt.Println("  [✗] DBUS_SESSION_BUS_ADDRESS is not set")
			}

			fmt.Println("\n── System Resource Checks ────────────────────────")
			if info, err := os.Stat("/dev/uinput"); err == nil {
				fmt.Printf("  [✓] /dev/uinput accessible (mode %v)\n", info.Mode())
			} else {
				fmt.Printf("  [✗] /dev/uinput not accessible: %v\n", err)
			}

			fmt.Println("\n  Run `pf info` for the full backend capability matrix.")
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
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg := session.Config{}
			if startResX > 0 && startResY > 0 {
				cfg.Resolution = image.Pt(startResX, startResY)
			}
			cfg.SwayConfigPath = startSwayConf

			sess, err := session.Start(cfg)
			if err != nil {
				return err
			}

			fmt.Printf("export XDG_RUNTIME_DIR=%s\n", sess.XDGRuntimeDir())
			fmt.Printf("export WAYLAND_DISPLAY=%s\n", sess.WaylandDisplay())
			fmt.Printf("export DBUS_SESSION_BUS_ADDRESS=%s\n", sess.DBusAddress())
			fmt.Fprintf(os.Stderr, "session: running (XDG=%s, pid sway=%d)\n", sess.XDGRuntimeDir(), os.Getpid())
			fmt.Fprintf(os.Stderr, "session: press Ctrl+C to stop\n")

			// Block until interrupted.
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
			<-sig

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

// ── screen ────────────────────────────────────────────────────────────────────────────

func screenCmd(openPF func() (*perfuncted.Perfuncted, error)) *cobra.Command {
	cmd := &cobra.Command{Use: "screen", Short: "Screen capture operations"}

	var rectFlag, outFlag string

	grab := &cobra.Command{
		Use:   "grab",
		Short: "Capture a screen region and save as PNG",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			img, err := pf.Screen.Grab(r)
			if err != nil {
				return err
			}
			out := outFlag
			if out == "" {
				out = "/tmp/pf-grab.png"
			}
			f, err := os.Create(out)
			if err != nil {
				return err
			}
			defer f.Close()
			if err := png.Encode(f, img); err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	}
	grab.Flags().StringVar(&rectFlag, "rect", "0,0,1920,1080", "x0,y0,x1,y1")
	grab.Flags().StringVar(&outFlag, "out", "", "output path (default /tmp/pf-grab.png)")

	checksum := &cobra.Command{
		Use:   "hash",
		Short: "Print the CRC32 pixel hash of a screen region",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			h, err := pf.Screen.GrabHash(r)
			if err != nil {
				return err
			}
			fmt.Println(h)
			return nil
		},
	}
	checksum.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")

	var px, py int
	pixel := &cobra.Command{
		Use:   "pixel",
		Short: "Print the RGB colour of a single pixel",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			c, err := find.FirstPixel(pf.Screen.Screenshotter, image.Rect(px, py, px+1, py+1))
			if err != nil {
				return err
			}
			fmt.Printf("R=%d G=%d B=%d\n", c.R, c.G, c.B)
			return nil
		},
	}
	pixel.Flags().IntVar(&px, "x", 0, "x coordinate")
	pixel.Flags().IntVar(&py, "y", 0, "y coordinate")

	cmd.AddCommand(grab, checksum, pixel)

	var watchRectFlag, watchPollFlag, watchDurFlag string
	watch := &cobra.Command{
		Use:   "watch",
		Short: "Continuously print hash changes in a screen region",
		Long: `Polls a screen region and prints a timestamped line whenever the pixel hash
changes. Useful for tuning poll intervals, spotting oscillating regions, and
understanding which parts of the screen change during an action.

Output format:
  <timestamp>  <hash>  <label>

Runs until --duration expires or Ctrl+C.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(watchRectFlag)
			if err != nil {
				return err
			}
			poll, err := parseDuration(watchPollFlag, 100*time.Millisecond)
			if err != nil {
				return err
			}
			dur, err := parseDuration(watchDurFlag, 0)
			if err != nil {
				return err
			}

			ctx := context.Background()
			var cancel context.CancelFunc
			if dur > 0 {
				ctx, cancel = context.WithTimeout(ctx, dur)
				defer cancel()
			}

			// Cancel on Ctrl+C as well.
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				select {
				case <-sigCh:
					if cancel != nil {
						cancel()
					} else {
						os.Exit(0)
					}
				case <-ctx.Done():
				}
			}()

			var last uint32
			first := true
			start := time.Now()
			streak := 0
			for {
				select {
				case <-ctx.Done():
					return nil
				default:
				}
				h, err := pf.Screen.GrabHash(r)
				if err != nil {
					return err
				}
				ts := time.Now().Format("15:04:05.000")
				if first {
					fmt.Printf("%s  0x%08x  (initial)\n", ts, h)
					last = h
					first = false
					streak = 1
				} else if h != last {
					elapsed := time.Since(start)
					fmt.Printf("%s  0x%08x  (+%s after %d stable)\n", ts, h, elapsed.Round(time.Millisecond), streak)
					last = h
					start = time.Now()
					streak = 1
				} else {
					streak++
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(poll):
				}
			}
		},
	}
	watch.Flags().StringVar(&watchRectFlag, "rect", "0,0,1920,1080", "x0,y0,x1,y1 region to monitor")
	watch.Flags().StringVar(&watchPollFlag, "poll", "100ms", "poll interval")
	watch.Flags().StringVar(&watchDurFlag, "duration", "", "stop after this duration (e.g. 10s); default runs until Ctrl+C")
	cmd.AddCommand(watch)

	resolution := &cobra.Command{
		Use:   "resolution",
		Short: "Print the screen resolution",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			w, h, err := pf.Screen.Resolution()
			if err != nil {
				return err
			}
			fmt.Printf("%dx%d\n", w, h)
			return nil
		},
	}

	cmd.AddCommand(resolution)
	return cmd
}

// ── input ────────────────────────────────────────────────────────────────────────────

func inputCmd(openPF func() (*perfuncted.Perfuncted, error)) *cobra.Command {
	cmd := &cobra.Command{Use: "input", Short: "Mouse and keyboard injection"}

	var mx, my, button int

	move := &cobra.Command{
		Use:   "move",
		Short: "Move mouse to absolute coordinates",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.MouseMove(mx, my); err != nil {
				return err
			}
			fmt.Printf("moved to %d,%d\n", mx, my)
			return nil
		},
	}
	move.Flags().IntVar(&mx, "x", 0, "x coordinate")
	move.Flags().IntVar(&my, "y", 0, "y coordinate")

	click := &cobra.Command{
		Use:   "click",
		Short: "Click a mouse button at coordinates",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.MouseClick(mx, my, button); err != nil {
				return err
			}
			fmt.Printf("clicked button %d at %d,%d\n", button, mx, my)
			return nil
		},
	}
	click.Flags().IntVar(&mx, "x", 0, "x coordinate")
	click.Flags().IntVar(&my, "y", 0, "y coordinate")
	click.Flags().IntVar(&button, "button", 1, "1=left 2=middle 3=right")

	doubleClick := &cobra.Command{
		Use:   "double-click",
		Short: "Double-click at coordinates",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.DoubleClick(mx, my); err != nil {
				return err
			}
			fmt.Printf("double-clicked at %d,%d\n", mx, my)
			return nil
		},
	}
	doubleClick.Flags().IntVar(&mx, "x", 0, "x coordinate")
	doubleClick.Flags().IntVar(&my, "y", 0, "y coordinate")

	var x1, y1, x2, y2 int
	drag := &cobra.Command{
		Use:   "drag-and-drop",
		Short: "Drag from one coordinate to another (press, move, release)",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.DragAndDrop(x1, y1, x2, y2); err != nil {
				return err
			}
			fmt.Printf("dragged %d,%d to %d,%d\n", x1, y1, x2, y2)
			return nil
		},
	}
	drag.Flags().IntVar(&x1, "x1", 0, "start x")
	drag.Flags().IntVar(&y1, "y1", 0, "start y")
	drag.Flags().IntVar(&x2, "x2", 0, "end x")
	drag.Flags().IntVar(&y2, "y2", 0, "end y")

	var crRect string
	clickCenter := &cobra.Command{
		Use:   "click-center",
		Short: "Click the center of a rectangle",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(crRect)
			if err != nil {
				return err
			}
			if err := pf.Input.ClickCenter(r); err != nil {
				return err
			}
			fmt.Printf("clicked center %d,%d\n", r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2)
			return nil
		},
	}
	clickCenter.Flags().StringVar(&crRect, "rect", "0,0,100,100", "x0,y0,x1,y1")

	typeCmd := &cobra.Command{
		Use:   "type <text>",
		Short: "Type a string as keyboard events",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			return pf.Input.Type(args[0])
		},
	}

	key := &cobra.Command{
		Use:   "key <combo>",
		Short: "Send a key or key combination (e.g. ctrl+s, return, escape)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			return pf.Input.PressCombo(args[0])
		},
	}

	keydown := &cobra.Command{
		Use:   "keydown <key>",
		Short: "Press and hold a key",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.KeyDown(args[0]); err != nil {
				return err
			}
			fmt.Printf("keydown %s\n", args[0])
			return nil
		},
	}

	keyup := &cobra.Command{
		Use:   "keyup <key>",
		Short: "Release a held key",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.KeyUp(args[0]); err != nil {
				return err
			}
			fmt.Printf("keyup %s\n", args[0])
			return nil
		},
	}

	var mdx, mdy, mdButton int
	mousedown := &cobra.Command{
		Use:   "mousedown",
		Short: "Press a mouse button (optional coords)",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if mdx != -1 && mdy != -1 {
				if err := pf.Input.MouseMove(mdx, mdy); err != nil {
					return err
				}
			}
			if err := pf.Input.MouseDown(mdButton); err != nil {
				return err
			}
			fmt.Printf("mousedown button %d at %d,%d\n", mdButton, mdx, mdy)
			return nil
		},
	}
	mousedown.Flags().IntVar(&mdx, "x", -1, "x coordinate (optional)")
	mousedown.Flags().IntVar(&mdy, "y", -1, "y coordinate (optional)")
	mousedown.Flags().IntVar(&mdButton, "button", 1, "button number")

	var mux, muy, muButton int
	mouseup := &cobra.Command{
		Use:   "mouseup",
		Short: "Release a mouse button (optional coords)",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if mux != -1 && muy != -1 {
				if err := pf.Input.MouseMove(mux, muy); err != nil {
					return err
				}
			}
			if err := pf.Input.MouseUp(muButton); err != nil {
				return err
			}
			fmt.Printf("mouseup button %d at %d,%d\n", muButton, mux, muy)
			return nil
		},
	}
	mouseup.Flags().IntVar(&mux, "x", -1, "x coordinate (optional)")
	mouseup.Flags().IntVar(&muy, "y", -1, "y coordinate (optional)")
	mouseup.Flags().IntVar(&muButton, "button", 1, "button number")

	cmd.AddCommand(move, click, doubleClick, drag, clickCenter,
		typeCmd, key, keydown, keyup, mousedown, mouseup, scrollCmd(openPF))
	return cmd
}

// ── scroll ─────────────────────────────────────────────────────────────────────────────

func scrollCmd(openPF func() (*perfuncted.Perfuncted, error)) *cobra.Command {
	cmd := &cobra.Command{Use: "scroll", Short: "Scroll the mouse wheel"}

	var clicks int

	up := &cobra.Command{
		Use:   "up",
		Short: "Scroll up by N clicks",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.ScrollUp(clicks); err != nil {
				return err
			}
			fmt.Printf("scrolled up %d\n", clicks)
			return nil
		},
	}
	up.Flags().IntVar(&clicks, "clicks", 3, "number of scroll clicks")

	down := &cobra.Command{
		Use:   "down",
		Short: "Scroll down by N clicks",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.ScrollDown(clicks); err != nil {
				return err
			}
			fmt.Printf("scrolled down %d\n", clicks)
			return nil
		},
	}
	down.Flags().IntVar(&clicks, "clicks", 3, "number of scroll clicks")

	cmd.AddCommand(up, down)

	left := &cobra.Command{
		Use:   "left",
		Short: "Scroll left by N clicks",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.ScrollLeft(clicks); err != nil {
				return err
			}
			fmt.Printf("scrolled left %d\n", clicks)
			return nil
		},
	}
	left.Flags().IntVar(&clicks, "clicks", 3, "number of scroll clicks")

	right := &cobra.Command{
		Use:   "right",
		Short: "Scroll right by N clicks",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.ScrollRight(clicks); err != nil {
				return err
			}
			fmt.Printf("scrolled right %d\n", clicks)
			return nil
		},
	}
	right.Flags().IntVar(&clicks, "clicks", 3, "number of scroll clicks")

	cmd.AddCommand(left, right)
	return cmd
}

// ── window ────────────────────────────────────────────────────────────────────────────

func windowCmd(openPF func() (*perfuncted.Perfuncted, error)) *cobra.Command {
	cmd := &cobra.Command{Use: "window", Short: "Window management"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List all visible windows",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			wins, err := pf.Window.List()
			if err != nil {
				return err
			}
			for _, w := range wins {
				fmt.Printf("0x%x\t%s\n", w.ID, w.Title)
			}
			return nil
		},
	}

	activate := &cobra.Command{
		Use:   "activate <pattern>",
		Short: "Bring a window to the foreground by title substring (case-insensitive)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.Activate(args[0]); err != nil {
				return err
			}
			fmt.Printf("activated: %s\n", args[0])
			return nil
		},
	}

	active := &cobra.Command{
		Use:   "active",
		Short: "Print the title of the currently focused window",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			t, err := pf.Window.ActiveTitle()
			if err != nil {
				return err
			}
			fmt.Println(t)
			return nil
		},
	}

	var mvTitle string
	var mvX, mvY int
	move := &cobra.Command{
		Use:   "move",
		Short: "Move a window to absolute screen coordinates",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.Move(mvTitle, mvX, mvY); err != nil {
				return err
			}
			fmt.Printf("moved %q to %d,%d\n", mvTitle, mvX, mvY)
			return nil
		},
	}
	move.Flags().StringVar(&mvTitle, "title", "", "window title substring (required)")
	move.Flags().IntVar(&mvX, "x", 0, "x coordinate")
	move.Flags().IntVar(&mvY, "y", 0, "y coordinate")
	_ = move.MarkFlagRequired("title")

	var rsTitle string
	var rsW, rsH int
	resize := &cobra.Command{
		Use:   "resize",
		Short: "Resize a window",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.Resize(rsTitle, rsW, rsH); err != nil {
				return err
			}
			fmt.Printf("resized %q to %dx%d\n", rsTitle, rsW, rsH)
			return nil
		},
	}
	resize.Flags().StringVar(&rsTitle, "title", "", "window title substring (required)")
	resize.Flags().IntVar(&rsW, "w", 800, "width in pixels")
	resize.Flags().IntVar(&rsH, "h", 600, "height in pixels")
	_ = resize.MarkFlagRequired("title")

	cmd.AddCommand(list, activate, active, move, resize)

	closeWin := &cobra.Command{
		Use:   "close <title>",
		Short: "Close a window by title",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.CloseWindow(args[0]); err != nil {
				return err
			}
			fmt.Printf("closed: %s\n", args[0])
			return nil
		},
	}

	minimize := &cobra.Command{
		Use:   "minimize <title>",
		Short: "Minimize a window by title",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.Minimize(args[0]); err != nil {
				return err
			}
			fmt.Printf("minimized: %s\n", args[0])
			return nil
		},
	}

	maximize := &cobra.Command{
		Use:   "maximize <title>",
		Short: "Maximize a window by title",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.Maximize(args[0]); err != nil {
				return err
			}
			fmt.Printf("maximized: %s\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(closeWin, minimize, maximize)
	return cmd
}

// ── find ──────────────────────────────────────────────────────────────────────────────

func findCmd(openPF func() (*perfuncted.Perfuncted, error)) *cobra.Command {
	cmd := &cobra.Command{Use: "find", Short: "Pixel scanning and wait utilities"}

	var (
		rectFlag       string
		rectsFlag      string
		wantsFlag      string
		hashFlag       string
		pollFlag       string
		timeoutFlag    string
		captureInitial bool
		stableCount    int
	)

	waitFor := &cobra.Command{
		Use:   "wait-for",
		Short: "Wait until a region's pixel hash equals the provided hash",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			want, err := parseHash(hashFlag)
			if err != nil {
				return err
			}
			poll, err := parseDuration(pollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(timeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			h, err := pf.Screen.WaitFor(ctx, r, want, poll)
			if err != nil {
				return err
			}
			fmt.Printf("%08x\n", h)
			return nil
		},
	}
	waitFor.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitFor.Flags().StringVar(&hashFlag, "hash", "", "target hash (decimal or 0xhex)")
	waitFor.Flags().StringVar(&pollFlag, "poll", "50ms", "poll interval")
	waitFor.Flags().StringVar(&timeoutFlag, "timeout", "5s", "timeout duration")
	_ = waitFor.MarkFlagRequired("hash")

	waitForChange := &cobra.Command{
		Use:   "wait-for-change",
		Short: "Wait until a region's pixel hash changes from an initial value",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			var initial uint32
			if captureInitial {
				if initial, err = pf.Screen.GrabHash(r); err != nil {
					return err
				}
			} else {
				if initial, err = parseHash(hashFlag); err != nil {
					return err
				}
			}
			poll, err := parseDuration(pollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(timeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			h, err := pf.Screen.WaitForChange(ctx, r, initial, poll)
			if err != nil {
				return err
			}
			fmt.Printf("%08x\n", h)
			return nil
		},
	}
	waitForChange.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitForChange.Flags().StringVar(&hashFlag, "initial", "", "initial hash (decimal or 0xhex)")
	waitForChange.Flags().BoolVar(&captureInitial, "capture-initial", false,
		"capture current region hash and wait for it to change")
	waitForChange.Flags().StringVar(&pollFlag, "poll", "50ms", "poll interval")
	waitForChange.Flags().StringVar(&timeoutFlag, "timeout", "5s", "timeout duration")
	waitForChange.MarkFlagsMutuallyExclusive("initial", "capture-initial")
	waitForChange.MarkFlagsOneRequired("initial", "capture-initial")

	waitForNoChange := &cobra.Command{
		Use:   "wait-for-no-change",
		Short: "Wait until a region's pixel hash is stable for N consecutive samples",
		Long: `Polls a screen region until its pixel hash is unchanged for --stable consecutive
samples. Pairs with wait-for-change: use wait-for-change to detect when something
starts (e.g. navigation begins), then wait-for-no-change to detect when it finishes.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			poll, err := parseDuration(pollFlag, 200*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(timeoutFlag, 30*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			h, err := pf.Screen.WaitForNoChange(ctx, r, stableCount, poll)
			if err != nil {
				return err
			}
			fmt.Printf("%08x\n", h)
			return nil
		},
	}
	waitForNoChange.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitForNoChange.Flags().IntVar(&stableCount, "stable", 5,
		"consecutive identical samples required")
	waitForNoChange.Flags().StringVar(&pollFlag, "poll", "200ms", "poll interval")
	waitForNoChange.Flags().StringVar(&timeoutFlag, "timeout", "30s", "timeout duration")

	scanFor := &cobra.Command{
		Use:   "scan-for",
		Short: "Scan multiple regions until one matches its expected hash",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			rects, err := parseRects(rectsFlag)
			if err != nil {
				return err
			}
			wants, err := parseWantHashes(wantsFlag)
			if err != nil {
				return err
			}
			if len(rects) != len(wants) {
				return fmt.Errorf("len(rects)=%d != len(wants)=%d", len(rects), len(wants))
			}
			poll, err := parseDuration(pollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(timeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			res, err := pf.Screen.ScanFor(ctx, rects, wants, poll)
			if err != nil {
				return err
			}
			fmt.Printf("match %v -> %08x\n", res.Rect, res.Hash)
			return nil
		},
	}
	scanFor.Flags().StringVar(&rectsFlag, "rects", "", "semicolon-separated rects: x0,y0,x1,y1;...")
	scanFor.Flags().StringVar(&wantsFlag, "wants", "", "comma-separated expected hashes")
	scanFor.Flags().StringVar(&pollFlag, "poll", "50ms", "poll interval")
	scanFor.Flags().StringVar(&timeoutFlag, "timeout", "5s", "timeout duration")
	_ = scanFor.MarkFlagRequired("rects")
	_ = scanFor.MarkFlagRequired("wants")

	var locateRect, locateRef string
	locate := &cobra.Command{
		Use:   "locate",
		Short: "Find a reference PNG image within a screen region",
		Long:  `Scans searchArea for an exact pixel match of the reference image and prints the bounding rectangle of the first match.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(locateRect)
			if err != nil {
				return err
			}
			ref, err := loadPNG(locateRef)
			if err != nil {
				return err
			}
			found, err := pf.Screen.LocateExact(r, ref)
			if err != nil {
				return err
			}
			fmt.Printf("%d,%d,%d,%d\n", found.Min.X, found.Min.Y, found.Max.X, found.Max.Y)
			return nil
		},
	}
	locate.Flags().StringVar(&locateRect, "rect", "0,0,1920,1080", "search area x0,y0,x1,y1")
	locate.Flags().StringVar(&locateRef, "ref", "", "path to reference PNG image")
	_ = locate.MarkFlagRequired("ref")

	cmd.AddCommand(waitFor, waitForChange, waitForNoChange, scanFor, locate)

	var colorRectFlag, colorTargetFlag string
	var colorTolerance int
	findColor := &cobra.Command{
		Use:   "color",
		Short: "Find the first pixel matching a colour within tolerance",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(colorRectFlag)
			if err != nil {
				return err
			}
			c, err := parseColor(colorTargetFlag)
			if err != nil {
				return err
			}
			pt, err := pf.Screen.FindColor(r, c, colorTolerance)
			if err != nil {
				return err
			}
			fmt.Printf("%d,%d\n", pt.X, pt.Y)
			return nil
		},
	}
	findColor.Flags().StringVar(&colorRectFlag, "rect", "0,0,1920,1080", "search area x0,y0,x1,y1")
	findColor.Flags().StringVar(&colorTargetFlag, "color", "", "target colour as RRGGBB hex (required)")
	findColor.Flags().IntVar(&colorTolerance, "tolerance", 0, "per-channel tolerance (0-255)")
	_ = findColor.MarkFlagRequired("color")

	var wlRefFlag, wlRectFlag, wlTimeoutFlag, wlPollFlag string
	waitLocate := &cobra.Command{
		Use:   "wait-locate",
		Short: "Poll until a reference image is found in the search area",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(wlRectFlag)
			if err != nil {
				return err
			}
			ref, err := loadPNG(wlRefFlag)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(wlTimeoutFlag, 10*time.Second)
			if err != nil {
				return err
			}
			poll, err := parseDuration(wlPollFlag, 200*time.Millisecond)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			rect, err := pf.Screen.WaitForLocate(ctx, r, ref, poll)
			if err != nil {
				return err
			}
			fmt.Printf("%d,%d,%d,%d\n", rect.Min.X, rect.Min.Y, rect.Max.X, rect.Max.Y)
			return nil
		},
	}
	waitLocate.Flags().StringVar(&wlRefFlag, "ref", "", "reference PNG image path (required)")
	waitLocate.Flags().StringVar(&wlRectFlag, "rect", "0,0,1920,1080", "search area x0,y0,x1,y1")
	waitLocate.Flags().StringVar(&wlTimeoutFlag, "timeout", "10s", "maximum wait time")
	waitLocate.Flags().StringVar(&wlPollFlag, "poll", "200ms", "poll interval")
	_ = waitLocate.MarkFlagRequired("ref")

	cmd.AddCommand(findColor, waitLocate)
	return cmd
}

// ── clipboard ───────────────────────────────────────────────────────────────────────────

func clipboardCmd(openPF func() (*perfuncted.Perfuncted, error)) *cobra.Command {
	cmd := &cobra.Command{Use: "clipboard", Short: "Clipboard operations"}

	get := &cobra.Command{
		Use:   "get",
		Short: "Print clipboard contents",
		RunE: func(_ *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if pf.Clipboard == nil {
				return fmt.Errorf("clipboard: not available")
			}
			s, err := pf.Clipboard.Get()
			if err != nil {
				return err
			}
			fmt.Print(s)
			return nil
		},
	}

	set := &cobra.Command{
		Use:   "set <text>",
		Short: "Set clipboard contents",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if pf.Clipboard == nil {
				return fmt.Errorf("clipboard: not available")
			}
			return pf.Clipboard.Set(args[0])
		},
	}

	cmd.AddCommand(get, set)
	return cmd
}

// ── helpers ───────────────────────────────────────────────────────────────────────────

func parseDuration(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %v", s, err)
	}
	return v, nil
}

func parseHash(s string) (uint32, error) {
	v, err := strconv.ParseUint(s, 0, 32) // handles 0x prefix + decimal
	if err == nil {
		return uint32(v), nil
	}
	v, err = strconv.ParseUint(s, 16, 32) // fallback for raw hex like "ab12cd"
	if err != nil {
		return 0, fmt.Errorf("invalid hash %q: %v", s, err)
	}
	return uint32(v), nil
}

func parseWantHashes(s string) ([]uint32, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		h, err := parseHash(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

func parseRects(s string) ([]image.Rectangle, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ";")
	out := make([]image.Rectangle, 0, len(parts))
	for _, p := range parts {
		r, err := parseRect(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func parseRect(s string) (image.Rectangle, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return image.Rectangle{}, fmt.Errorf("--rect must be x0,y0,x1,y1; got %q", s)
	}
	vals := make([]int, 4)
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return image.Rectangle{}, fmt.Errorf("--rect: invalid number %q", p)
		}
		vals[i] = v
	}
	return image.Rect(vals[0], vals[1], vals[2], vals[3]), nil
}

func loadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open reference image: %w", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode reference PNG: %w", err)
	}
	return img, nil
}

// parseColor parses a hex colour string like "ff0000" or "FF0000" into color.RGBA.
func parseColor(s string) (color.RGBA, error) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return color.RGBA{}, fmt.Errorf("--color must be 6-digit hex RRGGBB; got %q", s)
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("--color: invalid hex %q: %v", s, err)
	}
	return color.RGBA{R: b[0], G: b[1], B: b[2], A: 0xff}, nil
}
