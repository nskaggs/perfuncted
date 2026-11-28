package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/spf13/cobra"
)

//nolint:gocyclo // Cobra command assembly is intentionally centralized
func inputCmd(
	openPF sessionOpener,
	cfg *cliConfig,
	inputWindowOpeners ...sessionOpener,
) *cobra.Command {
	var openInputWindow sessionOpener
	if len(inputWindowOpeners) > 0 {
		openInputWindow = inputWindowOpeners[0]
	}
	var openInputOutput, openInputDialog, openInputObserver sessionOpener
	if len(inputWindowOpeners) > 1 {
		openInputOutput = inputWindowOpeners[1]
	}
	if len(inputWindowOpeners) > 2 {
		openInputDialog = inputWindowOpeners[2]
	}
	if len(inputWindowOpeners) > 3 {
		openInputObserver = inputWindowOpeners[3]
	}
	cmd := &cobra.Command{Use: "input", Short: "Mouse and keyboard injection"}
	syncIf := func(ctx context.Context, pf *perfuncted.Session) error {
		if cfg != nil && cfg.sync {
			return pf.Input().WithContext(ctx).Sync() //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
		}
		return nil
	}

	var mx, my, button int
	var motionDuration, motionEasing string
	var motionCurve float64
	var motionVariation int
	var motionSeed uint64
	var fromX, fromY int
	motionOpts := func() (input.MotionOptions, error) {
		duration, err := parseDuration(motionDuration, 0)
		if err != nil {
			return input.MotionOptions{}, err
		}
		return input.MotionOptions{Duration: duration, Easing: motionEasing, Curve: motionCurve, Variation: motionVariation, Seed: motionSeed}, nil
	}
	addMotionFlags := func(c *cobra.Command) {
		c.Flags().StringVar(&motionDuration, "duration", "0", "motion duration (0 means direct; max 10s)")
		c.Flags().StringVar(&motionEasing, "easing", "linear", "linear or ease-in-out")
		c.Flags().Float64Var(&motionCurve, "curve", 0, "perpendicular curve fraction (-0.5..0.5)")
		c.Flags().IntVar(&motionVariation, "variation", 0, "seeded coordinate variation in units (0..16)")
		c.Flags().Uint64Var(&motionSeed, "seed", 0, "variation seed")
	}

	move := &cobra.Command{
		Use:   "move",
		Short: "Move mouse to absolute coordinates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := motionOpts()
			if err != nil {
				return err
			}
			if validationErr := input.ValidateMotionOptions(opts); validationErr != nil {
				return validationErr
			}
			if cmd.Flags().Changed("from-x") != cmd.Flags().Changed("from-y") {
				return fmt.Errorf("from-x and from-y must both be supplied")
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if opts.Duration == 0 {
				err = pf.Input().WithContext(cmd.Context()).MouseMove(mx, my)
			} else {
				var origin *image.Point
				if cmd.Flags().Changed("from-x") {
					origin = &image.Point{X: fromX, Y: fromY}
				}
				err = pf.Input().WithContext(cmd.Context()).MouseMoveSmooth(origin, image.Point{X: mx, Y: my}, opts)
			}
			if err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "moved to %d,%d\n", mx, my)
		},
	}
	move.Flags().IntVar(&mx, "x", 0, "x coordinate")
	move.Flags().IntVar(&my, "y", 0, "y coordinate")
	addMotionFlags(move)
	move.Flags().IntVar(&fromX, "from-x", 0, "explicit start x (required with from-y)")
	move.Flags().IntVar(&fromY, "from-y", 0, "explicit start y (required with from-x)")

	var relX, relY int
	var verifyRelative bool
	moveRelative := &cobra.Command{
		Use:   "move-relative",
		Short: "Move pointer relative to its compositor-reported position",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if openInputWindow == nil {
				return fmt.Errorf("relative pointer movement requires window capability")
			}
			pf, err := openInputWindow(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			space, err := pf.Input().WithContext(cmd.Context()).PointerCoordinateSpace()
			if err != nil {
				return err
			}
			// KWin cursorPos is compositor-logical; never add its values to
			// physical framebuffer or unverified device coordinates.
			if string(space.Kind) != "global-logical" || space.ScaleX != 1 || space.ScaleY != 1 {
				return fmt.Errorf("cannot safely move relative to KWin cursor in pointer space %s with scale %g,%g", space.Kind, space.ScaleX, space.ScaleY)
			}
			x, y, err := pf.Input().WithContext(cmd.Context()).PointerLocation()
			if err != nil && errors.Is(err, perfuncted.ErrUnsupported) {
				x, y, err = pf.Windows().WithContext(cmd.Context()).CursorPosition()
			}
			if err != nil {
				return err
			}
			maxInt := int(^uint(0) >> 1)
			minInt := -maxInt - 1
			if (relX > 0 && x > maxInt-relX) || (relX < 0 && x < minInt-relX) ||
				(relY > 0 && y > maxInt-relY) || (relY < 0 && y < minInt-relY) {
				return fmt.Errorf("relative movement overflows pointer coordinates")
			}
			destX, destY := x+relX, y+relY
			if err := pf.Input().WithContext(cmd.Context()).MouseMove(destX, destY); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			if verifyRelative {
				verifiedX, verifiedY, readErr := pf.Windows().WithContext(cmd.Context()).CursorPosition()
				if readErr != nil {
					return fmt.Errorf("relative pointer position verification: %w", readErr)
				}
				if verifiedX != destX || verifiedY != destY {
					return fmt.Errorf("relative pointer move unverified: requested %d,%d, observed %d,%d", destX, destY, verifiedX, verifiedY)
				}
			}
			return writeCLIOutput(cmd.OutOrStdout(), "moved relative by %d,%d to %d,%d\n", relX, relY, destX, destY)
		},
	}
	moveRelative.Flags().IntVar(&relX, "dx", 0, "horizontal delta in compositor-logical pixels")
	moveRelative.Flags().IntVar(&relY, "dy", 0, "vertical delta in compositor-logical pixels")
	moveRelative.Flags().BoolVar(&verifyRelative, "verify", true, "confirm compositor cursor reached the target")
	cmd.AddCommand(moveRelative)

	var clickRepeat int
	var clickDelayFlag string
	click := &cobra.Command{
		Use:   "click",
		Short: "Click a mouse button at coordinates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if clickRepeat < 0 {
				return fmt.Errorf("click repeat count must not be negative")
			}
			// Reject invalid timings before opening a potentially
			// authorization-prompting desktop input session.
			delay, err := parseDuration(clickDelayFlag, 0)
			if err != nil {
				return err
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			repeat := clickRepeat
			if repeat <= 0 {
				repeat = 1
			}
			var timer *time.Timer
			if delay > 0 && repeat > 1 {
				timer = time.NewTimer(delay)
				stopAndDrainTimer(timer)
				defer timer.Stop()
			}
			for i := 0; i < repeat; i++ {
				if err := pf.Input().WithContext(cmd.Context()).Click(mx, my, button); err != nil {
					return err
				}
				if i+1 < repeat && timer != nil {
					timer.Reset(delay)
					select {
					case <-cmd.Context().Done():
						stopAndDrainTimer(timer)
						return cmd.Context().Err()
					case <-timer.C:
					}
				}
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "clicked button %d at %d,%d\n", button, mx, my)
		},
	}
	click.Flags().IntVar(&mx, "x", 0, "x coordinate")
	click.Flags().IntVar(&my, "y", 0, "y coordinate")
	click.Flags().IntVar(&button, "button", 1, "1=left 2=middle 3=right")
	click.Flags().IntVar(&clickRepeat, "repeat", 1, "repeat count")
	click.Flags().StringVar(&clickDelayFlag, "delay", "0", "delay between clicks")

	doubleClick := &cobra.Command{
		Use:   "double-click",
		Short: "Double-click at coordinates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input().WithContext(cmd.Context()).DoubleClick(mx, my); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "double-clicked at %d,%d\n", mx, my)
		},
	}
	doubleClick.Flags().IntVar(&mx, "x", 0, "x coordinate")
	doubleClick.Flags().IntVar(&my, "y", 0, "y coordinate")

	var x1, y1, x2, y2 int
	drag := &cobra.Command{
		Use:   "drag-and-drop",
		Short: "Drag from one coordinate to another (press, move, release)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := motionOpts()
			if err != nil {
				return err
			}
			if validationErr := input.ValidateMotionOptions(opts); validationErr != nil {
				return validationErr
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if opts.Duration == 0 {
				err = pf.Input().WithContext(cmd.Context()).DragAndDrop(x1, y1, x2, y2)
			} else {
				err = pf.Input().WithContext(cmd.Context()).DragAndDropSmooth(image.Point{X: x1, Y: y1}, image.Point{X: x2, Y: y2}, opts)
			}
			if err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "dragged %d,%d to %d,%d\n", x1, y1, x2, y2)
		},
	}
	drag.Flags().IntVar(&x1, "x1", 0, "start x")
	drag.Flags().IntVar(&y1, "y1", 0, "start y")
	drag.Flags().IntVar(&x2, "x2", 0, "end x")
	drag.Flags().IntVar(&y2, "y2", 0, "end y")
	addMotionFlags(drag)

	var crRect string
	clickCenter := &cobra.Command{
		Use:   "click-center",
		Short: "Click the center of a rectangle",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := parseRect(crRect)
			if err != nil {
				return err
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input().WithContext(cmd.Context()).ClickCenter(r); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "clicked center %d,%d\n", r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2)
		},
	}
	clickCenter.Flags().StringVar(&crRect, "rect", "0,0,100,100", "x0,y0,x1,y1")

	keydown := &cobra.Command{
		Use:   "keydown <key>",
		Short: "Press and hold a key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input().WithContext(cmd.Context()).KeyDown(args[0]); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "keydown %s\n", args[0])
		},
	}

	keyup := &cobra.Command{
		Use:   "keyup <key>",
		Short: "Release a held key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input().WithContext(cmd.Context()).KeyUp(args[0]); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "keyup %s\n", args[0])
		},
	}

	var mdx, mdy, mdButton int
	//nolint:dupl // Symmetric press/release commands preserve the same validation and output UX.
	mousedown := &cobra.Command{
		Use:   "mousedown",
		Short: "Press a mouse button (optional coords)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (mdx == -1) != (mdy == -1) {
				return fmt.Errorf("mousedown requires both --x and --y, or neither")
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if mdx != -1 && mdy != -1 {
				if err := pf.Input().WithContext(cmd.Context()).MouseMove(mdx, mdy); err != nil {
					return err
				}
			}
			if err := pf.Input().WithContext(cmd.Context()).MouseDown(mdButton); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "mousedown button %d at %d,%d\n", mdButton, mdx, mdy)
		},
	}
	mousedown.Flags().IntVar(&mdx, "x", -1, "x coordinate (optional)")
	mousedown.Flags().IntVar(&mdy, "y", -1, "y coordinate (optional)")
	mousedown.Flags().IntVar(&mdButton, "button", 1, "button number")

	var mux, muy, muButton int
	//nolint:dupl // Symmetric press/release commands preserve the same validation and output UX.
	mouseup := &cobra.Command{
		Use:   "mouseup",
		Short: "Release a mouse button (optional coords)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (mux == -1) != (muy == -1) {
				return fmt.Errorf("mouseup requires both --x and --y, or neither")
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if mux != -1 && muy != -1 {
				if err := pf.Input().WithContext(cmd.Context()).MouseMove(mux, muy); err != nil {
					return err
				}
			}
			if err := pf.Input().WithContext(cmd.Context()).MouseUp(muButton); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "mouseup button %d at %d,%d\n", muButton, mux, muy)
		},
	}
	mouseup.Flags().IntVar(&mux, "x", -1, "x coordinate (optional)")
	mouseup.Flags().IntVar(&muy, "y", -1, "y coordinate (optional)")
	mouseup.Flags().IntVar(&muButton, "button", 1, "button number")

	var locationOutput string
	location := &cobra.Command{
		Use:   "location",
		Short: "Print current pointer location",
		Long:  "Print current pointer location as x,y or JSON. JSON reports fresh GNOME global logical coordinates (1:1) or X11 root framebuffer pixels (native 1:1, not monitor scaling). Signed coordinates and origin zero are valid. observedAt is the local observation receipt time, not an atomic guarantee for subsequent input. Portal/EIS, virtual pointer, uinput and unverified KDE routes return unsupported in JSON; plain output retains its existing fallback.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if locationOutput != "plain" && locationOutput != "json" {
				return fmt.Errorf("invalid location output %q", locationOutput)
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if locationOutput == "json" {
				result, observeErr := pf.Input().WithContext(cmd.Context()).ObservePointer()
				if observeErr != nil {
					return observeErr
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			x, y, err := pf.Input().WithContext(cmd.Context()).PointerLocation()
			if err != nil && errors.Is(err, perfuncted.ErrUnsupported) && openInputWindow != nil {
				// uinput and portal-EIS can inject input but cannot read the
				// composited cursor. KDE's window backend can query KWin
				// directly, without requiring a privileged injection device.
				other, openErr := openInputWindow(cmd.Context())
				if openErr == nil {
					x, y, err = other.Windows().WithContext(cmd.Context()).CursorPosition()
					_ = other.Close()
				}
			}
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%d,%d\n", x, y)
		},
	}

	location.Flags().StringVar(&locationOutput, "output", "plain", "Output format: plain or json (authoritative input provider only)")
	coordinateSpace := &cobra.Command{
		Use:   "coordinate-space",
		Short: "Print the pointer backend coordinate space",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			info, err := pf.Input().WithContext(cmd.Context()).PointerCoordinateSpace()
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "kind=%s scaleX=%g scaleY=%g\n", info.Kind, info.ScaleX, info.ScaleY)
		},
	}

	var clickWindowID string
	var clickWindowTitle string
	var clickWindowX, clickWindowY, clickWindowButton int
	clickWindow := &cobra.Command{
		Use:   "click-window",
		Short: "Click a window-relative logical point using an explicitly declared backend coordinate space",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (clickWindowID == "") == (clickWindowTitle == "") {
				return fmt.Errorf("specify exactly one of --id or --title")
			}
			if openInputWindow == nil {
				return fmt.Errorf("click-window requires windows capability")
			}
			pf, err := openInputWindow(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			var target *perfuncted.Window
			if clickWindowID != "" {
				target, err = findWindowByStableID(cmd.Context(), pf.Windows(), clickWindowID)
			} else {
				target, err = findWindowByTitle(cmd.Context(), pf.Windows(), clickWindowTitle)
			}
			if err != nil {
				return err
			}
			info, err := target.WithContext(cmd.Context()).Info()
			if err != nil {
				return err
			}
			space, err := pf.Input().WithContext(cmd.Context()).PointerCoordinateSpace()
			if err != nil {
				return fmt.Errorf("cannot safely map window coordinates to current input backend: %w (use --input-backend remote-desktop on Wayland)", err)
			}
			px, py, err := windowRelativePoint(info, clickWindowX, clickWindowY, space)
			if err != nil {
				return err
			}
			if err := pf.Input().WithContext(cmd.Context()).Click(px, py, clickWindowButton); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "click event sent to window %s at %d,%d (backend %d,%d)\n", target.ID().String(), clickWindowX, clickWindowY, px, py)
		},
	}
	clickWindow.Flags().StringVar(&clickWindowID, "id", "", "stable ID from window list")
	clickWindow.Flags().StringVar(&clickWindowTitle, "title", "", "unique window title substring")
	clickWindow.Flags().IntVar(&clickWindowX, "x", -1, "logical window-relative X")
	clickWindow.Flags().IntVar(&clickWindowY, "y", -1, "logical window-relative Y")
	clickWindow.Flags().IntVar(&clickWindowButton, "button", 1, "mouse button")
	cmd.AddCommand(clickWindow)
	var clickScreenX, clickScreenY, clickScreenButton int
	clickScreen := &cobra.Command{Use: "click-screen", Short: "Click at physical screenshot pixels via authoritative output and backend coordinates", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if clickScreenX < 0 || clickScreenY < 0 {
			return fmt.Errorf("screen coordinates must be non-negative")
		}
		if openInputOutput == nil {
			return fmt.Errorf("click-screen requires outputs capability")
		}
		pf, err := openInputOutput(cmd.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		outs, err := pf.Outputs().WithContext(cmd.Context()).List()
		if err != nil {
			return err
		}
		lx, ly, err := screenshotPointToLogical(outs, clickScreenX, clickScreenY)
		if err != nil {
			return err
		}
		space, err := pf.Input().WithContext(cmd.Context()).PointerCoordinateSpace()
		if err != nil {
			return fmt.Errorf("cannot map screenshot point to input backend: %w (use --input-backend remote-desktop on Wayland)", err)
		}
		fx, fy := math.Round(float64(lx)*space.ScaleX), math.Round(float64(ly)*space.ScaleY)
		// float64(math.MaxInt) rounds up to 2^63 on 64-bit hosts.
		// A value at that rounded limit already exceeds the largest int.
		if fx >= float64(math.MaxInt) || fx < float64(math.MinInt) || fy >= float64(math.MaxInt) || fy < float64(math.MinInt) {
			return fmt.Errorf("mapped screenshot point outside pointer range")
		}
		px, py := int(fx), int(fy)
		if err := pf.Input().WithContext(cmd.Context()).Click(px, py, clickScreenButton); err != nil {
			return err
		}
		if err := syncIf(cmd.Context(), pf); err != nil {
			return err
		}
		return writeCLIOutput(cmd.OutOrStdout(), "click event sent at screenshot %d,%d -> input %d,%d\n", clickScreenX, clickScreenY, px, py)
	}}
	clickScreen.Flags().IntVar(&clickScreenX, "x", -1, "physical screenshot X")
	clickScreen.Flags().IntVar(&clickScreenY, "y", -1, "physical screenshot Y")
	clickScreen.Flags().IntVar(&clickScreenButton, "button", 1, "mouse button")
	cmd.AddCommand(clickScreen)

	var fileTargetID, fileTargetTitle, filePath string
	var confirmFile bool
	selectFile := &cobra.Command{Use: "select-file", Short: "Select a local file in an existing native file picker, preserving clipboard", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if (fileTargetID == "") == (fileTargetTitle == "") {
			return fmt.Errorf("specify exactly one of --id or --title for the open file chooser")
		}
		if !filepath.IsAbs(filePath) {
			return fmt.Errorf("file path must be absolute")
		}
		st, err := os.Stat(filePath)
		if err != nil {
			return fmt.Errorf("file chooser path: %w", err)
		}
		if st.IsDir() {
			return fmt.Errorf("file chooser expects a regular file, not a directory")
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("file chooser path is not a regular file")
		}
		if openInputDialog == nil {
			return fmt.Errorf("select-file requires window and clipboard capabilities")
		}
		pf, err := openInputDialog(cmd.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		var target *perfuncted.Window
		if fileTargetID != "" {
			target, err = resolveWindowID(cmd.Context(), pf.Windows(), fileTargetID, true)
		} else {
			target, err = findWindowByTitle(cmd.Context(), pf.Windows(), fileTargetTitle)
		}
		if err != nil {
			return err
		}
		info := target.CachedInfo()
		if info.PID == 0 && info.AppID == "" && info.Class == "" {
			return fmt.Errorf("file chooser lacks independent identity: %w", perfuncted.ErrUnsupported)
		}
		if _, infoErr := target.WithContext(cmd.Context()).Info(); infoErr != nil {
			return infoErr
		}
		if activateErr := target.WithContext(cmd.Context()).Activate(); activateErr != nil {
			return activateErr
		}
		if waitErr := waitWindowActive(cmd.Context(), target, 2*time.Second, 50*time.Millisecond); waitErr != nil {
			return waitErr
		}
		if err := selectFileGuarded(cmd.Context(), pf, target, filePath, confirmFile); err != nil {
			return err
		}
		return writeCLIOutput(cmd.OutOrStdout(), "file chooser closed after selection: %s (not proof of cloud upload)\n", target.ID().String())
	}}
	selectFile.Flags().StringVar(&fileTargetID, "id", "", "stable window ID of open file chooser")
	selectFile.Flags().StringVar(&fileTargetTitle, "title", "", "unique file chooser title")
	selectFile.Flags().StringVar(&filePath, "path", "", "absolute path to existing file")
	selectFile.Flags().BoolVar(&confirmFile, "confirm", true, "press final Enter to accept selection")
	cmd.AddCommand(selectFile)

	cmd.AddCommand(move, click, doubleClick, drag, clickCenter,
		keyboardTextCmd(openPF, openInputWindow, syncIf, false), keyboardTextCmd(openPF, openInputWindow, syncIf, true), keydown, keyup, mousedown, mouseup, location, coordinateSpace, scrollCmd(openPF, cfg))

	sync := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize the input backend with the compositor",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			return pf.Input().WithContext(cmd.Context()).Sync()
		},
	}
	cmd.AddCommand(sync)

	if openInputObserver != nil {
		cmd.AddCommand(inputStateCmd(openInputObserver))
		cmd.AddCommand(inputHotkeyCmd(openInputObserver))
	}

	return cmd
}

