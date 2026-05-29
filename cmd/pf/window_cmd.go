package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/window"
	"github.com/spf13/cobra"
)

type windowOutputFormat string

const (
	windowOutputPlain windowOutputFormat = "plain"
	windowOutputJSON  windowOutputFormat = "json"
)

func parseWindowMatchArgs(args []string) (window.Match, error) {
	if len(args) == 0 {
		return window.Match{}, nil
	}
	return window.ParseMatchSpec(strings.Join(args, " "))
}

func collectWindowMatches(ctx context.Context, m window.Manager, match window.Match) ([]window.Info, error) {
	wins, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	var matched []window.Info
	for _, w := range wins {
		if match.Matches(w) {
			matched = append(matched, w)
		}
	}
	return matched, nil
}

func windowNotFoundError(match window.Match) error {
	return fmt.Errorf("window matching %q not found: %w", match.String(), window.ErrWindowNotFound)
}

func printWindowPlain(w window.Info) {
	fmt.Printf("0x%x\t%s\tapp_id=%s\tpid=%d\tactive=%t\tminimized=%t\tmaximized=%t\tfullscreen=%t\n",
		w.ID, w.Title, w.AppID, w.PID, w.Active, w.Minimized, w.Maximized, w.Fullscreen)
}

func printWindowListPlain(wins []window.Info) {
	for _, w := range wins {
		printWindowPlain(w)
	}
}

