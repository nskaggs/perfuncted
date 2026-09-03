package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func collectWindowMatches(
	ctx context.Context,
	bundle *perfuncted.WindowBundle,
	match window.Match,
) ([]window.Info, error) {
	handles, err := bundle.List(ctx, match)
	if err != nil {
		return nil, err
	}
	matched := make([]window.Info, 0, len(handles))
	for _, handle := range handles {
		info, err := handle.Info(ctx)
		if err != nil {
			return nil, err
		}
		matched = append(matched, info)
	}
	return matched, nil
}

func windowNotFoundError(match window.Match) error {
	return fmt.Errorf("window matching %q not found: %w", match.String(), window.ErrWindowNotFound)
}

func printWindowPlain(out io.Writer, w window.Info) error {
	id := w.StableID()
	if w.ID != 0 {
		id = fmt.Sprintf("0x%x", w.ID)
	}
	return writeCLIOutput(out, "%s\t%s\tapp_id=%s\tpid=%d\tactive=%t\tminimized=%t\tmaximized=%t\tfullscreen=%t\n",
		id, w.Title, w.AppID, w.PID, w.Active, w.Minimized, w.Maximized, w.Fullscreen)
}

func printWindowListPlain(out io.Writer, wins []window.Info) error {
	for _, w := range wins {
		if err := printWindowPlain(out, w); err != nil {
			return err
		}
	}
	return nil
}

func waitForWindowMatch(
	ctx context.Context,
	bundle *perfuncted.WindowBundle,
	match window.Match,
	poll time.Duration,
) (window.Info, error) {
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}
	handle, err := bundle.Wait(ctx, match, perfuncted.WaitEvery(poll))
	if err != nil {
		return window.Info{}, err
	}
	return handle.Info(ctx)
}

func waitForWindowCloseMatch(
	ctx context.Context,
	session *perfuncted.Session,
	match window.Match,
	poll time.Duration,
) error {
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}
	return session.Wait(
		ctx,
		perfuncted.Not(perfuncted.WindowExists(match)),
		perfuncted.WaitEvery(poll),
	)
}

func findWindowByTitle(
	ctx context.Context,
	bundle *perfuncted.WindowBundle,
	title string,
) (*perfuncted.Window, error) {
	return bundle.Find(
		ctx,
		perfuncted.WindowMatch{TitleContains: title},
	)
}

