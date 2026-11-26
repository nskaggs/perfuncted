package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/find"
	"github.com/nskaggs/perfuncted/v2/screen"
	"github.com/nskaggs/perfuncted/v2/window"
	"github.com/spf13/cobra"
)

// singleOutputPhysicalResolution uses already-authoritative output metadata
// instead of requesting permission to capture private screen pixels. Multiple
// displays are intentionally left to the established screen backend because
// their physical sizes cannot be safely combined from logical coordinates.
func singleOutputPhysicalResolution(ctx context.Context, outputs perfuncted.OutputBundle) (int, int, bool) {
	if outputs == (perfuncted.OutputBundle{}) {
		return 0, 0, false
	}
	items, err := outputs.WithContext(ctx).List() //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
	if err != nil || ctx.Err() != nil || len(items) != 1 {
		return 0, 0, false
	}
	item := items[0]
	if !item.Available || item.ResolutionW <= 0 || item.ResolutionH <= 0 {
		return 0, 0, false
	}
	return item.ResolutionW, item.ResolutionH, true
}

type screenWindowSelector struct {
	kind  string
	id    string
	title string
	pid   int32
}

func parseScreenWindowSelector(cmd *cobra.Command, id, title string, pid int32, metadata string) (screenWindowSelector, bool, error) {
	changed := []string{"window-id", "window-title", "pid"}
	selected := make([]string, 0, len(changed))
	for _, flag := range changed {
		if cmd.Flags().Changed(flag) {
			selected = append(selected, flag)
		}
	}
	if len(selected) == 0 {
		if cmd.Flags().Changed("metadata") || metadata != "" {
			return screenWindowSelector{}, false, fmt.Errorf("%w: --metadata requires a window selector", perfuncted.ErrInvalidArgument)
		}
		return screenWindowSelector{}, false, nil
	}
	if len(selected) != 1 {
		return screenWindowSelector{}, false, fmt.Errorf("%w: choose exactly one of --window-id, --window-title, or --pid", perfuncted.ErrInvalidArgument)
	}
	if cmd.Flags().Changed("rect") {
		return screenWindowSelector{}, false, fmt.Errorf("%w: --rect cannot be combined with a window selector", perfuncted.ErrInvalidArgument)
	}
	if cmd.Flags().Changed("metadata") && (metadata == "" || metadata == "-") {
		return screenWindowSelector{}, false, fmt.Errorf("%w: --metadata requires a file path separate from stdout", perfuncted.ErrInvalidArgument)
	}
	return validateScreenWindowSelector(selected[0], id, title, pid)
}

func validateScreenWindowSelector(kind, id, title string, pid int32) (screenWindowSelector, bool, error) {
	selector := screenWindowSelector{kind: kind}
	switch selector.kind {
	case "window-id":
		if id == "" {
			return screenWindowSelector{}, false, fmt.Errorf("%w: --window-id must be non-empty", perfuncted.ErrInvalidArgument)
		}
		selector.id = id
	case "window-title":
		if title == "" {
			return screenWindowSelector{}, false, fmt.Errorf("%w: --window-title must be non-empty", perfuncted.ErrInvalidArgument)
		}
		selector.title = title
	case "pid":
		if pid <= 0 {
			return screenWindowSelector{}, false, fmt.Errorf("%w: --pid must be positive", perfuncted.ErrInvalidArgument)
		}
		selector.pid = pid
	}
	return selector, true, nil
}

func resolveScreenWindow(ctx context.Context, bundle perfuncted.WindowBundle, selector screenWindowSelector) (*perfuncted.Window, error) {
	switch selector.kind {
	case "window-id":
		return findWindowByStableID(ctx, bundle, selector.id)
	case "window-title":
		return bundle.WithContext(ctx).Find(window.Match{TitleExact: selector.title}) //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
	case "pid":
		pid := selector.pid
		return bundle.WithContext(ctx).Find(window.Match{PID: &pid}) //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
	default:
		return nil, fmt.Errorf("%w: unknown screen window selector", perfuncted.ErrInvalidArgument)
	}
}