// ── scroll ─────────────────────────────────────────────────────────────────────────────

func scrollCmd(openPF sessionOpener, cfg *cliConfig) *cobra.Command {
	cmd := &cobra.Command{Use: "scroll", Short: "Scroll the mouse wheel"}
	syncIf := func(ctx context.Context, pf *perfuncted.Session) error {
		if cfg != nil && cfg.sync {
			return pf.Input().WithContext(ctx).Sync() //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
		}
		return nil
	}

	var clicks int

	up := &cobra.Command{
		Use:   "up",
		Short: "Scroll up by N clicks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input().WithContext(cmd.Context()).ScrollUp(clicks); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "scrolled up %d\n", clicks)
		},
	}
	up.Flags().IntVar(&clicks, "clicks", 3, "number of scroll clicks")

	down := &cobra.Command{
		Use:   "down",
		Short: "Scroll down by N clicks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input().WithContext(cmd.Context()).ScrollDown(clicks); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "scrolled down %d\n", clicks)
		},
	}
	down.Flags().IntVar(&clicks, "clicks", 3, "number of scroll clicks")

	cmd.AddCommand(up, down)

	left := &cobra.Command{
		Use:   "left",
		Short: "Scroll left by N clicks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input().WithContext(cmd.Context()).ScrollLeft(clicks); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "scrolled left %d\n", clicks)
		},
	}
	left.Flags().IntVar(&clicks, "clicks", 3, "number of scroll clicks")

	right := &cobra.Command{
		Use:   "right",
		Short: "Scroll right by N clicks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input().WithContext(cmd.Context()).ScrollRight(clicks); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "scrolled right %d\n", clicks)
		},
	}
	right.Flags().IntVar(&clicks, "clicks", 3, "number of scroll clicks")

	cmd.AddCommand(left, right)
	return cmd
}

