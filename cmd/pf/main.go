// cmd/pf is a cobra CLI that exposes the perfuncted library for ad-hoc use.
// Each package maps to a command group; each method maps to a subcommand.
//
// Usage examples:
//
//	pf screen grab --rect 0,0,100,100 --out /tmp/shot.png
//	pf screen checksum --rect 0,0,100,100
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
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

func main() {
	root := &cobra.Command{
		Use:   "pf",
		Short: "perfuncted — screen automation CLI",
	}
	root.AddCommand(screenCmd(), inputCmd(), windowCmd(), infoCmd())
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── info ──────────────────────────────────────────────────────────────────────

func infoCmd() *cobra.Command {
	cmd := &cobra.Command{
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
				mark := "  [ ]"
				if r.Selected {
					mark = "  [✓]"
				} else if r.Available {
					mark = "  [·]"
				}
				fmt.Printf("%s %-26s %s\n", mark, r.Name, r.Reason)
			}

			fmt.Println("\n── Window ──────────────────────────────────────────")
			for _, r := range window.Probe() {
				mark := "  [ ]"
				if r.Selected {
					mark = "  [✓]"
				} else if r.Available {
					mark = "  [·]"
				}
				fmt.Printf("%s %-26s %s\n", mark, r.Name, r.Reason)
			}

			fmt.Println("\n── Input ───────────────────────────────────────────")
			for _, r := range input.Probe() {
				mark := "  [ ]"
				if r.Selected {
					mark = "  [✓]"
				} else if r.Available {
					mark = "  [·]"
				}
				fmt.Printf("%s %-26s %s\n", mark, r.Name, r.Reason)
			}

			fmt.Println("\n── Capability matrix ───────────────────────────────")
			switch kind {
			case compositor.KDE:
				fmt.Println("  screen capture   ✓  KWin.ScreenShot2, portal fallback (one-time consent)")
				fmt.Println("  window list      ✓  KWin scripting (workspace.windowList)")
				fmt.Println("  window control   ✓  KWin scripting (activate, geometry)")
				fmt.Println("  input injection  ✓  /dev/uinput (kernel-level, universal)")
				fmt.Println("  pixel scanning   ✓")
				fmt.Println("  global hotkeys   ✗  not yet implemented")
				fmt.Println("  virtual keyboard ✗  KDE does not expose zwp_virtual_keyboard_manager_v1")
			case compositor.Wlroots:
				fmt.Println("  screen capture   ✓  wlr-screencopy / ext-image-copy-capture")
				fmt.Println("  window list      ✓  wlr-foreign-toplevel")
				fmt.Println("  window control   ✓  wlr-foreign-toplevel (activate)")
				fmt.Println("  input injection  ✓  wl-virtual (zwlr_virtual_pointer + zwp_virtual_keyboard)")
				fmt.Println("  pixel scanning   ✓")
				fmt.Println("  global hotkeys   ✗  not yet implemented")
			case compositor.GNOME:
				fmt.Println("  screen capture   ~  portal only (one-time consent dialog)")
				fmt.Println("  window list      ✗  impossible on GNOME Wayland")
				fmt.Println("  window control   ✗  impossible on GNOME Wayland")
				fmt.Println("  input injection  ✓  /dev/uinput")
				fmt.Println("  pixel scanning   ✗  requires window list")
				fmt.Println("  global hotkeys   ✗  not exposed by GNOME")
				fmt.Println("")
				fmt.Println("  → Run inside a nested sway session for full automation:")
				fmt.Println("    sway --unsupported-gpu &")
				fmt.Println("    WAYLAND_DISPLAY=wayland-1 pf info")
			case compositor.X11:
				fmt.Println("  screen capture   ✓  XGetImage (X11/XWayland)")
				fmt.Println("  window list      ✓  EWMH (X11/XWayland)")
				fmt.Println("  window control   ✓  EWMH")
				fmt.Println("  input injection  ✓  XTEST")
				fmt.Println("  pixel scanning   ✓")
				fmt.Println("")
				fmt.Println("  Note: X11 is secondary target. Primary target is Wayland.")
			default:
				fmt.Println("  Unknown compositor — capabilities not determined.")
				fmt.Println("  Run inside a nested sway session for known-good automation.")
			}
		},
	}
	return cmd
}