func waitForWindowMatch(ctx context.Context, m window.Manager, match window.Match, poll time.Duration) (window.Info, error) {
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		wins, err := collectWindowMatches(ctx, m, match)
		if err != nil {
			return window.Info{}, err
		}
		if len(wins) > 0 {
			return wins[0], nil
		}
		select {
		case <-ctx.Done():
			return window.Info{}, fmt.Errorf("wait for window %q: %w", match.String(), ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForWindowCloseMatch(ctx context.Context, m window.Manager, match window.Match, poll time.Duration) error {
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		wins, err := collectWindowMatches(ctx, m, match)
		if err != nil {
			return err
		}
		if len(wins) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for window close %q: %w", match.String(), ctx.Err())
		case <-ticker.C:
		}
	}
}

func windowCmd(openPF func() (*perfuncted.Perfuncted, error), cfg *cliConfig) *cobra.Command {
	cmd := &cobra.Command{Use: "window", Short: "Window management"}
	syncIf := func(pf *perfuncted.Perfuncted) error {
		if cfg != nil && cfg.sync {
			return pf.Window.Sync(context.Background())
		}
		return nil
	}
	listOutputFlag := string(windowOutputPlain)

	list := &cobra.Command{
		Use:   "list",
		Short: "List windows",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			wins, err := pf.Window.List(cmd.Context())
			if err != nil {
				return err
			}
			switch strings.ToLower(listOutputFlag) {
			case string(windowOutputPlain):
				printWindowListPlain(wins)
			case string(windowOutputJSON):
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(wins); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown output format %q", listOutputFlag)
			}
			return nil
		},
	}
	list.Flags().StringVar(&listOutputFlag, "output", listOutputFlag, "plain|json")

	activate := &cobra.Command{
		Use:   "activate <pattern>",
		Short: "Bring a window to the foreground by title substring (case-insensitive)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.Activate(cmd.Context(), args[0]); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
				return err
			}
			fmt.Printf("activated: %s\n", args[0])
			return nil
		},
	}

	active := &cobra.Command{
		Use:   "active",
		Short: "Print the title of the currently focused window",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			t, err := pf.Window.ActiveTitle(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Println(t)
			return nil
		},
	}

	var mvTitle string
	var mvX, mvY string
	move := &cobra.Command{
		Use:   "move",
		Short: "Move a window to absolute screen coordinates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			info, err := pf.Window.FindByTitle(cmd.Context(), mvTitle)
			if err != nil {
				return err
			}
			x, unchanged, err := parseOptionalIntToken(mvX)
			if err != nil {
				return err
			}
			if unchanged {
				x = info.X
			}
			y, unchanged, err := parseOptionalIntToken(mvY)
			if err != nil {
				return err
			}
			if unchanged {
				y = info.Y
			}
			if err := pf.Window.Move(cmd.Context(), mvTitle, x, y); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
				return err
			}
			fmt.Printf("moved %q to %d,%d\n", mvTitle, x, y)
			return nil
		},
	}
	move.Flags().StringVar(&mvTitle, "title", "", "window title substring (required)")
	move.Flags().StringVar(&mvX, "x", "keep", "x coordinate or keep")
	move.Flags().StringVar(&mvY, "y", "keep", "y coordinate or keep")
	_ = move.MarkFlagRequired("title")

	var rsTitle string
	var rsW, rsH int
	resize := &cobra.Command{
		Use:   "resize",
		Short: "Resize a window",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.Resize(cmd.Context(), rsTitle, rsW, rsH); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
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

	fullscreen := &cobra.Command{
		Use:   "fullscreen <title>",
		Short: "Fullscreen a window by title",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.Fullscreen(cmd.Context(), args[0]); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
				return err
			}
			fmt.Printf("fullscreen: %s\n", args[0])
			return nil
		},
	}

	unfullscreen := &cobra.Command{
		Use:   "unfullscreen <title>",
		Short: "Exit fullscreen for a window by title",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.Unfullscreen(cmd.Context(), args[0]); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
				return err
			}
			fmt.Printf("unfullscreen: %s\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(list, activate, active, move, resize, fullscreen, unfullscreen)

	var waitForPollFlag, waitForTimeoutFlag string
	find := &cobra.Command{
		Use:   "find [match-spec ...]",
		Short: "Find matching windows and print them",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			match, err := parseWindowMatchArgs(args)
			if err != nil {
				return err
			}
			wins, err := collectWindowMatches(cmd.Context(), pf.Window.Manager, match)
			if err != nil {
				return err
			}
			if len(wins) == 0 {
				return windowNotFoundError(match)
			}
			printWindowListPlain(wins)
			return nil
		},
	}

	waitFor := &cobra.Command{
		Use:   "wait-for [match-spec ...]",
		Short: "Wait until a matching window appears",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			match, err := parseWindowMatchArgs(args)
			if err != nil {
				return err
			}
			poll, err := parseDuration(waitForPollFlag, 100*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(waitForTimeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			w, err := waitForWindowMatch(ctx, pf.Window.Manager, match, poll)
			if err != nil {
				return err
			}
			printWindowPlain(w)
			return nil
		},
	}
	waitFor.Flags().StringVar(&waitForPollFlag, "poll", "100ms", "poll interval")
	waitFor.Flags().StringVar(&waitForTimeoutFlag, "timeout", "5s", "timeout duration")

	var waitClosePollFlag, waitCloseTimeoutFlag string
	waitClose := &cobra.Command{
		Use:   "wait-close [match-spec ...]",
		Short: "Wait until matching windows disappear",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			match, err := parseWindowMatchArgs(args)
			if err != nil {
				return err
			}
			poll, err := parseDuration(waitClosePollFlag, 100*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(waitCloseTimeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if err := waitForWindowCloseMatch(ctx, pf.Window.Manager, match, poll); err != nil {
				return err
			}
			return nil
		},
	}
	waitClose.Flags().StringVar(&waitClosePollFlag, "poll", "100ms", "poll interval")
	waitClose.Flags().StringVar(&waitCloseTimeoutFlag, "timeout", "5s", "timeout duration")

	var geomOutputFlag string
	getGeom := &cobra.Command{
		Use:   "get-geometry <title>",
		Short: "Print geometry for a window",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := parseOutputMode(geomOutputFlag)
			if err != nil {
				return err
			}
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			info, err := pf.Window.FindByTitle(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			switch mode {
			case outputModeJSON:
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"title": args[0],
					"geometry": map[string]int{
						"x": info.X,
						"y": info.Y,
						"w": info.W,
						"h": info.H,
					},
				})
			default:
				fmt.Fprintf(cmd.OutOrStdout(), "%d,%d,%d,%d\n", info.X, info.Y, info.X+info.W, info.Y+info.H)
				return nil
			}
		},
	}
	getGeom.Flags().StringVar(&geomOutputFlag, "output", "plain", "plain|json")

	// Manual wrappers for additional WindowBundle APIs
	findByTitle := &cobra.Command{
		Use:   "find-by-title <pattern>",
		Short: "Find a window by title and print info",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			info, err := pf.Window.FindByTitle(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Printf("0x%x\t%s\n", info.ID, info.Title)
			fmt.Printf("x=%d y=%d w=%d h=%d\n", info.X, info.Y, info.W, info.H)
			fmt.Printf("pid=%d\n", info.PID)
			return nil
		},
	}

	isVisible := &cobra.Command{
		Use:   "is-visible <title>",
		Short: "Return whether a window is visible",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			_, err = pf.Window.FindByTitle(cmd.Context(), args[0])
			if err == nil {
				fmt.Println("true")
			} else if errors.Is(err, window.ErrWindowNotFound) {
				fmt.Println("false")
			} else {
				return err
			}
			return nil
		},
	}

	watchOutputFlag := "plain"
	watch := &cobra.Command{
		Use:   "watch",
		Short: "Stream window list changes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := parseOutputMode(watchOutputFlag)
			if err != nil {
				return err
			}
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			ticker := time.NewTicker(250 * time.Millisecond)
			defer ticker.Stop()
			var last string
			enc := json.NewEncoder(cmd.OutOrStdout())
			ctx := cmd.Context()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
				}
				wins, err := pf.Window.List(ctx)
				if err != nil {
					return err
				}
				raw, err := json.Marshal(wins)
				if err != nil {
					return err
				}
				cur := string(raw)
				if cur == last {
					continue
				}
				last = cur
				if mode == outputModeJSON {
					if err := enc.Encode(map[string]any{"windows": wins, "count": len(wins)}); err != nil {
						return err
					}
					continue
				}
				printWindowListPlain(wins)
			}
		},
	}
	watch.Flags().StringVar(&watchOutputFlag, "output", "plain", "plain|json")

	cmd.AddCommand(find, waitFor, waitClose, watch, findByTitle, getGeom, isVisible)

	closeWin := &cobra.Command{
		Use:   "close <title>",
		Short: "Close a window by title",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.CloseWindow(cmd.Context(), args[0]); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
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
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.Minimize(cmd.Context(), args[0]); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
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
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			if err := pf.Window.Maximize(cmd.Context(), args[0]); err != nil {
				return err
			}
			if err := syncIf(pf); err != nil {
				return err
			}
			fmt.Printf("maximized: %s\n", args[0])
			return nil
		},
	}

	cmd.AddCommand(closeWin, minimize, maximize)

	// append auto-generated window commands (avoid duplicates)
	existing := map[string]bool{}
	for _, c := range cmd.Commands() {
		existing[c.Name()] = true
	}
	for _, ac := range autogenWindowCommands(openPF) {
		if !existing[ac.Name()] {
			cmd.AddCommand(ac)
		}
	}

	return cmd
}

// ── find ──────────────────────────────────────────────────────────────────────────────
