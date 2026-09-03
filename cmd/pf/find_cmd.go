package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

//nolint:gocyclo // Cobra command assembly is intentionally centralized
func findCmd(
	openPF sessionOpener,
) *cobra.Command {
	cmd := &cobra.Command{Use: "find", Short: "Pixel scanning and wait utilities"}

	var waitForRectFlag, waitForHashFlag, waitForPollFlag, waitForTimeoutFlag string

	waitFor := &cobra.Command{
		Use:   "wait-for",
		Short: "Wait until a region's pixel hash equals the provided hash",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(waitForRectFlag)
			if err != nil {
				return err
			}
			want, err := parseHash(waitForHashFlag)
			if err != nil {
				return err
			}
			poll, err := parseDuration(waitForPollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(waitForTimeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			h, err := pf.Screen.WaitFor(ctx, r, want, poll)
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%08x\n", h)
		},
	}
	waitFor.Flags().StringVar(&waitForRectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitFor.Flags().StringVar(&waitForHashFlag, "hash", "", "target hash (decimal or 0xhex)")
	waitFor.Flags().StringVar(&waitForPollFlag, "poll", "50ms", "poll interval")
	waitFor.Flags().StringVar(&waitForTimeoutFlag, "timeout", "5s", "timeout duration")
	_ = waitFor.MarkFlagRequired("hash")

	var waitForChangeRectFlag, waitForChangeHashFlag, waitForChangePollFlag, waitForChangeTimeoutFlag string
	var waitForChangeCaptureInitial bool
	waitForChange := &cobra.Command{
		Use:   "wait-for-change",
		Short: "Wait until a region's pixel hash changes from an initial value",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(waitForChangeRectFlag)
			if err != nil {
				return err
			}
			poll, err := parseDuration(waitForChangePollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(waitForChangeTimeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			var initial uint32
			if waitForChangeCaptureInitial {
				if initial, err = pf.Screen.GrabRegionHash(ctx, r); err != nil {
					return err
				}
			} else {
				if initial, err = parseHash(waitForChangeHashFlag); err != nil {
					return err
				}
			}
			h, err := pf.Screen.WaitForChange(ctx, r, initial, poll)
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%08x\n", h)
		},
	}
	waitForChange.Flags().StringVar(&waitForChangeRectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitForChange.Flags().StringVar(&waitForChangeHashFlag, "initial", "", "initial hash (decimal or 0xhex)")
	waitForChange.Flags().BoolVar(&waitForChangeCaptureInitial, "capture-initial", false,
		"capture current region hash and wait for it to change")
	waitForChange.Flags().StringVar(&waitForChangePollFlag, "poll", "50ms", "poll interval")
	waitForChange.Flags().StringVar(&waitForChangeTimeoutFlag, "timeout", "5s", "timeout duration")
	waitForChange.MarkFlagsMutuallyExclusive("initial", "capture-initial")
	waitForChange.MarkFlagsOneRequired("initial", "capture-initial")

	var waitForNoChangeRectFlag, waitForNoChangePollFlag, waitForNoChangeTimeoutFlag string
	var waitForNoChangeStableCount int
	waitForNoChange := &cobra.Command{
		Use:   "wait-for-no-change",
		Short: "Wait until a region's pixel hash is stable for N consecutive samples",
		Args:  cobra.NoArgs,
		Long: `Polls a screen region until its pixel hash is unchanged for --stable consecutive
samples. Pairs with wait-for-change: use wait-for-change to detect when something
starts (e.g. navigation begins), then wait-for-no-change to detect when it finishes.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(waitForNoChangeRectFlag)
			if err != nil {
				return err
			}
			poll, err := parseDuration(waitForNoChangePollFlag, 200*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(waitForNoChangeTimeoutFlag, 30*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			h, err := pf.Screen.WaitForNoChange(
				ctx,
				r,
				waitForNoChangeStableCount,
				poll,
			)
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%08x\n", h)
		},
	}
	waitForNoChange.Flags().StringVar(&waitForNoChangeRectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitForNoChange.Flags().IntVar(&waitForNoChangeStableCount, "stable", 5,
		"consecutive identical samples required")
	waitForNoChange.Flags().StringVar(&waitForNoChangePollFlag, "poll", "200ms", "poll interval")
	waitForNoChange.Flags().StringVar(&waitForNoChangeTimeoutFlag, "timeout", "30s", "timeout duration")

	var scanForRectsFlag, scanForWantsFlag, scanForPollFlag, scanForTimeoutFlag string
	scanFor := &cobra.Command{
		Use:   "scan-for",
		Short: "Scan multiple regions until one matches its expected hash",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			rects, err := parseRects(scanForRectsFlag)
			if err != nil {
				return err
			}
			wants, err := parseWantHashes(scanForWantsFlag)
			if err != nil {
				return err
			}
			if len(rects) != len(wants) {
				return fmt.Errorf("len(rects)=%d != len(wants)=%d", len(rects), len(wants))
			}
			if len(rects) == 0 {
				return fmt.Errorf("scan-for requires at least one rect/hash pair")
			}
			poll, err := parseDuration(scanForPollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(scanForTimeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			res, err := pf.Screen.ScanFor(ctx, rects, wants, poll)
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "match %v -> %08x\n", res.Rect, res.Hash)
		},
	}
	scanFor.Flags().StringVar(&scanForRectsFlag, "rects", "", "semicolon-separated rects: x0,y0,x1,y1;...")
	scanFor.Flags().StringVar(&scanForWantsFlag, "wants", "", "comma-separated expected hashes")
	scanFor.Flags().StringVar(&scanForPollFlag, "poll", "50ms", "poll interval")
	scanFor.Flags().StringVar(&scanForTimeoutFlag, "timeout", "5s", "timeout duration")
	_ = scanFor.MarkFlagRequired("rects")
	_ = scanFor.MarkFlagRequired("wants")

	cmd.AddCommand(waitFor, waitForChange, waitForNoChange, scanFor)

	// Manual wrappers for additional Screen find APIs
	var vfRect, vfPoll, vfTimeout string
	var vfStable int
	waitForVisibleChange := &cobra.Command{
		Use:   "wait-for-visible-change",
		Short: "Wait until a region's visible content changes (useful for animations/loads)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(vfRect)
			if err != nil {
				return err
			}
			poll, err := parseDuration(vfPoll, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(vfTimeout, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			initial, err := pf.Screen.GrabRegionHash(ctx, r)
			if err != nil {
				return err
			}
			h, err := pf.Screen.WaitForChange(ctx, r, initial, poll)
			if err != nil {
				return err
			}
			if vfStable > 1 {
				h, err = pf.Screen.WaitForNoChange(
					ctx,
					r,
					vfStable,
					poll,
				)
				if err != nil {
					return err
				}
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%08x\n", h)
		},
	}
	waitForVisibleChange.Flags().StringVar(&vfRect, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitForVisibleChange.Flags().StringVar(&vfPoll, "poll", "50ms", "poll interval")
	waitForVisibleChange.Flags().StringVar(&vfTimeout, "timeout", "5s", "timeout duration")
	waitForVisibleChange.Flags().IntVar(&vfStable, "stable", 3, "consecutive identical samples required")

	var colorRectFlag, colorTargetFlag string
	var colorTolerance int
	findColor := &cobra.Command{
		Use:   "color",
		Short: "Find the first pixel matching a colour within tolerance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF(cmd.Context())
			if err != nil {
				return err
			}
			defer pf.Close()
			r, err := parseRect(colorRectFlag)
			if err != nil {
				return err
			}
			c, err := parseColor(colorTargetFlag)
			if err != nil {
				return err
			}
			pt, err := pf.Screen.FindColor(cmd.Context(), r, c, colorTolerance)
			if err != nil {
				return err
			}
			return writeCLIOutput(cmd.OutOrStdout(), "%d,%d\n", pt.X, pt.Y)
		},
	}
	findColor.Flags().StringVar(&colorRectFlag, "rect", "0,0,1920,1080", "search area x0,y0,x1,y1")
	findColor.Flags().StringVar(&colorTargetFlag, "color", "", "target colour as RRGGBB hex (required)")
	findColor.Flags().IntVar(&colorTolerance, "tolerance", 0, "per-channel tolerance (0-255)")
	_ = findColor.MarkFlagRequired("color")

	cmd.AddCommand(findColor, waitForVisibleChange)
	return cmd
}