func sameOutputPath(left, right string) bool {
	if left == "" || right == "" || left == "-" || right == "-" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil && rightErr == nil && filepath.Clean(leftAbs) == filepath.Clean(rightAbs) {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func writeWindowCaptureMetadata(ctx context.Context, capture screen.WindowCapture, path string) error {
	if path == "" || path == "-" {
		return fmt.Errorf("%w: metadata output must be a file path", perfuncted.ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".perfuncted-window-capture-*.json")
	if err != nil {
		return fmt.Errorf("create metadata %q: %w", path, err)
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(capture)
	closeErr := file.Close()
	if encodeErr != nil && closeErr != nil {
		return errors.Join(encodeErr, fmt.Errorf("close metadata %q: %w", path, closeErr))
	}
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return fmt.Errorf("close metadata %q: %w", path, closeErr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("publish metadata %q: %w", path, err)
	}
	return nil
}

//nolint:gocyclo // Cobra command assembly is intentionally centralized
func screenCmd(
	openPF sessionOpener,
	optionalOpeners ...sessionOpener,
) *cobra.Command {
	cmd := &cobra.Command{Use: "screen", Short: "Screen capture operations"}

	var grabRectFlag, grabOutFlag string
	var grabWindowIDFlag, grabWindowTitleFlag, grabMetadataFlag string
	var grabPIDFlag int32
	var grabBackgroundFlag bool

	grab := &cobra.Command{
		Use:   "grab",
		Short: "Capture a screen region or selected window as PNG",
		Long: `Capture a screen region, or select one window with exactly one of --window-id,
--window-title, or --pid. Window title matching is exact and case-insensitive;
PID selection fails if that process owns multiple windows. Window selectors
cannot be combined with --rect. Window capture uses the window manager's
reported bounds to crop the visible desktop composite, so overlapping windows
may appear. Minimized, clipped, or geometry-unknown targets are rejected.

With a window selector, --background requests the target's own X11 Composite
pixmap. It works only when an X11 compositor has already redirected the
mapped target; it never activates or redirects a window and never falls back
to a screen crop. Visibility state is reported separately. Other backends,
unmapped targets, and minimized targets return an explicit unsupported error.

Use --metadata FILE to write the window ID, screen rectangle, image pixel
bounds and scale, capture mode, geometry source, and occlusion note as JSON.
With --out -, PNG bytes stay on stdout and the human-readable bounds report
goes to stderr.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			selector, hasWindowSelector, err := parseScreenWindowSelector(cmd, grabWindowIDFlag, grabWindowTitleFlag, grabPIDFlag, grabMetadataFlag)
			if err != nil {
				return err
			}
			if grabBackgroundFlag && !hasWindowSelector {
				return fmt.Errorf("%w: --background requires a window selector", perfuncted.ErrInvalidArgument)
			}
			var r image.Rectangle
			if !hasWindowSelector {
				r, err = parseRect(grabRectFlag)
				if err != nil {
					return err
				}
			}
			inFlatpak := insideFlatpakSandbox()
			out := grabDestination(grabOutFlag, inFlatpak)
			if hasWindowSelector && sameOutputPath(out, grabMetadataFlag) {
				return fmt.Errorf("%w: PNG output and metadata output must use different paths", perfuncted.ErrInvalidArgument)
			}
			if out == "-" {
				if outputErr := refuseBinaryTerminal(cmd.OutOrStdout()); outputErr != nil {
					return outputErr
				}
			}
			captureOpen := openPF
			if hasWindowSelector && len(optionalOpeners) > 1 {
				captureOpen = optionalOpeners[1]
			}
			pf, err := captureOpen(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if hasWindowSelector {
				target, err := resolveScreenWindow(cmd.Context(), pf.Windows(), selector)
				if err != nil {
					return err
				}
				var capture screen.WindowCapture
				if out == "-" {
					if grabBackgroundFlag {
						capture, err = pf.Screen().WithContext(cmd.Context()).CaptureWindowBackground(target)
					} else {
						capture, err = pf.Screen().WithContext(cmd.Context()).CaptureWindow(target)
					}
					if err != nil {
						return err
					}
					if encodeErr := png.Encode(cmd.OutOrStdout(), capture.Image); encodeErr != nil {
						return encodeErr
					}
				} else {
					if grabBackgroundFlag {
						capture, err = pf.Screen().WithContext(cmd.Context()).CaptureWindowBackgroundTo(target, out)
					} else {
						capture, err = pf.Screen().WithContext(cmd.Context()).CaptureWindowTo(target, out)
					}
					if err != nil {
						return err
					}
				}
				if grabMetadataFlag != "" {
					if err := writeWindowCaptureMetadata(cmd.Context(), capture, grabMetadataFlag); err != nil {
						return err
					}
				}
				if err := writeCLIOutput(cmd.ErrOrStderr(), "window=%s screen_rect=%d,%d,%d,%d mode=%s state=%s geometry=%s occlusion=%s\n",
					capture.WindowID, capture.ScreenRect.Min.X, capture.ScreenRect.Min.Y,
					capture.ScreenRect.Max.X, capture.ScreenRect.Max.Y, capture.Mode,
					capture.State, capture.GeometrySource, capture.Occlusion); err != nil {
					return err
				}
				if out == "-" {
					return nil
				}
				if inFlatpak {
					if err := writeCLIOutput(cmd.ErrOrStderr(), "Flatpak: %s may be sandbox-private unless its directory was explicitly shared; use --out - > screenshot.png for a host file.\n", out); err != nil {
						return err
					}
				}
				return writeCLIMessage(cmd.OutOrStdout(), out)
			}
			if out == "-" {
				img, err := pf.Screen().WithContext(cmd.Context()).Grab(r)
				if err != nil {
					return err
				}
				return png.Encode(cmd.OutOrStdout(), img)
			}
			if err := pf.Screen().WithContext(cmd.Context()).CaptureRegion(r, out); err != nil {
				return err
			}
			if inFlatpak {
				if err := writeCLIOutput(cmd.ErrOrStderr(),
					"Flatpak: %s may be sandbox-private unless its directory was explicitly shared; use --out - > screenshot.png for a host file.\n", out); err != nil {
					return err
				}
			}
			return writeCLIMessage(cmd.OutOrStdout(), out)
		},
	}
	grab.Flags().StringVar(&grabRectFlag, "rect", "0,0,1920,1080", "x0,y0,x1,y1")
	grab.Flags().StringVar(&grabOutFlag, "out", "", "PNG output path or '-' for stdout (Flatpak defaults to stdout when redirected; native defaults to /tmp/pf-grab.png)")
	grab.Flags().StringVar(&grabWindowIDFlag, "window-id", "", "capture the visible screen rectangle of one stable window ID")
	grab.Flags().StringVar(&grabWindowTitleFlag, "window-title", "", "capture the visible screen rectangle of one exact case-insensitive window title")
	grab.Flags().Int32Var(&grabPIDFlag, "pid", 0, "capture the visible screen rectangle of one unique window owned by this process ID")
	grab.Flags().StringVar(&grabMetadataFlag, "metadata", "", "write window-capture provenance as JSON to a separate file")
	grab.Flags().BoolVar(&grabBackgroundFlag, "background", false, "capture the target's own X11 window pixmap without activating it")

	var checksumRectFlag string
	checksum := &cobra.Command{
		Use:   "hash",
		Short: "Print the CRC32 pixel hash of a screen region",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := parseRect(checksumRectFlag)
			if err != nil {
				return err
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			h, err := pf.Screen().WithContext(cmd.Context()).GrabRegionHash(r)
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%08x\n", h)
		},
	}
	checksum.Flags().StringVar(&checksumRectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")

	var px, py int
	pixel := &cobra.Command{
		Use:   "pixel",
		Short: "Print the RGB colour of a single pixel",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			c, err := pf.Screen().WithContext(cmd.Context()).GetPixel(px, py)
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "R=%d G=%d B=%d\n", c.R, c.G, c.B)
		},
	}
	pixel.Flags().IntVar(&px, "x", 0, "x coordinate")
	pixel.Flags().IntVar(&py, "y", 0, "y coordinate")

	cmd.AddCommand(grab, checksum, pixel)

	getAllPixels := &cobra.Command{
		Use:   "get-all-pixels",
		Short: "Capture the entire screen and output raw RGBA pixels to stdout",
		Args:  cobra.NoArgs,
		Long: `Captures the entire screen and writes the raw 8-bit RGBA pixel data
directly to stdout. Useful for piping into ffmpeg, imagemagick, or other tools.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			img, err := pf.Screen().WithContext(cmd.Context()).Grab()
			if err != nil {
				return err
			}
			if rgba, ok := img.(*image.RGBA); ok {
				return writePackedRGBAPixels(cmd.OutOrStdout(), rgba)
			}
			return fmt.Errorf("unexpected image type %T", img)
		},
	}

	var grabRegionRect, grabRegionOut string
	grabRegion := &cobra.Command{
		Use:   "grab-region",
		Short: "Capture a specific screen region",
		Args:  cobra.NoArgs,
		Long: `Captures a specific screen region and outputs it as a PNG.
If --out is not provided or is '-', the PNG data is written to stdout.
Otherwise, it is saved to the specified file.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := parseRect(grabRegionRect)
			if err != nil {
				return err
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if grabRegionOut != "" && grabRegionOut != "-" {
				// The bundle publishes PNGs atomically after successful encoding.
				// os.Create would truncate an existing screenshot before an
				// untrusted capture's encoding failure is even known.
				if captureErr := pf.Screen().WithContext(cmd.Context()).CaptureRegion(r, grabRegionOut); captureErr != nil {
					return captureErr
				}
				return writeCLIMessage(cmd.ErrOrStderr(), grabRegionOut)
			}
			img, err := pf.Screen().WithContext(cmd.Context()).Grab(r)
			if err != nil {
				return err
			}
			return png.Encode(cmd.OutOrStdout(), img)
		},
	}
	grabRegion.Flags().StringVar(&grabRegionRect, "rect", "0,0,1920,1080", "x0,y0,x1,y1")
	grabRegion.Flags().StringVar(&grabRegionOut, "out", "-", "output path or '-' for stdout")

	cmd.AddCommand(getAllPixels, grabRegion)

	var watchRectFlag, watchPollFlag, watchDurFlag string
	var watchOutputFlag string
	var watchChanges, watchIncludePixels bool
	watch := &cobra.Command{
		Use:   "watch",
		Short: "Continuously print hash changes in a screen region",
		Args:  cobra.NoArgs,
		Long: `Polls a screen region and prints a timestamped line whenever the pixel hash
changes. Useful for tuning poll intervals, spotting oscillating regions, and
understanding which parts of the screen change during an action.

Output format:
  <timestamp>  <hash>  <label>

With --changes --output json, emit metadata-only NDJSON from full-frame polling.
The initial frame and resyncs have no base frame; unchanged samples are silent.
Dirty rectangles are desktop bounds, capped at 256 before full-frame resync.
Geometry version starts at 1 and advances only when screen or image bounds change.
Format and rectangle-overflow resyncs retain the geometry version.
Negative poll intervals are rejected in changes mode.
With --include-pixels (requires --changes --output json), pixels contains PNG
payloads in base64, with image-space bounds, dimensions and capture scale.
Full frames contain the entire capture; deltas contain exact changed crops.
Pixel records are limited to 16 MiB serialized (including newline), and PNG
working dimensions to 16 Mi pixels and 16384 pixels per axis. Oversized
records fail before output. No pixels are encoded without explicit opt-in.

Runs until --duration expires or Ctrl+C.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode, err := parseOutputMode(watchOutputFlag)
			if err != nil {
				return err
			}
			if watchIncludePixels && (!watchChanges || mode != outputModeJSON) {
				return fmt.Errorf("--include-pixels requires --changes --output json")
			}
			if watchChanges && mode != outputModeJSON {
				return fmt.Errorf("--changes requires --output json")
			}
			r, err := parseRect(watchRectFlag)
			if err != nil {
				return err
			}
			poll, err := parseDuration(watchPollFlag, 100*time.Millisecond)
			if err != nil {
				return err
			}
			if watchChanges && poll < 0 {
				return fmt.Errorf("--changes requires a non-negative --poll")
			}
			if poll <= 0 {
				poll = 100 * time.Millisecond
			}
			dur, err := parseDuration(watchDurFlag, 0)
			if err != nil {
				return err
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			ctx := cmd.Context()
			var cancel context.CancelFunc
			if dur > 0 {
				ctx, cancel = context.WithTimeout(ctx, dur)
				defer cancel()
			}

			if watchChanges {
				return watchScreenChanges(ctx, cmd.OutOrStdout(), r, poll, func(ctx context.Context, r image.Rectangle) (screen.Capture, error) {
					return pf.Screen().WithContext(ctx).Capture(r) //nolint:contextcheck // The caller context is bound explicitly by the scoped API.
				}, watchIncludePixels)
			}
			var last uint32
			first := true
			start := time.Now()
			streak := 0
			enc := json.NewEncoder(cmd.OutOrStdout())
			timer := time.NewTimer(poll)
			stopAndDrainTimer(timer)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				default:
				}
				h, err := pf.Screen().WithContext(ctx).GrabRegionHash(r)
				if err != nil {
					if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
						return nil
					}
					return err
				}
				ts := time.Now().Format("15:04:05.000")
				switch {
				case first:
					if mode == outputModeJSON {
						if err := enc.Encode(map[string]any{"timestamp": ts, "hash": fmt.Sprintf("0x%08x", h), "event": "initial"}); err != nil {
							return err
						}
					} else {
						if err := writeCLIOutput(cmd.OutOrStdout(), "%s  0x%08x  (initial)\n", ts, h); err != nil {
							return err
						}
					}
					last = h
					first = false
					streak = 1
				case h != last:
					elapsed := time.Since(start)
					if mode == outputModeJSON {
						if err := enc.Encode(map[string]any{"timestamp": ts, "hash": fmt.Sprintf("0x%08x", h), "event": "change", "elapsed": elapsed.Round(time.Millisecond).String(), "stable": streak}); err != nil {
							return err
						}
					} else {
						if err := writeCLIOutput(cmd.OutOrStdout(), "%s  0x%08x  (+%s after %d stable)\n", ts, h, elapsed.Round(time.Millisecond), streak); err != nil {
							return err
						}
					}
					last = h
					start = time.Now()
					streak = 1
				default:
					streak++
				}
				timer.Reset(poll)
				select {
				case <-ctx.Done():
					return nil
				case <-timer.C:
				}
			}
		},
	}
	watch.Flags().StringVar(&watchRectFlag, "rect", "0,0,1920,1080", "x0,y0,x1,y1 region to monitor")
	watch.Flags().StringVar(&watchPollFlag, "poll", "100ms", "poll interval")
	watch.Flags().StringVar(&watchDurFlag, "duration", "", "stop after this duration (e.g. 10s); default runs until Ctrl+C")
	watch.Flags().StringVar(&watchOutputFlag, "output", "plain", "plain|json")
	watch.Flags().BoolVar(&watchChanges, "changes", false, "emit metadata-only dirty rectangles (requires --output json)")
	watch.Flags().BoolVar(&watchIncludePixels, "include-pixels", false, "include exact PNG crops (requires --changes --output json)")
	cmd.AddCommand(watch)

	openResolution := openPF
	if len(optionalOpeners) != 0 {
		openResolution = optionalOpeners[0]
	}
	resolution := &cobra.Command{
		Use:   "resolution",
		Short: "Print the screen resolution",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openResolution(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			if w, h, ok := singleOutputPhysicalResolution(cmd.Context(), pf.Outputs()); ok {
				return writeCLIOutput(cmd.OutOrStdout(), "%dx%d\n", w, h)
			}
			w, h, err := pf.Screen().WithContext(cmd.Context()).Resolution()
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%dx%d\n", w, h)
		},
	}

	cmd.AddCommand(resolution)

	var waitFnRectFlag, waitFnPollFlag, waitFnTimeoutFlag, waitFnPredicateFlag string
	waitForFn := &cobra.Command{
		Use:   "wait-for-fn",
		Short: "Wait for a screen region to satisfy a built-in predicate",
		Args:  cobra.NoArgs,
		Long: `Uses the library's WaitForFn helper with a small set of practical built-in
predicates. This is useful when the caller wants a higher-level readiness
check than a raw hash comparison.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := parseRect(waitFnRectFlag)
			if err != nil {
				return err
			}
			pred, err := screenPredicate(waitFnPredicateFlag)
			if err != nil {
				return err
			}
			poll, err := parseDuration(waitFnPollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(waitFnTimeoutFlag, 5*time.Second)
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
			img, err := pf.Screen().WithContext(ctx).WaitForFn(r, pred, poll)
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%08x\n", find.PixelHash(img, nil))
		},
	}
	waitForFn.Flags().StringVar(&waitFnRectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitForFn.Flags().StringVar(&waitFnPredicateFlag, "predicate", "non-empty", "built-in predicate: non-empty|opaque|non-zero")
	waitForFn.Flags().StringVar(&waitFnPollFlag, "poll", "50ms", "poll interval")
	waitForFn.Flags().StringVar(&waitFnTimeoutFlag, "timeout", "5s", "timeout duration")

	var settleRectFlag, settleStableFlag, settlePollFlag, settleTimeoutFlag string
	waitForSettle := &cobra.Command{
		Use:   "wait-for-settle",
		Short: "Wait for a screen region to change and then settle",
		Args:  cobra.NoArgs,
		Long: `Captures a baseline hash, yields to the caller's surrounding action model,
then waits for the region to change and become stable again. The CLI version
uses a no-op action so it still works as a pure readiness probe.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := parseRect(settleRectFlag)
			if err != nil {
				return err
			}
			stable, err := strconv.Atoi(strings.TrimSpace(settleStableFlag))
			if err != nil {
				return fmt.Errorf("invalid --stable %q: %w", settleStableFlag, err)
			}
			poll, err := parseDuration(settlePollFlag, 100*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(settleTimeoutFlag, 5*time.Second)
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
			h, err := pf.Screen().WithContext(ctx).WaitForSettle(r, func() error { return nil }, stable, poll)
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%08x\n", h)
		},
	}
	waitForSettle.Flags().StringVar(&settleRectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitForSettle.Flags().StringVar(&settleStableFlag, "stable", "3", "consecutive identical samples required")
	waitForSettle.Flags().StringVar(&settlePollFlag, "poll", "100ms", "poll interval")
	waitForSettle.Flags().StringVar(&settleTimeoutFlag, "timeout", "5s", "timeout duration")

	var multiPointsFlag, multiOutputFlag string
	getMultiplePixels := &cobra.Command{
		Use:   "get-multiple-pixels",
		Short: "Capture several pixels in one screen grab",
		Args:  cobra.NoArgs,
		Long: `Captures a bounding rectangle covering every requested point, then prints the
colour of each point in order. Use --output json for machine parsing, or the
plain format for quick interactive checks.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			points, err := parsePoints(multiPointsFlag)
			if err != nil {
				return err
			}
			if len(points) == 0 {
				return fmt.Errorf("--points must contain at least one x,y pair")
			}
			format := strings.ToLower(strings.TrimSpace(multiOutputFlag))
			if format != "" && format != "plain" && format != "json" {
				return fmt.Errorf("invalid --output %q: want plain or json", multiOutputFlag)
			}
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			cols, err := pf.Screen().WithContext(cmd.Context()).GetMultiplePixels(points)
			if err != nil {
				return err
			}
			type sample struct {
				X int   `json:"x"`
				Y int   `json:"y"`
				R uint8 `json:"r"`
				G uint8 `json:"g"`
				B uint8 `json:"b"`
				A uint8 `json:"a"`
			}
			out := make([]sample, len(points))
			for i, p := range points {
				c := cols[i]
				out[i] = sample{X: p.X, Y: p.Y, R: c.R, G: c.G, B: c.B, A: c.A}
			}
			switch format {
			case "", "plain":
				for _, s := range out {
					if err := writeCLIOutput(cmd.OutOrStdout(), "%d,%d R=%d G=%d B=%d A=%d\n", s.X, s.Y, s.R, s.G, s.B, s.A); err != nil {
						return err
					}
				}
			case "json":
				return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
			default:
				return fmt.Errorf("invalid --output %q: want plain or json", multiOutputFlag)
			}
			return nil
		},
	}
	getMultiplePixels.Flags().StringVar(&multiPointsFlag, "points", "", "semicolon-separated x,y pairs")
	getMultiplePixels.Flags().StringVar(&multiOutputFlag, "output", "plain", "plain|json")
	_ = getMultiplePixels.MarkFlagRequired("points")

	cmd.AddCommand(waitForFn, waitForSettle, getMultiplePixels)

	// append auto-generated screen commands (avoid duplicates)
	existing := map[string]bool{}
	for _, c := range cmd.Commands() {
		existing[c.Name()] = true
	}
	for _, ac := range autogenScreenCommands(openPF) {
		if !existing[ac.Name()] {
			cmd.AddCommand(ac)
		}
	}

	return cmd
}

// writePackedRGBAPixels emits exactly one tight RGBA row per image row.
// Captures may use padded strides and sub-images; copying Pix directly would
// expose padding bytes that are not pixels and may contain stale user data.
// Validate the entire layout before emitting any potentially partial output.
func writePackedRGBAPixels(dst io.Writer, img *image.RGBA) error {
	if img == nil {
		return fmt.Errorf("screen: invalid RGBA frame: nil capture")
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	if width <= 0 || height <= 0 || width > int(^uint(0)>>1)/4 {
		return fmt.Errorf("screen: invalid RGBA frame bounds %v", img.Bounds())
	}
	rowSize := width * 4
	if img.Stride < rowSize || len(img.Pix) < rowSize || (height-1) > (len(img.Pix)-rowSize)/img.Stride {
		return fmt.Errorf("screen: invalid RGBA frame stride=%d length=%d bounds=%v", img.Stride, len(img.Pix), img.Bounds())
	}
	for row := 0; row < height; row++ {
		offset := row * img.Stride
		if _, err := io.Copy(dst, bytes.NewReader(img.Pix[offset:offset+rowSize])); err != nil {
			return err
		}
	}
	return nil
}

func stopAndDrainTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

// ── input ────────────────────────────────────────────────────────────────────────────

// grabDestination never silently writes the Flatpak default into its private
// /tmp. Piping stdout to a host file works without widening sandbox grants.
func grabDestination(requested string, flatpak bool) string {
	if requested != "" {
		return requested
	}
	if flatpak {
		return "-"
	}
	return "/tmp/pf-grab.png"
}

func insideFlatpakSandbox() bool {
	_, err := os.Stat("/.flatpak-info")
	return err == nil
}

// Raw PNG on an interactive terminal can corrupt its state. Shell redirects
// and ordinary Go io.Writer buffers are safe destinations.
func refuseBinaryTerminal(w io.Writer) error {
	if f, ok := w.(*os.File); ok {
		info, err := f.Stat()
		if err != nil {
			return fmt.Errorf("inspect screenshot output: %w", err)
		}
		if info.Mode()&os.ModeCharDevice != 0 {
			return fmt.Errorf("PNG output is binary; redirect to a host file (pf screen grab > screenshot.png) or specify --out in a shared directory")
		}
	}
	return nil
}