// inputHotkeyCmd observes only explicitly requested global shortcuts or
// mouse-button transitions; it never opens an input-injection backend.
//
//nolint:gocyclo // Cobra command construction and streaming validation are collocated
func inputHotkeyCmd(openObserver sessionOpener) *cobra.Command {
	hotkey := &cobra.Command{Use: "hotkey", Short: "Observe authorized global shortcuts and mouse buttons"}
	var combo, buttonName, event, output string
	var reportRepeat bool
	var timeout time.Duration
	listen := &cobra.Command{
		Use:   "listen",
		Short: "Listen for an authorized global shortcut or mouse-button event",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if output != "json" {
				return fmt.Errorf("unsupported output %q (use json)", output)
			}
			button := 0
			switch buttonName {
			case "":
			case "left", "1":
				button = 1
			case "middle", "2":
				button = 2
			case "right", "3":
				button = 3
			default:
				return fmt.Errorf("invalid mouse button %q", buttonName)
			}
			filter := input.ObservationFilter{Combo: combo, Button: button, Transition: event, ReportRepeat: reportRepeat}
			if err := input.ValidateObservationFilter(filter); err != nil {
				return err
			}
			ctx := cmd.Context()
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, timeout)
				defer cancel()
			}
			pf, err := openObserver(ctx)
			if err != nil {
				return err
			}
			defer pf.Close()
			sub, err := pf.InputObservation().WithContext(ctx).Listen(filter)
			if err != nil {
				return err
			}
			defer sub.Close()
			return streamInputObservation(ctx, sub, cmd.OutOrStdout())
		},
	}
	listen.Flags().StringVar(&combo, "combo", "", "named shortcut (for example F8 or Ctrl+Shift+P)")
	listen.Flags().StringVar(&buttonName, "button", "", "mouse button (left, middle, right; native X11 only)")
	listen.Flags().StringVar(&event, "event", "press", "press, release, or both")
	listen.Flags().BoolVar(&reportRepeat, "repeat", false, "report backend-attested repeat events when supported")
	listen.Flags().StringVar(&output, "output", "json", "output format: json")
	listen.Flags().DurationVar(&timeout, "timeout", 0, "maximum listening duration (0 means until interrupted)")
	hotkey.AddCommand(listen)
	return hotkey
}

