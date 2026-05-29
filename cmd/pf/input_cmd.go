package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/spf13/cobra"
)

func inputCmd(openPF func() (*perfuncted.Perfuncted, error), cfg *cliConfig) *cobra.Command {
	cmd := &cobra.Command{Use: "input", Short: "Mouse and keyboard injection"}
	syncIf := func(pf *perfuncted.Perfuncted) error {
		if cfg != nil && cfg.sync {
			return pf.Input.Sync(context.Background())
		}
		return nil
	}

	var mx, my, button int
	var typeStdin bool

	move := &cobra.Command{
		Use:   "move",
		Short: "Move mouse to absolute coordinates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.MouseMove(cmd.Context(), mx, my); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
				return err
			}
			fmt.Printf("moved to %d,%d\n", mx, my)
			return nil
		},
	}
	move.Flags().IntVar(&mx, "x", 0, "x coordinate")
	move.Flags().IntVar(&my, "y", 0, "y coordinate")

	var clickRepeat int
	var clickDelayFlag string
	click := &cobra.Command{
		Use:   "click",
		Short: "Click a mouse button at coordinates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			repeat := clickRepeat
			if repeat <= 0 {
				repeat = 1
			}
			delay, err := parseDuration(clickDelayFlag, 0)
			if err != nil {
				return err
			}
			for i := 0; i < repeat; i++ {
				if err := pf.Input.MouseClick(cmd.Context(), mx, my, button); err != nil {
					return err
				}
				if i+1 < repeat && delay > 0 {
					time.Sleep(delay)
				}
			}
			if err := syncIf(pf); err != nil {
				return err
			}
			fmt.Printf("clicked button %d at %d,%d\n", button, mx, my)
			return nil
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.DoubleClick(cmd.Context(), mx, my); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.DragAndDrop(cmd.Context(), x1, y1, x2, y2); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(crRect)
			if err != nil {
				return err
			}
			if err := pf.Input.ClickCenter(cmd.Context(), r); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
				return err
			}
			fmt.Printf("clicked center %d,%d\n", r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2)
			return nil
		},
	}
	clickCenter.Flags().StringVar(&crRect, "rect", "0,0,100,100", "x0,y0,x1,y1")

	typeCmd := &cobra.Command{
		Use:   "type <text>",
		Short: "Type a string or send keys (e.g. {enter}, {ctrl+s})",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			text := ""
			useStdin := typeStdin
			if useStdin {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				text = string(b)
			} else if len(args) == 1 {
				text = args[0]
			} else {
				return fmt.Errorf("type requires text or --stdin")
			}
			if err := pf.Input.Type(cmd.Context(), text); err != nil {
				return err
			}
			return syncIf(pf)
		},
	}
	typeCmd.Flags().BoolVar(&typeStdin, "stdin", false, "read text from stdin")

	keydown := &cobra.Command{
		Use:   "keydown <key>",
		Short: "Press and hold a key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.KeyDown(cmd.Context(), args[0]); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
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
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.KeyUp(cmd.Context(), args[0]); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
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
			if err := syncIf(pf); err != nil {
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
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
			if err := syncIf(pf); err != nil {
				return err
			}
			fmt.Printf("mouseup button %d at %d,%d\n", muButton, mux, muy)
			return nil
		},
	}
	mouseup.Flags().IntVar(&mux, "x", -1, "x coordinate (optional)")
	mouseup.Flags().IntVar(&muy, "y", -1, "y coordinate (optional)")
	mouseup.Flags().IntVar(&muButton, "button", 1, "button number")

	location := &cobra.Command{
		Use:   "location",
		Short: "Print current pointer location",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			x, y, err := pf.Input.PointerLocation(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Printf("%d,%d\n", x, y)
			return nil
		},
	}

	cmd.AddCommand(move, click, doubleClick, drag, clickCenter,
		typeCmd, keydown, keyup, mousedown, mouseup, location, scrollCmd(openPF, cfg))

	sync := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize the input backend with the compositor",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
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

func scrollCmd(openPF func() (*perfuncted.Perfuncted, error), cfg *cliConfig) *cobra.Command {
	cmd := &cobra.Command{Use: "scroll", Short: "Scroll the mouse wheel"}
	syncIf := func(pf *perfuncted.Perfuncted) error {
		if cfg != nil && cfg.sync {
			return pf.Input.Sync(context.Background())
		}
		return nil
	}

	var clicks int

	up := &cobra.Command{
		Use:   "up",
		Short: "Scroll up by N clicks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.ScrollUp(cmd.Context(), clicks); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.ScrollDown(cmd.Context(), clicks); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.ScrollLeft(cmd.Context(), clicks); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Input.ScrollRight(cmd.Context(), clicks); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
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
