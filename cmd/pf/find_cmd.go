package main

import (
	"context"
	"fmt"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/spf13/cobra"
)

func findCmd(openPF func() (*perfuncted.Perfuncted, error)) *cobra.Command {
	cmd := &cobra.Command{Use: "find", Short: "Pixel scanning and wait utilities"}

	var (
		rectFlag       string
		rectsFlag      string
		wantsFlag      string
		hashFlag       string
		pollFlag       string
		timeoutFlag    string
		captureInitial bool
		stableCount    int
	)

	waitFor := &cobra.Command{
		Use:   "wait-for",
		Short: "Wait until a region's pixel hash equals the provided hash",
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
			want, err := parseHash(hashFlag)
			if err != nil {
				return err
			}
			poll, err := parseDuration(pollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(timeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			h, err := pf.Screen.WaitFor(ctx, r, want, poll)
			if err != nil {
				return err
			}
			fmt.Printf("%08x\n", h)
			return nil
		},
	}
	waitFor.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitFor.Flags().StringVar(&hashFlag, "hash", "", "target hash (decimal or 0xhex)")
	waitFor.Flags().StringVar(&pollFlag, "poll", "50ms", "poll interval")
	waitFor.Flags().StringVar(&timeoutFlag, "timeout", "5s", "timeout duration")
	_ = waitFor.MarkFlagRequired("hash")

	waitForChange := &cobra.Command{
		Use:   "wait-for-change",
		Short: "Wait until a region's pixel hash changes from an initial value",
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
			poll, err := parseDuration(pollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(timeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			var initial uint32
			if captureInitial {
				if initial, err = pf.Screen.GrabRegionHash(ctx, r); err != nil {
					return err
				}
			} else {
				if initial, err = parseHash(hashFlag); err != nil {
					return err
				}
			}
			h, err := pf.Screen.WaitForChange(ctx, r, initial, poll)
			if err != nil {
				return err
			}
			fmt.Printf("%08x\n", h)
			return nil
		},
	}
	waitForChange.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitForChange.Flags().StringVar(&hashFlag, "initial", "", "initial hash (decimal or 0xhex)")
	waitForChange.Flags().BoolVar(&captureInitial, "capture-initial", false,
		"capture current region hash and wait for it to change")
	waitForChange.Flags().StringVar(&pollFlag, "poll", "50ms", "poll interval")
	waitForChange.Flags().StringVar(&timeoutFlag, "timeout", "5s", "timeout duration")
	waitForChange.MarkFlagsMutuallyExclusive("initial", "capture-initial")
	waitForChange.MarkFlagsOneRequired("initial", "capture-initial")

	waitForNoChange := &cobra.Command{
		Use:   "wait-for-no-change",
		Short: "Wait until a region's pixel hash is stable for N consecutive samples",
		Long: `Polls a screen region until its pixel hash is unchanged for --stable consecutive
samples. Pairs with wait-for-change: use wait-for-change to detect when something
starts (e.g. navigation begins), then wait-for-no-change to detect when it finishes.`,
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
			poll, err := parseDuration(pollFlag, 200*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(timeoutFlag, 30*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			h, err := find.WaitForNoChange(ctx, pf.Screen.Screenshotter, r, stableCount, poll, nil)
			if err != nil {
				return err
			}
			fmt.Printf("%08x\n", h)
			return nil
		},
	}
	waitForNoChange.Flags().StringVar(&rectFlag, "rect", "0,0,100,100", "x0,y0,x1,y1")
	waitForNoChange.Flags().IntVar(&stableCount, "stable", 5,
		"consecutive identical samples required")
	waitForNoChange.Flags().StringVar(&pollFlag, "poll", "200ms", "poll interval")
	waitForNoChange.Flags().StringVar(&timeoutFlag, "timeout", "30s", "timeout duration")

	scanFor := &cobra.Command{
		Use:   "scan-for",
		Short: "Scan multiple regions until one matches its expected hash",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
			if err != nil {
				return err
			}
			defer pf.Close()
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
			if len(rects) == 0 {
				return fmt.Errorf("scan-for requires at least one rect/hash pair")
			}
			poll, err := parseDuration(pollFlag, 50*time.Millisecond)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(timeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			res, err := pf.Screen.ScanFor(ctx, rects, wants, poll)
			if err != nil {
				return err
			}
			fmt.Printf("match %v -> %08x\n", res.Rect, res.Hash)
			return nil
		},
	}
	scanFor.Flags().StringVar(&rectsFlag, "rects", "", "semicolon-separated rects: x0,y0,x1,y1;...")
	scanFor.Flags().StringVar(&wantsFlag, "wants", "", "comma-separated expected hashes")
	scanFor.Flags().StringVar(&pollFlag, "poll", "50ms", "poll interval")
	scanFor.Flags().StringVar(&timeoutFlag, "timeout", "5s", "timeout duration")
	_ = scanFor.MarkFlagRequired("rects")
	_ = scanFor.MarkFlagRequired("wants")

	cmd.AddCommand(waitFor, waitForChange, waitForNoChange, scanFor)

	// Manual wrappers for additional Screen find APIs
	var vfRect, vfPoll, vfTimeout string
	var vfStable int
	waitForVisibleChange := &cobra.Command{
		Use:   "wait-for-visible-change",
		Short: "Wait until a region's visible content changes (useful for animations/loads)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
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
				h, err = find.WaitForNoChange(ctx, pf.Screen.Screenshotter, r, vfStable, poll, nil)
				if err != nil {
					return err
				}
			}
			fmt.Printf("%08x\n", h)
			return nil
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
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := openPF()
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
			fmt.Printf("%d,%d\n", pt.X, pt.Y)
			return nil
		},
	}
	findColor.Flags().StringVar(&colorRectFlag, "rect", "0,0,1920,1080", "search area x0,y0,x1,y1")
	findColor.Flags().StringVar(&colorTargetFlag, "color", "", "target colour as RRGGBB hex (required)")
	findColor.Flags().IntVar(&colorTolerance, "tolerance", 0, "per-channel tolerance (0-255)")
	_ = findColor.MarkFlagRequired("color")

	cmd.AddCommand(findColor, waitForVisibleChange)
	return cmd
}