// streamInputObservation owns the single consumer of a bounded subscription.
// It neither registers input hooks nor changes Session/capability ownership.
func streamInputObservation(ctx context.Context, sub input.ObservationSubscription, out io.Writer) error {
	encoder := json.NewEncoder(out)
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return ctx.Err()
		case ev, ok := <-sub.Events():
			if !ok {
				if err := sub.Err(); err != nil && !errors.Is(err, context.Canceled) {
					return err
				}
				return nil
			}
			if err := encoder.Encode(ev); err != nil {
				return err
			}
		}
	}
}

// keyboardTextCmd shares validation, targeting, stdin and synchronization.
func keyboardTextCmd(open, openWindow sessionOpener, syncIf func(context.Context, *perfuncted.Session) error, keys bool) *cobra.Command {
	name, short := "type", "Type literal text without interpreting key syntax"
	if keys {
		name, short = "sendkeys", "Send interpreted key sequences (e.g. {ctrl+s})"
		// Targeted sequences are preflighted and guarded at parser-defined boundaries.
	}
	var stdin bool
	var title, id string
	cmd := &cobra.Command{
		Use:   name + " <text>",
		Short: short,
		Long:  short + ". Successful dispatch does not prove application acceptance.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targeted, err := keyboardTarget(cmd, title, id)
			if err != nil {
				return err
			}
			text, err := readKeyboardText(cmd.InOrStdin(), args, stdin)
			if err != nil {
				return err
			}
			if keys {
				if targeted {
					if _, validationErr := input.AtomicKeySequences(cmd.Context(), text); validationErr != nil {
						return validationErr
					}
				} else if validationErr := input.ValidateKeySequence(cmd.Context(), text); validationErr != nil {
					return validationErr
				}
			}
			opener := open
			if targeted {
				if openWindow == nil {
					return fmt.Errorf("targeted input requires window capability")
				}
				opener = openWindow
			}
			pf, err := opener(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if dispatchErr := dispatchKeyboardText(cmd.Context(), pf, title, id, text, keys); dispatchErr != nil {
				return dispatchErr
			}
			return syncIf(cmd.Context(), pf)
		},
	}
	cmd.Flags().BoolVar(&stdin, "stdin", false, "read text from stdin")
	cmd.Flags().StringVar(&title, "window", "", "target an exact, unique window title")
	cmd.Flags().StringVar(&id, "window-id", "", "target a unique stable window ID")
	return cmd
}

