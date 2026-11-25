// Package motion implements bounded deterministic pointer interpolation.
package motion

import (
	"fmt"
	"math"
	"time"
)

type Options struct {
	Duration  time.Duration
	Easing    string  // "", "linear", or "ease-in-out"
	Curve     float64 // perpendicular bend fraction, -0.5..0.5
	Variation int     // seeded coordinate variation in backend units, 0..16
	Seed      uint64
}
type Point struct{ X, Y int }

const (
	motionInterval    = time.Second / 60
	motionMaxDuration = 10 * time.Second
	motionMaxSamples  = 120
)

func (o Options) Validate() error {
	if o.Duration < 0 || o.Duration > motionMaxDuration {
		return fmt.Errorf("input: duration must be between 0 and %s", motionMaxDuration)
	}
	if o.Easing != "" && o.Easing != "linear" && o.Easing != "ease-in-out" {
		return fmt.Errorf("input: invalid easing %q", o.Easing)
	}
	if math.IsNaN(o.Curve) || math.IsInf(o.Curve, 0) || math.Abs(o.Curve) > .5 {
		return fmt.Errorf("input: invalid curve")
	}
	if o.Variation < 0 || o.Variation > 16 {
		return fmt.Errorf("input: variation must be 0..16")
	}
	return nil
}
func Samples(a, b Point, o Options) []Point {
	if o.Duration == 0 || a == b {
		return []Point{a, b}
	}
	n := int(o.Duration / motionInterval)
	if n < 2 {
		n = 2
	}
	if n > motionMaxSamples {
		n = motionMaxSamples
	}
	points := make([]Point, n+1)
	points[0], points[n] = a, b
	ax, ay := float64(a.X), float64(a.Y)
	dx, dy := float64(b.X)-ax, float64(b.Y)-ay
	// The coordinate envelope is the endpoint rectangle expanded by the
	// maximum perpendicular bend (|Curve| <= 0.5) and variation on each
	// relevant axis. Unlike the endpoint rectangle, this permits horizontal
	// and vertical paths to bend normally without flattening.
	xMargin := .5*math.Abs(dy) + float64(o.Variation)
	yMargin := .5*math.Abs(dx) + float64(o.Variation)
	minX, maxX := math.Min(ax, float64(b.X))-xMargin, math.Max(ax, float64(b.X))+xMargin
	minY, maxY := math.Min(ay, float64(b.Y))-yMargin, math.Max(ay, float64(b.Y))+yMargin
	state := o.Seed
	rand := func() float64 {
		state += 0x9e3779b97f4a7c15
		z := state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		return float64(z>>11)/float64(uint64(1)<<53)*2 - 1
	}
	for i := 1; i < n; i++ {
		t := float64(i) / float64(n)
		e := t
		if o.Easing == "ease-in-out" {
			e = t * t * (3 - 2*t)
		}
		bend := 4 * e * (1 - e) * o.Curve
		x, y := ax+dx*e-dy*bend, ay+dy*e+dx*bend
		if o.Variation > 0 {
			fade := 2 * math.Min(e, 1-e)
			x += rand() * float64(o.Variation) * fade
			y += rand() * float64(o.Variation) * fade
		}
		x = math.Max(minX, math.Min(maxX, x))
		y = math.Max(minY, math.Min(maxY, y))
		points[i] = Point{motionCoordinate(x), motionCoordinate(y)}
	}
	return points
}

// motionCoordinate rounds and saturates finite trajectory positions. Float64
// cannot represent MaxInt precisely on 64-bit hosts, so compare before
// conversion, which would otherwise overflow at the integer limits.
func motionCoordinate(v float64) int {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	if v >= float64(maxInt) {
		return maxInt
	}
	if v <= float64(minInt) {
		return minInt
	}
	return int(math.Round(v))
}
