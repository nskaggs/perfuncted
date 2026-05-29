package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"strconv"
	"strings"
	"time"
)

func parseDuration(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return v, nil
}

type outputMode string

const (
	outputModePlain outputMode = "plain"
	outputModeJSON  outputMode = "json"
)

func parseOutputMode(raw string) (outputMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(outputModePlain):
		return outputModePlain, nil
	case string(outputModeJSON):
		return outputModeJSON, nil
	default:
		return "", fmt.Errorf("invalid --output %q: want plain or json", raw)
	}
}

func parseHash(s string) (uint32, error) {
	v, err := strconv.ParseUint(s, 0, 32) // handles 0x prefix + decimal
	if err == nil {
		return uint32(v), nil
	}
	v, err = strconv.ParseUint(s, 16, 32) // fallback for raw hex like "ab12cd"
	if err != nil {
		return 0, fmt.Errorf("invalid hash %q: %w", s, err)
	}
	return uint32(v), nil
}

func parseWantHashes(s string) ([]uint32, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		h, err := parseHash(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

func parseRects(s string) ([]image.Rectangle, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ";")
	out := make([]image.Rectangle, 0, len(parts))
	for _, p := range parts {
		r, err := parseRect(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func parsePoints(s string) ([]image.Point, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ";")
	out := make([]image.Point, 0, len(parts))
	for _, p := range parts {
		pt, err := parsePoint(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		out = append(out, pt)
	}
	return out, nil
}

func parseRect(s string) (image.Rectangle, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return image.Rectangle{}, fmt.Errorf("--rect must be x0,y0,x1,y1; got %q", s)
	}
	vals := make([]int, 4)
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return image.Rectangle{}, fmt.Errorf("--rect: invalid number %q", p)
		}
		vals[i] = v
	}
	return image.Rect(vals[0], vals[1], vals[2], vals[3]), nil
}

func parsePoint(s string) (image.Point, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 2 {
		return image.Point{}, fmt.Errorf("--points must be semicolon-separated x,y pairs; got %q", s)
	}
	x, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return image.Point{}, fmt.Errorf("--points: invalid x %q", parts[0])
	}
	y, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return image.Point{}, fmt.Errorf("--points: invalid y %q", parts[1])
	}
	return image.Pt(x, y), nil
}

func screenPredicate(name string) (func(context.Context, image.Image) bool, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "non-empty", "non-zero":
		return func(_ context.Context, img image.Image) bool {
			bounds := img.Bounds()
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					r, g, blue, a := img.At(x, y).RGBA()
					if r|g|blue|a != 0 {
						return true
					}
				}
			}
			return false
		}, nil
	case "opaque":
		return func(_ context.Context, img image.Image) bool {
			bounds := img.Bounds()
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					_, _, _, a := img.At(x, y).RGBA()
					if a != 0 {
						return true
					}
				}
			}
			return false
		}, nil
	default:
		return nil, fmt.Errorf("invalid --predicate %q: want non-empty, non-zero, or opaque", name)
	}
}

// parseColor parses a hex colour string like "ff0000" or "FF0000" into color.RGBA.
func parseColor(s string) (color.RGBA, error) {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return color.RGBA{}, fmt.Errorf("--color must be 6-digit hex RRGGBB; got %q", s)
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("--color: invalid hex %q: %w", s, err)
	}
	return color.RGBA{R: b[0], G: b[1], B: b[2], A: 0xff}, nil
}

func parseOptionalIntToken(raw string) (value int, unchanged bool, err error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "-", "keep", "same", "unchanged":
		return 0, true, nil
	default:
		v, err := strconv.Atoi(raw)
		if err != nil {
			return 0, false, err
		}
		return v, false, nil
	}
}
