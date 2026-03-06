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
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

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
	root.AddCommand(screenCmd(), inputCmd(), windowCmd(), findCmd(), infoCmd(), docsCmd(root))
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// ── info ──────────────────────────────────────────────────────────────────────

func docsCmd(root *cobra.Command) *cobra.Command {
	var dirFlag string
	cmd := &cobra.Command{
		Use:    "docs",
		Short:  "Generate markdown documentation for the CLI",
		Hidden: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := os.MkdirAll(dirFlag, 0755); err != nil {
				return err
			}
			if err := doc.GenMarkdownTree(root, dirFlag); err != nil {
				return err
			}
			fmt.Printf("Documentation generated in %s\n", dirFlag)
			return nil
		},
	}
	cmd.Flags().StringVarP(&dirFlag, "dir", "d", "./docs-cli", "Directory to write markdown files")
	return cmd
}

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
		RunE: func(_ *cobra.Command, _ []string) error {
			sc, err := screen.Open()
			if err != nil {
				return err
			}
			defer sc.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			img, err := sc.Grab(r)
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
		Use:   "checksum",
		Short: "Print the CRC32 pixel checksum of a screen region",
		RunE: func(_ *cobra.Command, _ []string) error {
			sc, err := screen.Open()
			if err != nil {
				return err
			}
			defer sc.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			h, err := find.GrabHash(sc, r, nil)
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
			sc, err := screen.Open()
			if err != nil {
				return err
			}
			defer sc.Close()
			c, err := find.FirstPixel(sc, image.Rect(px, py, px+1, py+1))
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
	return cmd
}

// ── input ─────────────────────────────────────────────────────────────────────

func inputCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "input", Short: "Mouse and keyboard injection"}

	var mx, my, button int

	move := &cobra.Command{
		Use:   "move",
		Short: "Move mouse to absolute coordinates",
		RunE: func(_ *cobra.Command, _ []string) error {
			inp, err := input.Open(1920, 1080)
			if err != nil {
				return err
			}
			defer inp.Close()
			if err := inp.MouseMove(mx, my); err != nil {
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
			inp, err := input.Open(1920, 1080)
			if err != nil {
				return err
			}
			defer inp.Close()
			if err := inp.MouseClick(mx, my, button); err != nil {
				return err
			}
			fmt.Printf("clicked button %d at %d,%d\n", button, mx, my)
			return nil
		},
	}
	click.Flags().IntVar(&mx, "x", 0, "x coordinate")
	click.Flags().IntVar(&my, "y", 0, "y coordinate")
	click.Flags().IntVar(&button, "button", 1, "1=left 2=middle 3=right")

	typeCmd := &cobra.Command{
		Use:   "type <text>",
		Short: "Type a string as keyboard events",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			inp, err := input.Open(1920, 1080)
			if err != nil {
				return err
			}
			defer inp.Close()
			return inp.Type(args[0])
		},
	}

	// key accepts "ctrl+s" style combos: hold modifiers, tap the final key.
	key := &cobra.Command{
		Use:   "key <combo>",
		Short: "Send a key or key combination (e.g. ctrl+s, return, escape)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			inp, err := input.Open(1920, 1080)
			if err != nil {
				return err
			}
			defer inp.Close()
			return pressCombo(inp, args[0])
		},
	}

	// New low-level bindings: keydown / keyup / mousedown / mouseup
	keydown := &cobra.Command{
		Use:   "keydown <key>",
		Short: "Press and hold a key",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			inp, err := input.Open(1920, 1080)
			if err != nil {
				return err
			}
			defer inp.Close()
			if err := inp.KeyDown(args[0]); err != nil {
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
			inp, err := input.Open(1920, 1080)
			if err != nil {
				return err
			}
			defer inp.Close()
			if err := inp.KeyUp(args[0]); err != nil {
				return err
			}
			fmt.Printf("keyup %s\n", args[0])
			return nil
		},
	}

	var mdx, mdy int
	var mdButton int
	mousedown := &cobra.Command{
		Use:   "mousedown",
		Short: "Press a mouse button (optional coords)",
		RunE: func(_ *cobra.Command, _ []string) error {
			inp, err := input.Open(1920, 1080)
			if err != nil {
				return err
			}
			defer inp.Close()
			if mdx != -1 && mdy != -1 {
				if err := inp.MouseMove(mdx, mdy); err != nil {
					return err
				}
			}
			if err := inp.MouseDown(mdButton); err != nil {
				return err
			}
			fmt.Printf("mousedown button %d at %d,%d\n", mdButton, mdx, mdy)
			return nil
		},
	}
	mousedown.Flags().IntVar(&mdx, "x", -1, "x coordinate (optional)")
	mousedown.Flags().IntVar(&mdy, "y", -1, "y coordinate (optional)")
	mousedown.Flags().IntVar(&mdButton, "button", 1, "button number")

	var muX, muY int
	var muButton int
	mouseup := &cobra.Command{
		Use:   "mouseup",
		Short: "Release a mouse button (optional coords)",
		RunE: func(_ *cobra.Command, _ []string) error {
			inp, err := input.Open(1920, 1080)
			if err != nil {
				return err
			}
			defer inp.Close()
			if muX != -1 && muY != -1 {
				if err := inp.MouseMove(muX, muY); err != nil {
					return err
				}
			}
			if err := inp.MouseUp(muButton); err != nil {
				return err
			}
			fmt.Printf("mouseup button %d at %d,%d\n", muButton, muX, muY)
			return nil
		},
	}
	mouseup.Flags().IntVar(&muX, "x", -1, "x coordinate (optional)")
	mouseup.Flags().IntVar(&muY, "y", -1, "y coordinate (optional)")
	mouseup.Flags().IntVar(&muButton, "button", 1, "button number")

	cmd.AddCommand(move, click, typeCmd, key, keydown, keyup, mousedown, mouseup)
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
		RunE: func(_ *cobra.Command, _ []string) error {
			wm, err := window.Open()
			if err != nil {
				return err
			}
			defer wm.Close()
			wins, err := wm.List()
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
		Use:   "activate <title>",
		Short: "Bring a window to the foreground by title substring",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			wm, err := window.Open()
			if err != nil {
				return err
			}
			defer wm.Close()
			if err := wm.Activate(args[0]); err != nil {
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
			wm, err := window.Open()
			if err != nil {
				return err
			}
			defer wm.Close()
			t, err := wm.ActiveTitle()
			if err != nil {
				return err
			}
			fmt.Println(t)
			return nil
		},
	}

	var (
		mvTitle  string
		mvX, mvY int
	)
	move := &cobra.Command{
		Use:   "move",
		Short: "Move a window to absolute screen coordinates",
		RunE: func(_ *cobra.Command, _ []string) error {
			wm, err := window.Open()
			if err != nil {
				return err
			}
			defer wm.Close()
			if err := wm.Move(mvTitle, mvX, mvY); err != nil {
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

	var (
		rsTitle  string
		rsW, rsH int
	)
	resize := &cobra.Command{
		Use:   "resize",
		Short: "Resize a window",
		RunE: func(_ *cobra.Command, _ []string) error {
			wm, err := window.Open()
			if err != nil {
				return err
			}
			defer wm.Close()
			if err := wm.Resize(rsTitle, rsW, rsH); err != nil {
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
	return cmd
}

func findCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "find", Short: "Pixel scanning and wait utilities"}

	var rectFlag string
	var rectsFlag string
	var wantsFlag string
	var hashFlag string
	var pollFlag string
	var timeoutFlag string
	var captureInitial bool

	// pixel-hash (alias of checksum)
	pixelHash := &cobra.Command{
		Use:   "pixel-hash",
		Short: "Print the CRC32 pixel hash of a screen region",
		RunE: func(_ *cobra.Command, _ []string) error {
			sc, err := screen.Open()
			if err != nil {
				return err
			}
			defer sc.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			h, err := find.GrabHash(sc, r, nil)
			if err != nil {
				return err
			}
			fmt.Println(h)
			return nil
		},
	}
	pixelHash.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")

	lastPixel := &cobra.Command{
		Use:   "last-pixel",
		Short: "Print the RGB colour of the bottom-right pixel of a region",
		RunE: func(_ *cobra.Command, _ []string) error {
			sc, err := screen.Open()
			if err != nil {
				return err
			}
			defer sc.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			c, err := find.LastPixel(sc, r)
			if err != nil {
				return err
			}
			fmt.Printf("R=%d G=%d B=%d\n", c.R, c.G, c.B)
			return nil
		},
	}
	lastPixel.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")

	waitFor := &cobra.Command{
		Use:   "wait-for",
		Short: "Wait until a region's pixel hash equals the provided hash",
		RunE: func(_ *cobra.Command, _ []string) error {
			sc, err := screen.Open()
			if err != nil {
				return err
			}
			defer sc.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}

			want, err := parseHash(hashFlag)
			if err != nil {
				return err
			}
			poll, err := parseDurationOrDefault(pollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDurationOrDefault(timeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			h, err := find.WaitFor(ctx, sc, r, want, poll, nil)
			if err != nil {
				return err
			}
			fmt.Println(h)
			return nil
		},
	}
	waitFor.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitFor.Flags().StringVar(&hashFlag, "hash", "", "target hash (decimal or 0xhex)")
	waitFor.Flags().StringVar(&pollFlag, "poll", "50ms", "poll interval (e.g. 50ms)")
	waitFor.Flags().StringVar(&timeoutFlag, "timeout", "5s", "timeout duration (e.g. 5s)")
	waitFor.MarkFlagRequired("hash")

	waitForChange := &cobra.Command{
		Use:   "wait-for-change",
		Short: "Wait until a region's pixel hash changes from an initial value",
		RunE: func(_ *cobra.Command, _ []string) error {
			sc, err := screen.Open()
			if err != nil {
				return err
			}
			defer sc.Close()
			defer sc.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			var initial uint32
			if captureInitial {
				h, err := find.GrabHash(sc, r, nil)
				if err != nil {
					return err
				}
				initial = h
			} else {
				initial, err = parseHash(hashFlag)
				if err != nil {
					return err
				}
			}
			poll, err := parseDurationOrDefault(pollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDurationOrDefault(timeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			h, err := find.WaitForChange(ctx, sc, r, initial, poll, nil)
			if err != nil {
				return err
			}
			fmt.Println(h)
			return nil
		},
	}
	waitForChange.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitForChange.Flags().StringVar(&hashFlag, "initial", "", "initial hash (decimal or 0xhex)")
	waitForChange.Flags().BoolVar(&captureInitial, "capture-initial", false, "capture current region hash and wait for it to change")
	waitForChange.Flags().StringVar(&pollFlag, "poll", "50ms", "poll interval")
	waitForChange.Flags().StringVar(&timeoutFlag, "timeout", "5s", "timeout duration")
	waitForChange.MarkFlagsMutuallyExclusive("initial", "capture-initial")
	waitForChange.MarkFlagsOneRequired("initial", "capture-initial")

	scanFor := &cobra.Command{
		Use:   "scan-for",
		Short: "Scan multiple regions until one matches its expected hash",
		RunE: func(_ *cobra.Command, _ []string) error {
			sc, err := screen.Open()
			if err != nil {
				return err
			}
			defer sc.Close()
			defer sc.Close()
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
			poll, err := parseDurationOrDefault(pollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDurationOrDefault(timeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			res, err := find.ScanFor(ctx, sc, rects, wants, poll, nil)
			if err != nil {
				return err
			}
			fmt.Printf("match %v -> %08x\n", res.Rect, res.Hash)
			return nil
		},
	}
	scanFor.Flags().StringVar(&rectsFlag, "rects", "", "semicolon-separated rects: x0,y0,x1,y1;...")
	scanFor.Flags().StringVar(&wantsFlag, "wants", "", "comma-separated expected hashes (decimal or 0xhex)")
	scanFor.Flags().StringVar(&pollFlag, "poll", "50ms", "poll interval")
	scanFor.Flags().StringVar(&timeoutFlag, "timeout", "5s", "timeout duration")
	scanFor.MarkFlagRequired("rects")
	scanFor.MarkFlagRequired("wants")

	cmd.AddCommand(pixelHash, lastPixel, waitFor, waitForChange, scanFor)
	return cmd
}

// parseDurationOrDefault parses a duration string or returns default.
func parseDurationOrDefault(s string, d time.Duration) (time.Duration, error) {
	if s == "" {
		return d, nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %v", s, err)
	}
	return v, nil
}

// parseHash accepts decimal or 0x hex and returns uint32
func parseHash(s string) (uint32, error) {
	s = strings.TrimPrefix(s, "0x")

	isHex := false
	for _, c := range s {
		if (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			isHex = true
			break
		}
	}

	if isHex {
		v, err := strconv.ParseUint(s, 16, 32)
		if err != nil {
			return 0, fmt.Errorf("invalid hex hash %q: %v", s, err)
		}
		return uint32(v), nil
	}

	v, err := strconv.ParseUint(s, 0, 32)
	if err != nil {
		vHex, errHex := strconv.ParseUint(s, 16, 32)
		if errHex == nil {
			return uint32(vHex), nil
		}
		return 0, fmt.Errorf("invalid hash %q: %v", s, err)
	}
	return uint32(v), nil
}

// parseWantHashes parses comma-separated hashes into []uint32
func parseWantHashes(s string) ([]uint32, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		h, err := parseHash(p)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

// parseRects parses semicolon-separated rects
func parseRects(s string) ([]image.Rectangle, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ";")
	out := make([]image.Rectangle, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		r, err := parseRect(p)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

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