// ── screen ────────────────────────────────────────────────────────────────────

func screenCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "screen", Short: "Screen capture operations"}

	var rectFlag, outFlag string

	grab := &cobra.Command{
		Use:   "grab",
		Short: "Capture a screen region and save as PNG",
		Run: func(_ *cobra.Command, _ []string) {
			sc := openScreen()
			defer sc.Close()
			r := parseRect(rectFlag)
			img, err := sc.Grab(r)
			die(err)
			out := outFlag
			if out == "" {
				out = "/tmp/pf-grab.png"
			}
			f, err := os.Create(out)
			die(err)
			defer f.Close()
			die(png.Encode(f, img))
			fmt.Println(out)
		},
	}
	grab.Flags().StringVar(&rectFlag, "rect", "0,0,1920,1080", "x0,y0,x1,y1")
	grab.Flags().StringVar(&outFlag, "out", "", "output path (default /tmp/pf-grab.png)")

	checksum := &cobra.Command{
		Use:   "checksum",
		Short: "Print the CRC32 pixel checksum of a screen region",
		Run: func(_ *cobra.Command, _ []string) {
			sc := openScreen()
			defer sc.Close()
			h, err := find.GrabHash(sc, parseRect(rectFlag), nil)
			die(err)
			fmt.Println(h)
		},
	}
	checksum.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")

	var px, py int
	pixel := &cobra.Command{
		Use:   "pixel",
		Short: "Print the RGB colour of a single pixel",
		Run: func(_ *cobra.Command, _ []string) {
			sc := openScreen()
			defer sc.Close()
			c, err := find.FirstPixel(sc, image.Rect(px, py, px+1, py+1))
			die(err)
			fmt.Printf("R=%d G=%d B=%d\n", c.R, c.G, c.B)
		},
	}
	pixel.Flags().IntVar(&px, "x", 0, "x coordinate")
	pixel.Flags().IntVar(&py, "y", 0, "y coordinate")

	cmd.AddCommand(grab, checksum, pixel)
	return cmd
}

// ── input ─────────────────────────────────────────────────────────────────────

func inputCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "input", Short: "Mouse and keyboard injection"}

	var mx, my, button int

	move := &cobra.Command{
		Use:   "move",
		Short: "Move mouse to absolute coordinates",
		Run: func(_ *cobra.Command, _ []string) {
			inp := openInput()
			defer inp.Close()
			die(inp.MouseMove(mx, my))
			fmt.Printf("moved to %d,%d\n", mx, my)
		},
	}
	move.Flags().IntVar(&mx, "x", 0, "x coordinate")
	move.Flags().IntVar(&my, "y", 0, "y coordinate")

	click := &cobra.Command{
		Use:   "click",
		Short: "Click a mouse button at coordinates",
		Run: func(_ *cobra.Command, _ []string) {
			inp := openInput()
			defer inp.Close()
			die(inp.MouseClick(mx, my, button))
			fmt.Printf("clicked button %d at %d,%d\n", button, mx, my)
		},
	}
	click.Flags().IntVar(&mx, "x", 0, "x coordinate")
	click.Flags().IntVar(&my, "y", 0, "y coordinate")
	click.Flags().IntVar(&button, "button", 1, "1=left 2=middle 3=right")

	typeCmd := &cobra.Command{
		Use:   "type <text>",
		Short: "Type a string as keyboard events",
		Args:  cobra.ExactArgs(1),
		Run: func(_ *cobra.Command, args []string) {
			inp := openInput()
			defer inp.Close()
			die(inp.Type(args[0]))
		},
	}

	// key accepts "ctrl+s" style combos: hold modifiers, tap the final key.
	key := &cobra.Command{
		Use:   "key <combo>",
		Short: "Send a key or key combination (e.g. ctrl+s, return, escape)",
		Args:  cobra.ExactArgs(1),
		Run: func(_ *cobra.Command, args []string) {
			inp := openInput()
			defer inp.Close()
			die(pressCombo(inp, args[0]))
		},
	}

	cmd.AddCommand(move, click, typeCmd, key)
	return cmd
}