const maxKeyboardTextBytes = 4 * 1024 * 1024

func readKeyboardText(in io.Reader, args []string, stdin bool) (string, error) {
	var text string
	switch {
	case stdin && len(args) != 0:
		return "", fmt.Errorf("choose either text or --stdin")
	case stdin:
		b, err := io.ReadAll(io.LimitReader(in, maxKeyboardTextBytes+1))
		if err != nil {
			return "", err
		}
		text = string(b)
	case len(args) == 1:
		text = args[0]
	default:
		return "", fmt.Errorf("input requires text or --stdin")
	}
	if len(text) > maxKeyboardTextBytes {
		return "", fmt.Errorf("input text exceeds %d bytes", maxKeyboardTextBytes)
	}
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("input text must be valid UTF-8")
	}
	return text, nil
}

func keyboardTarget(cmd *cobra.Command, title, id string) (bool, error) {
	byTitle, byID := cmd.Flags().Changed("window"), cmd.Flags().Changed("window-id")
	if byTitle && byID || byTitle && strings.TrimSpace(title) == "" || byID && strings.TrimSpace(id) == "" {
		return false, fmt.Errorf("choose one nonempty --window or --window-id")
	}
	return byTitle || byID, nil
}

func dispatchKeyboardText(ctx context.Context, pf *perfuncted.Session, title, id, text string, keys bool) error {
	if title == "" && id == "" {
		if keys {
			return pf.Input().WithContext(ctx).SendKeys(text) //nolint:contextcheck // Scoped context is bound explicitly.
		}
		return pf.Input().WithContext(ctx).Type(text) //nolint:contextcheck // Scoped context is bound explicitly.
	}
	step := perfuncted.Type(text)
	if keys {
		step = perfuncted.SendKeys(text)
	}
	if id == "" {
		return pf.WithContext(ctx).Window(title).Do(step) //nolint:contextcheck // Scoped context is bound explicitly.
	}
	target, err := resolveWindowID(ctx, pf.Windows(), id, true)
	if err != nil {
		return err
	}
	return target.WithContext(ctx).Do(step) //nolint:contextcheck // Scoped context is bound explicitly.
}

