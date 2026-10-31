package main

import (
	"fmt"
	"github.com/nskaggs/perfuncted/output"
	"math/big"
)

// validScreenshotPoint requires geometry from one authoritative output and
// rejects negative and out-of-bounds positions before coordinate arithmetic.
func validScreenshotPoint(info output.Info, x, y int) bool {
	g := info.Geometry
	return g.W > 0 && g.H > 0 && info.ResolutionW > 0 && info.ResolutionH > 0 && x >= 0 && y >= 0 && x < info.ResolutionW && y < info.ResolutionH
}

// screenshotPointToLogical maps physical pixels from a full-screen screenshot
// into compositor-logical coordinates. The global physical layout of multiple
// fractional-scale outputs cannot be inferred safely from per-output metadata;
// fail closed rather than clicking an unrelated window.
func screenshotPointToLogical(outputs []output.Info, x, y int) (int, int, error) {
	var active []output.Info
	for _, o := range outputs {
		if o.Available {
			active = append(active, o)
		}
	}
	if len(active) != 1 {
		return 0, 0, fmt.Errorf("physical screenshot coordinate conversion requires exactly one active output (found %d)", len(active))
	}
	o := active[0]
	g := o.Geometry
	if !validScreenshotPoint(o, x, y) {
		return 0, 0, fmt.Errorf("invalid screenshot point or missing authoritative output geometry")
	}
	// Round local output-pixel ratios before translating to global logical
	// coordinates. Exact integer arithmetic avoids float cancellation and
	// signed rounding surprises at fractional scale and negative origins.
	roundAxis := func(pixel, logical, physical, origin int) (int, error) {
		product := new(big.Int).Mul(big.NewInt(int64(pixel)), big.NewInt(int64(logical)))
		quotient, remainder := new(big.Int).QuoRem(product, big.NewInt(int64(physical)), new(big.Int))
		if remainder.Mul(remainder, big.NewInt(2)).Cmp(big.NewInt(int64(physical))) >= 0 {
			quotient.Add(quotient, big.NewInt(1))
		}
		// Nearest-neighbor rounding can select the pixel beyond the far
		// edge of a downscaled output (e.g. 10,000 physical -> 1 logical).
		// Never send an out-of-bounds point to an input backend.
		if quotient.Cmp(big.NewInt(int64(logical))) >= 0 {
			quotient.SetInt64(int64(logical) - 1)
		}
		quotient.Add(quotient, big.NewInt(int64(origin)))
		if !quotient.IsInt64() {
			return 0, fmt.Errorf("screen coordinate conversion out of range")
		}
		n := quotient.Int64()
		if int64(int(n)) != n {
			return 0, fmt.Errorf("screen coordinate conversion out of range")
		}
		return int(n), nil
	}
	lx, err := roundAxis(x, g.W, o.ResolutionW, g.X)
	if err != nil {
		return 0, 0, err
	}
	ly, err := roundAxis(y, g.H, o.ResolutionH, g.Y)
	if err != nil {
		return 0, 0, err
	}
	return lx, ly, nil
}
