package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/spf13/cobra"
)

func screenCmd(openPF func() (*perfuncted.Perfuncted, error), cfg *cliConfig) *cobra.Command {
	cmd := &cobra.Command{Use: "screen", Short: "Screen capture operations"}

	var rectFlag, outFlag string

	grab := &cobra.Command{
		Use:   "grab",
		Short: "Capture a screen region and save as PNG",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			out := outFlag
			if out == "" {
				out = "/tmp/pf-grab.png"
			}
			if err := pf.Screen.CaptureRegion(cmd.Context(), r, out); err != nil {
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			h, err := pf.Screen.GrabRegionHash(cmd.Context(), r)
			if err != nil {
				return err
			}
			fmt.Printf("%08x\n", h)
			return nil
		},
	}
	checksum.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")

	var px, py int
	pixel := &cobra.Command{
		Use:   "pixel",
		Short: "Print the RGB colour of a single pixel",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			c, err := pf.Screen.GetPixel(cmd.Context(), px, py)
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

	getAllPixels := &cobra.Command{
		Use:   "get-all-pixels",
		Short: "Capture the entire screen and output raw RGBA pixels to stdout",
		Long: `Captures the entire screen and writes the raw 8-bit RGBA pixel data
directly to stdout. Useful for piping into ffmpeg, imagemagick, or other tools.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			img, err := pf.Screen.GetAllPixels(cmd.Context())
			if err != nil {
				return err
			}
			if rgba, ok := img.(*image.RGBA); ok {
				_, err = cmd.OutOrStdout().Write(rgba.Pix)
				return err
			}
			return fmt.Errorf("unexpected image type %T", img)
		},
	}

	var grabRegionRect, grabRegionOut string
	grabRegion := &cobra.Command{
		Use:   "grab-region",
		Short: "Capture a specific screen region",
		Long: `Captures a specific screen region and outputs it as a PNG.
If --out is not provided or is '-', the PNG data is written to stdout.
Otherwise, it is saved to the specified file.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(grabRegionRect)
			if err != nil {
				return err
			}
			img, err := pf.Screen.GrabRegion(cmd.Context(), r)
			if err != nil {
				return err
			}
			if grabRegionOut == "" || grabRegionOut == "-" {
				return png.Encode(cmd.OutOrStdout(), img)
			}
			f, err := os.Create(grabRegionOut)
			if err != nil {
				return err
			}
			defer f.Close()
			if err := png.Encode(f, img); err != nil {
				return err
			}
			fmt.Fprintln(os.Stderr, grabRegionOut)
			return nil
		},
	}
	grabRegion.Flags().StringVar(&grabRegionRect, "rect", "0,0,1920,1080", "x0,y0,x1,y1")
	grabRegion.Flags().StringVar(&grabRegionOut, "out", "-", "output path or '-' for stdout")

	cmd.AddCommand(getAllPixels, grabRegion)

	var watchRectFlag, watchPollFlag, watchDurFlag string
	var watchOutputFlag string
	watch := &cobra.Command{
		Use:   "watch",
		Short: "Continuously print hash changes in a screen region",
		Long: `Polls a screen region and prints a timestamped line whenever the pixel hash
changes. Useful for tuning poll intervals, spotting oscillating regions, and
understanding which parts of the screen change during an action.

Output format:
  <timestamp>  <hash>  <label>

Runs until --duration expires or Ctrl+C.`,
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
			r, err := parseRect(watchRectFlag)
			if err != nil {
				return err
			}
			poll, err := parseDuration(watchPollFlag, 100*time.Millisecond)
			if err != nil {
				return err
			}
			if poll <= 0 {
				poll = 100 * time.Millisecond
			}
			dur, err := parseDuration(watchDurFlag, 0)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			var cancel context.CancelFunc
			if dur > 0 {
				ctx, cancel = context.WithTimeout(ctx, dur)
				defer cancel()
			}

			var last uint32
			first := true
			start := time.Now()
			streak := 0
			enc := json.NewEncoder(cmd.OutOrStdout())
			for {
				select {
				case <-ctx.Done():
					return nil
				default:
				}
				h, err := pf.Screen.GrabRegionHash(ctx, r)
				if err != nil {
					return err
				}
				ts := time.Now().Format("15:04:05.000")
				if first {
					if mode == outputModeJSON {
						if err := enc.Encode(map[string]any{"timestamp": ts, "hash": fmt.Sprintf("0x%08x", h), "event": "initial"}); err != nil {
							return err
						}
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "%s  0x%08x  (initial)\n", ts, h)
					}
					last = h
					first = false
					streak = 1
				} else if h != last {
					elapsed := time.Since(start)
					if mode == outputModeJSON {
						if err := enc.Encode(map[string]any{"timestamp": ts, "hash": fmt.Sprintf("0x%08x", h), "event": "change", "elapsed": elapsed.Round(time.Millisecond).String(), "stable": streak}); err != nil {
							return err
						}
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "%s  0x%08x  (+%s after %d stable)\n", ts, h, elapsed.Round(time.Millisecond), streak)
					}
					last = h
					start = time.Now()
					streak = 1
				} else {
					streak++
				}
				timer := time.NewTimer(poll)
				select {
				case <-ctx.Done():
					timer.Stop()
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
	cmd.AddCommand(watch)

	resolution := &cobra.Command{
		Use:   "resolution",
		Short: "Print the screen resolution",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			w, h, err := pf.Screen.Resolution(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Printf("%dx%d\n", w, h)
			return nil
		},
	}

	cmd.AddCommand(resolution)

	var waitFnRectFlag, waitFnPollFlag, waitFnTimeoutFlag, waitFnPredicateFlag string
	waitForFn := &cobra.Command{
		Use:   "wait-for-fn",
		Short: "Wait for a screen region to satisfy a built-in predicate",
		Long: `Uses the library's WaitForFn helper with a small set of practical built-in
predicates. This is useful when the caller wants a higher-level readiness
check than a raw hash comparison.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
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
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			img, err := pf.Screen.WaitForFn(ctx, r, pred, poll)
			if err != nil {
				return err
			}
			fmt.Printf("%08x\n", find.PixelHash(img, nil))
			return nil
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
		Long: `Captures a baseline hash, yields to the caller's surrounding action model,
then waits for the region to change and become stable again. The CLI version
uses a no-op action so it still works as a pure readiness probe.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
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
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			h, err := pf.Screen.WaitForSettle(ctx, r, func() {}, stable, poll)
			if err != nil {
				return err
			}
			fmt.Printf("%08x\n", h)
			return nil
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
		Long: `Captures a bounding rectangle covering every requested point, then prints the
colour of each point in order. Use --output json for machine parsing, or the
plain format for quick interactive checks.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
			points, err := parsePoints(multiPointsFlag)
			if err != nil {
				return err
			}
			if len(points) == 0 {
				return fmt.Errorf("--points must contain at least one x,y pair")
			}
			cols, err := pf.Screen.GetMultiplePixels(cmd.Context(), points)
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
			switch strings.ToLower(strings.TrimSpace(multiOutputFlag)) {
			case "", "plain":
				for _, s := range out {
					fmt.Fprintf(cmd.OutOrStdout(), "%d,%d R=%d G=%d B=%d A=%d\n", s.X, s.Y, s.R, s.G, s.B, s.A)
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

// ── input ────────────────────────────────────────────────────────────────────────────
