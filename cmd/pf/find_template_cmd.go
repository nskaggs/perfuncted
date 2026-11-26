package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"time"

	"github.com/spf13/cobra"
)

const (
	maxTemplatePNGBytes  = 32 << 20
	maxTemplatePNGWidth  = 8192
	maxTemplatePNGHeight = 8192
	maxTemplatePNGArea   = 2 * 1024 * 1024
)

//nolint:gocyclo // Cobra validation and both output modes share a single command lifecycle.
func findTemplateCmd(openPF sessionOpener) *cobra.Command {
	var templatePath, rectFlag string
	var confidence float64
	var timeoutFlag string
	var all bool
	var maxResults int
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Find fuzzy matches for a PNG template",
		Long: `Search a screen region for a PNG template and print its desktop position and
similarity score. The score is one minus the reference-alpha-weighted mean absolute
RGB channel difference, normalized to [0,1]; it is a similarity measure, not a
probability. Transparent template pixels are ignored and partial alpha weights a
pixel. Matching does not compensate for scale or rotation. For exact pixel matching,
the Go API provides find.LocateExact. The PNG must be no larger than 32 MiB and 2
million pixels, with neither dimension above 8192 pixels. The active capture backend
must verify that it captured the complete requested region.
With --all, print up to --max-results non-overlapping matches (default 64)
in descending score order, ties top-to-bottom then left-to-right. Overlapping
candidates are greedily suppressed by earlier matches. Each match uses the
same output line format as best-match mode. No matches prints no lines.
The limit bounds output and candidate retention, but the exact greedy ordering
may require more than --max-results candidates in memory. In the worst allowed
16-Mpixel capture, this can require up to 256 MiB of additional retained heap
storage plus allocation overhead. The full search is scanned once.`,
		Example: "pf find image --template button.png --rect 0,0,1280,720 --confidence 0.9\npf find image --template button.png --rect 0,0,1280,720 --confidence 0.9 --all --max-results 10",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if all && (maxResults < 1 || maxResults > 64) {
				return fmt.Errorf("--max-results must be in [1,64]")
			}
			if !all && cmd.Flags().Changed("max-results") {
				return fmt.Errorf("--max-results requires --all")
			}
			if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 1 {
				return fmt.Errorf("--confidence must be finite and in [0,1], got %g", confidence)
			}
			rect, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			timeout, err := parseDuration(timeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			template, err := decodeTemplatePNG(templatePath)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			pf, err := openPF(ctx)
			if err != nil {
				return err
			}
			defer pf.Close()
			if all {
				matches, matchErr := pf.Screen().WithContext(ctx).FindTemplates(rect, template, confidence, maxResults)
				if matchErr != nil {
					return matchErr
				}
				for _, match := range matches {
					if outputErr := writeCLIOutput(cmd.OutOrStdout(), "match x=%d y=%d width=%d height=%d confidence=%.6f\n", match.Rect.Min.X, match.Rect.Min.Y, match.Rect.Dx(), match.Rect.Dy(), match.Confidence); outputErr != nil {
						return outputErr
					}
				}
				return nil
			}
			match, err := pf.Screen().WithContext(ctx).FindTemplate(rect, template, confidence)
			if err != nil {
				return err
			}
			return writeCLIOutput(
				cmd.OutOrStdout(),
				"match x=%d y=%d width=%d height=%d confidence=%.6f\n",
				match.Rect.Min.X,
				match.Rect.Min.Y,
				match.Rect.Dx(),
				match.Rect.Dy(),
				match.Confidence,
			)
		},
	}
	cmd.Flags().StringVar(&templatePath, "template", "", "PNG template image to find")
	cmd.Flags().StringVar(&rectFlag, "rect", "", "search region as x0,y0,x1,y1")
	cmd.Flags().Float64Var(&confidence, "confidence", 0.95, "minimum similarity score (0..1)")
	cmd.Flags().BoolVar(&all, "all", false, "return non-overlapping matches in score order")
	cmd.Flags().IntVar(&maxResults, "max-results", 64, "maximum matches with --all (1..64)")
	cmd.Flags().StringVar(&timeoutFlag, "timeout", "5s", "capture and matching timeout")
	_ = cmd.MarkFlagRequired("template")
	_ = cmd.MarkFlagRequired("rect")
	return cmd
}

func decodeTemplatePNG(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open template PNG: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat template PNG: %w", err)
	}
	if info.Size() > maxTemplatePNGBytes {
		return nil, fmt.Errorf("template PNG exceeds %d-byte limit", maxTemplatePNGBytes)
	}

	data, err := io.ReadAll(io.LimitReader(file, maxTemplatePNGBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read template PNG: %w", err)
	}
	if len(data) > maxTemplatePNGBytes {
		return nil, fmt.Errorf("template PNG exceeds %d-byte limit", maxTemplatePNGBytes)
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode template PNG header: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 ||
		config.Width > maxTemplatePNGWidth || config.Height > maxTemplatePNGHeight ||
		uint64(config.Width)*uint64(config.Height) > maxTemplatePNGArea {
		return nil, fmt.Errorf("template PNG dimensions %dx%d exceed supported limits", config.Width, config.Height)
	}
	template, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode template PNG: %w", err)
	}
	if template.Bounds().Dx() != config.Width || template.Bounds().Dy() != config.Height {
		return nil, fmt.Errorf("template PNG dimensions changed while decoding")
	}
	return template, nil
}