// pressCombo handles "ctrl+s", "alt+f4", "return", etc.
func pressCombo(inp input.Inputter, combo string) error {
	parts := strings.Split(strings.ToLower(combo), "+")
	modifiers := parts[:len(parts)-1]
	final := parts[len(parts)-1]
	for _, m := range modifiers {
		if err := inp.KeyDown(m); err != nil {
			return err
		}
	}
	if err := inp.KeyTap(final); err != nil {
		// release already-held modifiers before returning error
		for _, m := range modifiers {
			inp.KeyUp(m) //nolint:errcheck
		}
		return err
	}
	for i := len(modifiers) - 1; i >= 0; i-- {
		if err := inp.KeyUp(modifiers[i]); err != nil {
			return err
		}
	}
	return nil
}

// ── window ────────────────────────────────────────────────────────────────────

func windowCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "window", Short: "Window management"}

	list := &cobra.Command{
		Use:   "list",
		Short: "List all visible windows",
		Run: func(_ *cobra.Command, _ []string) {
			wm := openWindow()
			defer wm.Close()
			wins, err := wm.List()
			die(err)
			for _, w := range wins {
				fmt.Printf("0x%x\t%s\n", w.ID, w.Title)
			}
		},
	}

	activate := &cobra.Command{
		Use:   "activate <title>",
		Short: "Bring a window to the foreground by title substring",
		Args:  cobra.ExactArgs(1),
		Run: func(_ *cobra.Command, args []string) {
			wm := openWindow()
			defer wm.Close()
			die(wm.Activate(args[0]))
			fmt.Printf("activated: %s\n", args[0])
		},
	}

	active := &cobra.Command{
		Use:   "active",
		Short: "Print the title of the currently focused window",
		Run: func(_ *cobra.Command, _ []string) {
			wm := openWindow()
			defer wm.Close()
			t, err := wm.ActiveTitle()
			die(err)
			fmt.Println(t)
		},
	}

	var (
		mvTitle  string
		mvX, mvY int
	)
	move := &cobra.Command{
		Use:   "move",
		Short: "Move a window to absolute screen coordinates",
		Run: func(_ *cobra.Command, _ []string) {
			wm := openWindow()
			defer wm.Close()
			die(wm.Move(mvTitle, mvX, mvY))
			fmt.Printf("moved %q to %d,%d\n", mvTitle, mvX, mvY)
		},
	}
	move.Flags().StringVar(&mvTitle, "title", "", "window title substring (required)")
	move.Flags().IntVar(&mvX, "x", 0, "x coordinate")
	move.Flags().IntVar(&mvY, "y", 0, "y coordinate")
	_ = move.MarkFlagRequired("title")

	var (
		rsTitle  string
		rsW, rsH int
	)
	resize := &cobra.Command{
		Use:   "resize",
		Short: "Resize a window",
		Run: func(_ *cobra.Command, _ []string) {
			wm := openWindow()
			defer wm.Close()
			die(wm.Resize(rsTitle, rsW, rsH))
			fmt.Printf("resized %q to %dx%d\n", rsTitle, rsW, rsH)
		},
	}
	resize.Flags().StringVar(&rsTitle, "title", "", "window title substring (required)")
	resize.Flags().IntVar(&rsW, "w", 800, "width in pixels")
	resize.Flags().IntVar(&rsH, "h", 600, "height in pixels")
	_ = resize.MarkFlagRequired("title")

	cmd.AddCommand(list, activate, active, move, resize)
	return cmd
}

// ── backend openers ───────────────────────────────────────────────────────────

// openScreen, openInput, openWindow open individual backends and exit on error.
// Unlike perfuncted.New(), which continues when some backends fail, the CLI
// uses fail-fast semantics: if a requested backend is unavailable, exit immediately.

func openScreen() screen.Screenshotter {
	sc, err := screen.Open()
	die(err)
	return sc
}

func openInput() input.Inputter {
	inp, err := input.Open(1920, 1080)
	die(err)
	return inp
}

func openWindow() window.Manager {
	wm, err := window.Open()
	die(err)
	return wm
}

// ── helpers ───────────────────────────────────────────────────────────────────

func parseRect(s string) image.Rectangle {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		log.Fatalf("--rect must be x0,y0,x1,y1; got %q", s)
	}
	vals := make([]int, 4)
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			log.Fatalf("--rect: invalid number %q", p)
		}
		vals[i] = v
	}
	return image.Rect(vals[0], vals[1], vals[2], vals[3])
}

func die(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