// selectFileGuarded never refocuses a chooser after initial activation. A
// successful final observation confirms disappearance, not acceptance of a file.
func selectFileGuarded(ctx context.Context, pf *perfuncted.Session, target *perfuncted.Window, path string, confirm bool) (result error) {
	if err := checkChooserFocus(ctx, target); err != nil {
		return err
	}
	previous, err := pf.Clipboard().WithContext(ctx).Get() //nolint:contextcheck // Scoped context.
	if err != nil {
		return err
	}
	// Cleanup also covers uncertain staging failures; never restore if a
	// newer clipboard value has been observed. Clipboard has no atomic CAS.
	defer func() { result = errors.Join(result, restoreChooserClipboard(ctx, pf, path, previous)) }()
	if err := pf.Clipboard().WithContext(ctx).Set(path); err != nil { //nolint:contextcheck // Scoped context.
		return err
	}
	for _, sequence := range []string{"{ctrl+l}", "{ctrl+v}", "{enter}"} {
		if sequence == "{ctrl+v}" {
			current, err := pf.Clipboard().WithContext(ctx).Get() //nolint:contextcheck // Scoped context.
			if err != nil {
				return err
			}
			if current != path {
				return errors.New("file chooser clipboard changed before paste")
			}
		}
		if err := checkChooserFocus(ctx, target); err != nil {
			return err
		}
		if err := pf.Input().WithContext(ctx).SendKeys(sequence); err != nil { //nolint:contextcheck // Scoped context.
			return err
		}
	}
	if confirm {
		err := checkChooserFocus(ctx, target)
		switch {
		case errors.Is(err, perfuncted.ErrWindowNotFound):
			// First Enter may have already closed the chooser. Never send
			// another Enter to whatever application now owns focus.
		case err != nil:
			return err
		default:
			if err := pf.Input().WithContext(ctx).SendKeys("{enter}"); err != nil { //nolint:contextcheck // Scoped context.
				return err
			}
		}
	}
	return waitWindowGone(ctx, target, 10*time.Second, 75*time.Millisecond)
}

func checkChooserFocus(ctx context.Context, target *perfuncted.Window) error {
	info, err := target.WithContext(ctx).Info() //nolint:contextcheck // Scoped context.
	if err != nil {
		return err
	}
	if !info.Active {
		return perfuncted.ErrWindowFocusUnverified
	}
	return ctx.Err()
}

func restoreChooserClipboard(ctx context.Context, pf *perfuncted.Session, path, previous string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	cb := pf.Clipboard().WithContext(cleanup)
	current, err := cb.Get() //nolint:contextcheck // Bounded independent cleanup context.
	if err == nil && current == path {
		err = cb.Set(previous) //nolint:contextcheck // Bounded independent cleanup context.
	}
	if err != nil {
		return fmt.Errorf("restore clipboard: %w", err)
	}
	return nil
}
