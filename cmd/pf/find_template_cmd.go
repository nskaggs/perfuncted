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

func findTemplateCmd(openPF sessionOpener) *cobra.Command {
	var templatePath, rectFlag string
	var confidence float64
	var timeoutFlag string
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Find the best fuzzy match for a PNG template",
		Long: `Search a screen region for a PNG template and print its desktop position and
similarity score. The score is one minus the reference-alpha-weighted mean absolute
RGB channel difference, normalized to [0,1]; it is a similarity measure, not a
probability. Transparent template pixels are ignored and partial alpha weights a
pixel. Matching does not compensate for scale or rotation. For exact pixel matching,
the Go API provides find.LocateExact. The PNG must be no larger than 32 MiB and 2
million pixels, with neither dimension above 8192 pixels. The active capture backend
must verify that it captured the complete requested region.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			match, err := pf.Screen.FindTemplate(ctx, rect, template, confidence)
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
