package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/nskaggs/perfuncted"
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
	var openInputOutput, openInputDialog sessionOpener
	if len(inputWindowOpeners) > 1 {
		openInputOutput = inputWindowOpeners[1]
	}
	if len(inputWindowOpeners) > 2 {
		openInputDialog = inputWindowOpeners[2]
	}
	cmd := &cobra.Command{Use: "input", Short: "Mouse and keyboard injection"}
	syncIf := func(ctx context.Context, pf *perfuncted.Session) error {
		if cfg != nil && cfg.sync {
			return pf.Input.Sync(ctx)
		}
		return nil
	}

	var mx, my, button int
	var typeStdin bool

	move := &cobra.Command{
		Use:   "move",
		Short: "Move mouse to absolute coordinates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.MouseMove(cmd.Context(), mx, my); err != nil {
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

	var clickRepeat int
	var clickDelayFlag string
	click := &cobra.Command{
		Use:   "click",
		Short: "Click a mouse button at coordinates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
				if err := pf.Input.MouseClick(cmd.Context(), mx, my, button); err != nil {
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
			if err := pf.Input.DoubleClick(cmd.Context(), mx, my); err != nil {
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
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.DragAndDrop(cmd.Context(), x1, y1, x2, y2); err != nil {
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
			if err := pf.Input.ClickCenter(cmd.Context(), r); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "clicked center %d,%d\n", r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2)
		},
	}
	clickCenter.Flags().StringVar(&crRect, "rect", "0,0,100,100", "x0,y0,x1,y1")

	typeCmd := &cobra.Command{
		Use:   "type <text>",
		Short: "Type a string or send keys (e.g. {enter}, {ctrl+s})",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Missing text cannot require desktop access. Validate first;
			// stdin reads also precede session opening.
			text := ""
			switch {
			case typeStdin:
				b, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				text = string(b)
			case len(args) == 1:
				text = args[0]
			default:
				return fmt.Errorf("type requires text or --stdin")
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.Type(cmd.Context(), text); err != nil {
				return err
			}
			return syncIf(cmd.Context(), pf)
		},
	}
	typeCmd.Flags().BoolVar(&typeStdin, "stdin", false, "read text from stdin")

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
			if err := pf.Input.KeyDown(cmd.Context(), args[0]); err != nil {
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
			if err := pf.Input.KeyUp(cmd.Context(), args[0]); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "keyup %s\n", args[0])
		},
	}

	var mdx, mdy, mdButton int
	mousedown := &cobra.Command{
		Use:   "mousedown",
		Short: "Press a mouse button (optional coords)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if mdx != -1 && mdy != -1 {
				if err := pf.Input.MouseMove(cmd.Context(), mdx, mdy); err != nil {
					return err
				}
			}
			if err := pf.Input.MouseDown(cmd.Context(), mdButton); err != nil {
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
	mouseup := &cobra.Command{
		Use:   "mouseup",
		Short: "Release a mouse button (optional coords)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if mux != -1 && muy != -1 {
				if err := pf.Input.MouseMove(cmd.Context(), mux, muy); err != nil {
					return err
				}
			}
			if err := pf.Input.MouseUp(cmd.Context(), muButton); err != nil {
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

	location := &cobra.Command{
		Use:   "location",
		Short: "Print current pointer location",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			x, y, err := pf.Input.PointerLocation(cmd.Context())
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%d,%d\n", x, y)
		},
	}

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
			info, err := pf.Input.PointerCoordinateSpace(cmd.Context())
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
				target, err = findWindowByStableID(cmd.Context(), pf.Windows, clickWindowID)
			} else {
				target, err = findWindowByTitle(cmd.Context(), pf.Windows, clickWindowTitle)
			}
			if err != nil {
				return err
			}
			info, err := target.Info(cmd.Context())
			if err != nil {
				return err
			}
			space, err := pf.Input.PointerCoordinateSpace(cmd.Context())
			if err != nil {
				return fmt.Errorf("cannot safely map window coordinates to current input backend: %w (use --input-backend remote-desktop on Wayland)", err)
			}
			px, py, err := windowRelativePoint(info, clickWindowX, clickWindowY, space)
			if err != nil {
				return err
			}
			if err := pf.Input.MouseClick(cmd.Context(), px, py, clickWindowButton); err != nil {
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
		outs, err := pf.Outputs.List(cmd.Context())
		if err != nil {
			return err
		}
		lx, ly, err := screenshotPointToLogical(outs, clickScreenX, clickScreenY)
		if err != nil {
			return err
		}
		space, err := pf.Input.PointerCoordinateSpace(cmd.Context())
		if err != nil {
			return fmt.Errorf("cannot map screenshot point to input backend: %w (use --input-backend remote-desktop on Wayland)", err)
		}
		fx, fy := math.Round(float64(lx)*space.ScaleX), math.Round(float64(ly)*space.ScaleY)
		if fx > math.MaxInt || fx < math.MinInt || fy > math.MaxInt || fy < math.MinInt {
			return fmt.Errorf("mapped screenshot point outside pointer range")
		}
		px, py := int(fx), int(fy)
		if err := pf.Input.MouseClick(cmd.Context(), px, py, clickScreenButton); err != nil {
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
	selectFile := &cobra.Command{Use: "select-file", Short: "Select a local file in an existing native file picker, preserving clipboard", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) (retErr error) {
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
			target, err = findWindowByStableID(cmd.Context(), pf.Windows, fileTargetID)
		} else {
			target, err = findWindowByTitle(cmd.Context(), pf.Windows, fileTargetTitle)
		}
		if err != nil {
			return err
		}
		if err := target.Activate(cmd.Context()); err != nil {
			return err
		}
		if err := waitWindowActive(cmd.Context(), target, 2*time.Second, 50*time.Millisecond); err != nil {
			return err
		}
		prev, err := pf.Clipboard.Get(cmd.Context())
		if err != nil {
			return err
		}
		if err := pf.Clipboard.Set(cmd.Context(), filePath); err != nil {
			return err
		}
		defer func() {
			if restoreErr := pf.Clipboard.Set(context.WithoutCancel(cmd.Context()), prev); restoreErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("restore clipboard: %w", restoreErr))
			}
		}()
		if err := pf.Input.Type(cmd.Context(), "{ctrl+l}{ctrl+v}{enter}"); err != nil {
			return err
		}
		if confirmFile {
			// KDE and other choosers may first navigate into a directory and
			// select the filename. The final Enter accepts the selected file.
			if err := pf.Input.Type(cmd.Context(), "{enter}"); err != nil {
				return err
			}
		}
		// Only restore clipboard after the chooser disappears: event dispatch
		// alone does not prove that Ctrl+V has been processed.
		if err := waitWindowGone(cmd.Context(), pf.Windows, target.ID().String(), 10*time.Second, 75*time.Millisecond); err != nil {
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
		typeCmd, keydown, keyup, mousedown, mouseup, location, coordinateSpace, scrollCmd(openPF, cfg))

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
			return pf.Input.Sync(cmd.Context())
		},
	}
	cmd.AddCommand(sync)

	// append auto-generated input commands (avoid duplicates)
	existing := map[string]bool{}
	for _, c := range cmd.Commands() {
		existing[c.Name()] = true
	}
	for _, ac := range autogenInputCommands(openPF) {
		if !existing[ac.Name()] {
			cmd.AddCommand(ac)
		}
	}

	return cmd
}

// ── scroll ─────────────────────────────────────────────────────────────────────────────

func scrollCmd(openPF sessionOpener, cfg *cliConfig) *cobra.Command {
	cmd := &cobra.Command{Use: "scroll", Short: "Scroll the mouse wheel"}
	syncIf := func(ctx context.Context, pf *perfuncted.Session) error {
		if cfg != nil && cfg.sync {
			return pf.Input.Sync(ctx)
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
			if err := pf.Input.ScrollUp(cmd.Context(), clicks); err != nil {
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
			if err := pf.Input.ScrollDown(cmd.Context(), clicks); err != nil {
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
			if err := pf.Input.ScrollLeft(cmd.Context(), clicks); err != nil {
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
			if err := pf.Input.ScrollRight(cmd.Context(), clicks); err != nil {
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