//nolint:gocyclo // Cobra command assembly is intentionally centralized
func windowCmd(
	openPF sessionOpener,
	cfg *cliConfig,
) *cobra.Command {
	cmd := &cobra.Command{Use: "window", Short: "Window management"}
	syncIf := func(ctx context.Context, pf *perfuncted.Session) error {
		if cfg != nil && cfg.sync {
			return pf.Windows.Sync(ctx)
		}
		return nil
	}
	listOutputFlag := string(windowOutputPlain)

	list := &cobra.Command{
		Use:   "list",
		Short: "List windows",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			wins, err := collectWindowMatches(
				cmd.Context(),
				pf.Windows,
				window.Match{},
			)
			if err != nil {
				return err
			}
			switch strings.ToLower(listOutputFlag) {
			case string(windowOutputPlain):
				return printWindowListPlain(cmd.OutOrStdout(), wins)
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
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := findWindowByTitle(
				cmd.Context(),
				pf.Windows,
				args[0],
			)
			if err != nil {
				return err
			}
			if err := target.Activate(cmd.Context()); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "activated: %s\n", args[0])
		},
	}

	active := &cobra.Command{
		Use:   "active",
		Short: "Print the title of the currently focused window",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			t, err := pf.Windows.ActiveTitle(cmd.Context())
			if err != nil {
				return err
			}
			return writeCLIMessage(cmd.OutOrStdout(), t)
		},
	}

	var mvTitle string
	var mvX, mvY string
	move := &cobra.Command{
		Use:   "move",
		Short: "Move a window to absolute screen coordinates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := findWindowByTitle(cmd.Context(), pf.Windows, mvTitle)
			if err != nil {
				return err
			}
			info, err := target.Info(cmd.Context())
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
			if err := target.Move(cmd.Context(), x, y); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "moved %q to %d,%d\n", mvTitle, x, y)
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
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := findWindowByTitle(
				cmd.Context(),
				pf.Windows,
				rsTitle,
			)
			if err != nil {
				return err
			}
			if err := target.Resize(cmd.Context(), rsW, rsH); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "resized %q to %dx%d\n", rsTitle, rsW, rsH)
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
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := findWindowByTitle(
				cmd.Context(),
				pf.Windows,
				args[0],
			)
			if err != nil {
				return err
			}
			if err := target.Fullscreen(cmd.Context()); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "fullscreen: %s\n", args[0])
		},
	}

	unfullscreen := &cobra.Command{
		Use:   "unfullscreen <title>",
		Short: "Exit fullscreen for a window by title",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := findWindowByTitle(
				cmd.Context(),
				pf.Windows,
				args[0],
			)
			if err != nil {
				return err
			}
			if err := target.Unfullscreen(cmd.Context()); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "unfullscreen: %s\n", args[0])
		},
	}

	cmd.AddCommand(list, activate, active, move, resize, fullscreen, unfullscreen)

	var waitForPollFlag, waitForTimeoutFlag string
	find := &cobra.Command{
		Use:   "find [match-spec ...]",
		Short: "Find matching windows and print them",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			match, err := parseWindowMatchArgs(args)
			if err != nil {
				return err
			}
			wins, err := collectWindowMatches(
				cmd.Context(),
				pf.Windows,
				match,
			)
			if err != nil {
				return err
			}
			if len(wins) == 0 {
				return windowNotFoundError(match)
			}
			return printWindowListPlain(cmd.OutOrStdout(), wins)
		},
	}

	waitFor := &cobra.Command{
		Use:   "wait-for [match-spec ...]",
		Short: "Wait until a matching window appears",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
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
			w, err := waitForWindowMatch(ctx, pf.Windows, match, poll)
			if err != nil {
				return err
			}
			return printWindowPlain(cmd.OutOrStdout(), w)
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
			pf, err := openPF(cmd.Context())
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
			if err := waitForWindowCloseMatch(ctx, pf, match, poll); err != nil {
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
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := findWindowByTitle(
				cmd.Context(),
				pf.Windows,
				args[0],
			)
			if err != nil {
				return err
			}
			info, err := target.Info(cmd.Context())
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
				return writeCLIOutput(cmd.OutOrStdout(), "%d,%d,%d,%d\n", info.X, info.Y, info.X+info.W, info.Y+info.H)
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
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := findWindowByTitle(
				cmd.Context(),
				pf.Windows,
				args[0],
			)
			if err != nil {
				return err
			}
			info, err := target.Info(cmd.Context())
			if err != nil {
				return err
			}
			if err := writeCLIOutput(cmd.OutOrStdout(), "0x%x\t%s\n", info.ID, info.Title); err != nil {
				return err
			}
			if err := writeCLIOutput(cmd.OutOrStdout(), "x=%d y=%d w=%d h=%d\n", info.X, info.Y, info.W, info.H); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "pid=%d\n", info.PID)
		},
	}

	isVisible := &cobra.Command{
		Use:   "is-visible <title>",
		Short: "Return whether a window is visible",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			_, err = findWindowByTitle(
				cmd.Context(),
				pf.Windows,
				args[0],
			)
			switch {
			case err == nil:
				return writeCLIMessage(cmd.OutOrStdout(), "true")
			case errors.Is(err, window.ErrWindowNotFound):
				return writeCLIMessage(cmd.OutOrStdout(), "false")
			default:
				return err
			}
		},
	}

	watchOutputFlag := "plain"
	watch := &cobra.Command{
		Use:   "watch",
		Short: "Stream window list changes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := parseOutputMode(watchOutputFlag)
			if err != nil {
				return err
			}
			pf, err := openPF(cmd.Context())
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
				wins, err := collectWindowMatches(
					ctx,
					pf.Windows,
					window.Match{},
				)
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
				if err := printWindowListPlain(cmd.OutOrStdout(), wins); err != nil {
					return err
				}
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
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := findWindowByTitle(
				cmd.Context(),
				pf.Windows,
				args[0],
			)
			if err != nil {
				return err
			}
			if err := target.Close(cmd.Context()); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "closed: %s\n", args[0])
		},
	}

	minimize := &cobra.Command{
		Use:   "minimize <title>",
		Short: "Minimize a window by title",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := findWindowByTitle(
				cmd.Context(),
				pf.Windows,
				args[0],
			)
			if err != nil {
				return err
			}
			if err := target.Minimize(cmd.Context()); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "minimized: %s\n", args[0])
		},
	}

	maximize := &cobra.Command{
		Use:   "maximize <title>",
		Short: "Maximize a window by title",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := findWindowByTitle(
				cmd.Context(),
				pf.Windows,
				args[0],
			)
			if err != nil {
				return err
			}
			if err := target.Maximize(cmd.Context()); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "maximized: %s\n", args[0])
		},
	}

	cmd.AddCommand(closeWin, minimize, maximize)

	return cmd
}

// ── find ──────────────────────────────────────────────────────────────────────────────
