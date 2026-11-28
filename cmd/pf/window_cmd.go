package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/window"
	"github.com/spf13/cobra"
	"io"
	"math"
	"strings"
	"time"
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
	// Shell quoting is removed before Cobra receives arguments. Preserve each
	// shell argument as a single match token, including positional titles.
	parts := append([]string(nil), args...)
	for i, arg := range parts {
		if strings.ContainsAny(arg, " \t\n\r,") {
			parts[i] = `"` + strings.ReplaceAll(strings.ReplaceAll(arg, `\`, `\\`), `"`, `\"`) + `"`
		}
	}
	return window.ParseMatchSpec(strings.Join(parts, " "))
}

func collectWindowMatches(
	ctx context.Context,
	bundle perfuncted.WindowBundle,
	match window.Match,
) ([]window.Info, error) {
	handles, err := bundle.WithContext(ctx).List(match) //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
	if err != nil {
		return nil, err
	}
	matched := make([]window.Info, 0, len(handles))
	for _, handle := range handles {
		info, err := handle.WithContext(ctx).Info() //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
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
	bundle perfuncted.WindowBundle,
	match window.Match,
	poll time.Duration,
) (window.Info, error) {
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}
	handle, err := bundle.WithContext(ctx).Wait(match, perfuncted.WaitEvery(poll)) //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
	if err != nil {
		return window.Info{}, err
	}
	return handle.WithContext(ctx).Info() //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
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
	return session.WithContext(ctx).Wait( //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
		perfuncted.Not(perfuncted.WindowExists(match)),
		perfuncted.WaitEvery(poll),
	)
}

func findWindowByTitle(
	ctx context.Context,
	bundle perfuncted.WindowBundle,
	title string,
) (*perfuncted.Window, error) {
	return bundle.WithContext(ctx).Find( //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
		window.Match{TitleContains: title},
	)
}

// findWindowByStableID resolves a concrete native ID rather than a title. The
// ID is displayed by `window list`; UUIDs and numeric X11 identifiers both work.
func findWindowByStableID(ctx context.Context, bundle perfuncted.WindowBundle, id string) (*perfuncted.Window, error) {
	return resolveWindowID(ctx, bundle, id, false)
}

func resolveWindowID(ctx context.Context, bundle perfuncted.WindowBundle, id string, unique bool) (*perfuncted.Window, error) {
	if id == "" {
		return nil, fmt.Errorf("window ID must be non-empty: %w", window.ErrInvalidArgument)
	}
	wins, err := bundle.WithContext(ctx).List(window.Match{}) //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
	if err != nil {
		return nil, err
	}
	// Exact native IDs take priority across the entire snapshot. An opaque
	// native ID such as "010" must not be shadowed by a numeric alias for 10
	// that happens to appear earlier in the listing.
	var found *perfuncted.Window
	for _, win := range wins {
		if win.ID().String() == id {
			if !unique {
				return win, nil
			}
			if found != nil {
				return nil, fmt.Errorf("window id %q: %w", id, window.ErrWindowAmbiguous)
			}
			found = win
		}
	}
	if found != nil {
		return found, nil
	}
	number, numberErr := parseDecimalOrPrefixedID(id, 64)
	if numberErr == nil && number != 0 {
		for _, win := range wins {
			if win.CachedInfo().ID == number {
				if !unique {
					return win, nil
				}
				if found != nil {
					return nil, fmt.Errorf("window id %q: %w", id, window.ErrWindowAmbiguous)
				}
				found = win
			}
		}
	}
	if found != nil {
		return found, nil
	}
	return nil, fmt.Errorf("window id %q: %w", id, window.ErrWindowNotFound)
}

// windowSelectorArgs preserves the existing title syntax unless --id is
// explicitly supplied. Conflicts and empty IDs fail before opening a session.
func windowSelectorArgs(flagTitle bool) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if flagTitle {
			if err := cobra.NoArgs(cmd, args); err != nil {
				return err
			}
		} else if !cmd.Flags().Changed("id") {
			return cobra.ExactArgs(1)(cmd, args)
		}
		if !cmd.Flags().Changed("id") {
			if !cmd.Flags().Changed("title") {
				return fmt.Errorf("required flag(s) %q not set", "title")
			}
			return nil
		}
		if len(args) != 0 || (flagTitle && cmd.Flags().Changed("title")) {
			return fmt.Errorf("%w: choose either a title or --id", window.ErrInvalidArgument)
		}
		id, err := cmd.Flags().GetString("id")
		if err != nil {
			return err
		}
		if id == "" {
			return fmt.Errorf("%w: --id must be non-empty", window.ErrInvalidArgument)
		}
		return nil
	}
}

func windowSelectionTitle(cmd *cobra.Command, args []string) string {
	if cmd.Flags().Changed("id") {
		return ""
	}
	return args[0]
}

func resolveWindowSelection(ctx context.Context, bundle perfuncted.WindowBundle, title, id string) (*perfuncted.Window, error) {
	if id != "" {
		return resolveWindowID(ctx, bundle, id, true)
	}
	return findWindowByTitle(ctx, bundle, title)
}

func windowSelectionLabel(title, id string) string {
	if id != "" {
		return id
	}
	return title
}

// activateSelectedWindow shares the exact activation, synchronization, and
// optional focus-verification transaction for title and stable-ID selectors.
func activateSelectedWindow(
	cmd *cobra.Command,
	selection string,
	openPF sessionOpener,
	syncIf func(context.Context, *perfuncted.Session) error,
	find func(context.Context, perfuncted.WindowBundle, string) (*perfuncted.Window, error),
	verify bool,
	verifyTimeout, verifyPoll time.Duration,
) error {
	if verify && (verifyTimeout <= 0 || verifyPoll <= 0) {
		return fmt.Errorf("activation verification requires positive durations")
	}
	session, err := openPF(cmd.Context())
	if err != nil {
		return err
	}
	defer session.Close()
	target, err := find(cmd.Context(), session.Windows(), selection)
	if err != nil {
		return err
	}
	if err := target.WithContext(cmd.Context()).Activate(); err != nil {
		return err
	}
	if err := syncIf(cmd.Context(), session); err != nil {
		return err
	}
	if verify {
		if err := waitWindowActive(cmd.Context(), target, verifyTimeout, verifyPoll); err != nil {
			return err
		}
		return writeCLIOutput(cmd.OutOrStdout(), "activation verified: %s\n", selection)
	}
	return writeCLIOutput(cmd.OutOrStdout(), "activation requested: %s\n", selection)
}

//nolint:gocyclo // Cobra command assembly is intentionally centralized
func windowCmd(
	openPF sessionOpener,
	cfg *cliConfig,
) *cobra.Command {
	cmd := &cobra.Command{Use: "window", Short: "Window management"}
	syncIf := func(ctx context.Context, pf *perfuncted.Session) error {
		if cfg != nil && cfg.sync {
			return pf.Windows().WithContext(ctx).Sync() //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
		}
		return nil
	}
	listOutputFlag := string(windowOutputPlain)

	list := &cobra.Command{
		Use:   "list",
		Short: "List windows",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Reject unsupported output formats before session authorization.
			format := strings.ToLower(listOutputFlag)
			if format != string(windowOutputPlain) && format != string(windowOutputJSON) {
				return fmt.Errorf("unknown output format %q", listOutputFlag)
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			wins, err := collectWindowMatches(
				cmd.Context(),
				pf.Windows(),
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

	var verifyTitle bool
	var verifyTitleTimeout, verifyTitlePoll time.Duration
	activate := &cobra.Command{
		Use:   "activate <pattern>",
		Short: "Bring a window to the foreground by title substring (case-insensitive)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return activateSelectedWindow(cmd, args[0], openPF, syncIf, findWindowByTitle, verifyTitle, verifyTitleTimeout, verifyTitlePoll)
		},
	}

	activate.Flags().BoolVar(&verifyTitle, "verify", true, "verify actual window focus")
	activate.Flags().DurationVar(&verifyTitleTimeout, "verify-timeout", 2*time.Second, "maximum time to verify focus")
	activate.Flags().DurationVar(&verifyTitlePoll, "verify-poll", 50*time.Millisecond, "verification polling interval")

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
			t, err := pf.Windows().WithContext(cmd.Context()).ActiveTitle()
			if err != nil {
				return err
			}
			return writeCLIMessage(cmd.OutOrStdout(), t)
		},
	}

	var mvTitle, mvID string
	var mvX, mvY string
	move := &cobra.Command{
		Use:   "move",
		Short: "Move a window to absolute screen coordinates",
		Args:  windowSelectorArgs(true),
		RunE: func(cmd *cobra.Command, _ []string) error {
			x, keepX, err := parseOptionalIntToken(mvX)
			if err != nil {
				return err
			}
			y, keepY, err := parseOptionalIntToken(mvY)
			if err != nil {
				return err
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := resolveWindowSelection(cmd.Context(), pf.Windows(), mvTitle, mvID)
			if err != nil {
				return err
			}
			info, err := target.WithContext(cmd.Context()).Info()
			if err != nil {
				return err
			}
			if keepX {
				x = info.X
			}
			if keepY {
				y = info.Y
			}
			if err := target.WithContext(cmd.Context()).Move(x, y); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "moved %q to %d,%d\n", windowSelectionLabel(mvTitle, mvID), x, y)
		},
	}
	move.Flags().StringVar(&mvTitle, "title", "", "unique case-insensitive title substring (required unless --id)")
	move.Flags().StringVar(&mvX, "x", "keep", "x coordinate or keep")
	move.Flags().StringVar(&mvY, "y", "keep", "y coordinate or keep")
	move.Flags().StringVar(&mvID, "id", "", "stable window ID from window list (instead of --title)")

	var rsTitle, rsID string
	var rsW, rsH int
	resize := &cobra.Command{
		Use:   "resize",
		Short: "Resize a window",
		Args:  windowSelectorArgs(true),
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := resolveWindowSelection(cmd.Context(), pf.Windows(), rsTitle, rsID)
			if err != nil {
				return err
			}
			if err := target.WithContext(cmd.Context()).Resize(rsW, rsH); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "resized %q to %dx%d\n", windowSelectionLabel(rsTitle, rsID), rsW, rsH)
		},
	}
	resize.Flags().StringVar(&rsTitle, "title", "", "unique case-insensitive title substring (required unless --id)")
	resize.Flags().IntVar(&rsW, "w", 800, "width in pixels")
	resize.Flags().IntVar(&rsH, "h", 600, "height in pixels")
	resize.Flags().StringVar(&rsID, "id", "", "stable window ID from window list (instead of --title)")

	var fullscreenID string
	fullscreen := &cobra.Command{
		Use:   "fullscreen [title]",
		Short: "Fullscreen a window by title or stable ID",
		Args:  windowSelectorArgs(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := resolveWindowSelection(cmd.Context(), pf.Windows(), windowSelectionTitle(cmd, args), fullscreenID)
			if err != nil {
				return err
			}
			if err := target.WithContext(cmd.Context()).Fullscreen(); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "fullscreen: %s\n", windowSelectionLabel(windowSelectionTitle(cmd, args), fullscreenID))
		},
	}
	fullscreen.Flags().StringVar(&fullscreenID, "id", "", "stable window ID from window list (instead of positional title)")

	var unfullscreenID string
	unfullscreen := &cobra.Command{
		Use:   "unfullscreen [title]",
		Short: "Exit fullscreen for a window by title or stable ID",
		Args:  windowSelectorArgs(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := resolveWindowSelection(cmd.Context(), pf.Windows(), windowSelectionTitle(cmd, args), unfullscreenID)
			if err != nil {
				return err
			}
			if err := target.WithContext(cmd.Context()).Unfullscreen(); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "unfullscreen: %s\n", windowSelectionLabel(windowSelectionTitle(cmd, args), unfullscreenID))
		},
	}
	unfullscreen.Flags().StringVar(&unfullscreenID, "id", "", "stable window ID from window list (instead of positional title)")

	cmd.AddCommand(list, activate, active, move, resize, fullscreen, unfullscreen)
	cmd.AddCommand(kdeWindowExtraCommands(openPF)...)

	var waitForPollFlag, waitForTimeoutFlag string
	find := &cobra.Command{
		Use:   "find [match-spec ...]",
		Short: "Find matching windows and print them",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			match, err := parseWindowMatchArgs(args)
			if err != nil {
				return err
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			wins, err := collectWindowMatches(
				cmd.Context(),
				pf.Windows(),
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
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			w, err := waitForWindowMatch(ctx, pf.Windows(), match, poll)
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
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
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
	var getGeomID string
	getGeom := &cobra.Command{
		Use:   "get-geometry [title]",
		Short: "Print reported window geometry",
		Long:  "Select one window by unique case-insensitive title substring or --id. Plain output is x0,y0,x1,y1; JSON uses x,y,w,h in compositor-reported coordinates. For --id, JSON title is the reported window title.",
		Args:  windowSelectorArgs(false),
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
			target, err := resolveWindowSelection(cmd.Context(), pf.Windows(), windowSelectionTitle(cmd, args), getGeomID)
			if err != nil {
				return err
			}
			info, err := target.WithContext(cmd.Context()).Info()
			if err != nil {
				return err
			}
			title := info.Title
			if getGeomID == "" {
				title = args[0]
			}
			switch mode {
			case outputModeJSON:
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
					"title": title,
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
	getGeom.Flags().StringVar(&getGeomID, "id", "", "stable window ID from window list (instead of positional title)")

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
				pf.Windows(),
				args[0],
			)
			if err != nil {
				return err
			}
			info, err := target.WithContext(cmd.Context()).Info()
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
		Short: "Return whether a title identifies one discoverable window",
		Long:  "Checks window discovery, not whether pixels are unobstructed, on screen, or unminimized. Multiple matching windows return an ambiguity error.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			_, err = findWindowByTitle(
				cmd.Context(),
				pf.Windows(),
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
					pf.Windows(),
					window.Match{},
				)
				if err != nil {
					if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
						return nil
					}
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

	var verifyActivation bool
	var activationVerifyTimeout, activationVerifyPoll time.Duration
	activateID := &cobra.Command{Use: "activate-id <stable-id>", Short: "Activate a specific window even when titles are duplicated", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		return activateSelectedWindow(cmd, args[0], openPF, syncIf, findWindowByStableID, verifyActivation, activationVerifyTimeout, activationVerifyPoll)
	}}
	var verifyClose bool
	var verifyCloseTimeout, verifyClosePoll time.Duration
	closeID := &cobra.Command{Use: "close-id <stable-id>", Short: "Close a specific window even when titles are duplicated", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		pf, err := openPF(cmd.Context())
		if err != nil {
			return err
		}
		defer pf.Close()
		target, err := findWindowByStableID(cmd.Context(), pf.Windows(), args[0])
		if err != nil {
			return err
		}
		if err := target.WithContext(cmd.Context()).Close(); err != nil {
			return err
		}
		if err := syncIf(cmd.Context(), pf); err != nil {
			return err
		}
		if verifyClose {
			if err := waitWindowGone(cmd.Context(), target, verifyCloseTimeout, verifyClosePoll); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "close verified: %s\n", args[0])
		}
		return writeCLIOutput(cmd.OutOrStdout(), "close requested: %s\n", args[0])
	}}
	activateID.Flags().BoolVar(&verifyActivation, "verify", true, "wait for actual target focus confirmation")
	activateID.Flags().DurationVar(&activationVerifyTimeout, "verify-timeout", 2*time.Second, "maximum wait for target to become active")
	activateID.Flags().DurationVar(&activationVerifyPoll, "verify-poll", 50*time.Millisecond, "focus verification poll interval")
	closeID.Flags().BoolVar(&verifyClose, "verify", false, "verify target window disappears")
	closeID.Flags().DurationVar(&verifyCloseTimeout, "verify-timeout", 2*time.Second, "maximum time to verify disappearance")
	closeID.Flags().DurationVar(&verifyClosePoll, "verify-poll", 50*time.Millisecond, "verification polling interval")

	cmd.AddCommand(activateID, closeID)
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
				pf.Windows(),
				args[0],
			)
			if err != nil {
				return err
			}
			if err := target.WithContext(cmd.Context()).Close(); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "closed: %s\n", args[0])
		},
	}

	var minimizeID string
	minimize := &cobra.Command{
		Use:   "minimize [title]",
		Short: "Minimize a window by title or stable ID",
		Args:  windowSelectorArgs(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := resolveWindowSelection(cmd.Context(), pf.Windows(), windowSelectionTitle(cmd, args), minimizeID)
			if err != nil {
				return err
			}
			if err := target.WithContext(cmd.Context()).Minimize(); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "minimized: %s\n", windowSelectionLabel(windowSelectionTitle(cmd, args), minimizeID))
		},
	}
	minimize.Flags().StringVar(&minimizeID, "id", "", "stable window ID from window list (instead of positional title)")

	var maximizeID string
	maximize := &cobra.Command{
		Use:   "maximize [title]",
		Short: "Maximize a window by title or stable ID",
		Args:  windowSelectorArgs(false),
		RunE: func(cmd *cobra.Command, args []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			target, err := resolveWindowSelection(cmd.Context(), pf.Windows(), windowSelectionTitle(cmd, args), maximizeID)
			if err != nil {
				return err
			}
			if err := target.WithContext(cmd.Context()).Maximize(); err != nil {
				return err
			}
			if err := syncIf(cmd.Context(), pf); err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "maximized: %s\n", windowSelectionLabel(windowSelectionTitle(cmd, args), maximizeID))
		},
	}
	maximize.Flags().StringVar(&maximizeID, "id", "", "stable window ID from window list (instead of positional title)")

	cmd.AddCommand(closeWin, minimize, maximize)

	return cmd
}

// ── find ──────────────────────────────────────────────────────────────────────────────

// windowRelativePoint translates compositor-logical window client coordinates
// to the input backend's *declared* global space. Unknown coordinate spaces
// fail closed rather than silently clicking the wrong window.
func windowRelativePoint(w window.Info, x, y int, space input.CoordinateSpaceInfo) (int, int, error) {
	if err := space.Validate(); err != nil {
		return 0, 0, fmt.Errorf("window-relative input requires a declared coordinate space: %w", err)
	}
	if w.W <= 0 || w.H <= 0 || x < 0 || y < 0 || x >= w.W || y >= w.H {
		return 0, 0, fmt.Errorf("window-relative coordinates (%d,%d) outside window %dx%d: %w", x, y, w.W, w.H, window.ErrInvalidArgument)
	}
	px, py := float64(w.X)+float64(x), float64(w.Y)+float64(y)
	px *= space.ScaleX
	py *= space.ScaleY
	if math.IsInf(px, 0) || math.IsInf(py, 0) || px >= float64(math.MaxInt) || px <= float64(math.MinInt) || py >= float64(math.MaxInt) || py <= float64(math.MinInt) {
		return 0, 0, fmt.Errorf("window-relative point exceeds input coordinate range: %w", window.ErrInvalidArgument)
	}
	return int(math.Round(px)), int(math.Round(py)), nil
}

var errFocusNotVerified = errors.New("window activation not verified")

// waitWindowActive reports success only after the authoritative window manager
// confirms the chosen stable ID is active, not when an event is merely queued.
func waitWindowActive(ctx context.Context, w *perfuncted.Window, timeout, poll time.Duration) error {
	if timeout <= 0 || poll <= 0 {
		return fmt.Errorf("verification durations must be positive")
	}
	err := w.WithContext(ctx).WithTimeout(timeout).WaitActive(perfuncted.WaitEvery(poll)) //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("window %s never reported active: %w", w.ID().String(), errors.Join(errFocusNotVerified, err))
	}
	if err != nil {
		return fmt.Errorf("verify activation of window %s: %w", w.ID().String(), err)
	}
	return nil
}

// waitWindowGone confirms a dialog or window disappeared, which is stronger
// than observing that a close or Enter keystroke was merely sent. It also
// prevents restoring clipboard contents before an asynchronous paste is read.
func waitWindowGone(ctx context.Context, target *perfuncted.Window, timeout, poll time.Duration) error {
	if target == nil || target.ID().String() == "" || timeout <= 0 || poll <= 0 {
		return fmt.Errorf("window disappearance verification needs an ID and positive durations")
	}
	err := target.WithContext(ctx).WithTimeout(timeout).WaitClosed(perfuncted.WaitEvery(poll), perfuncted.WaitFailFast()) //nolint:contextcheck // The scoped handle binds ctx explicitly.
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("window %s remains open: %w", target.ID().String(), errors.Join(errWindowNotGone, err))
	}
	if err != nil {
		return fmt.Errorf("verify window %s disappeared: %w", target.ID().String(), err)
	}
	return nil
}

var errWindowNotGone = errors.New("window disappearance not verified")
