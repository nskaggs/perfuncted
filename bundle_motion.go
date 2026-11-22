package perfuncted

import (
	"context"
	"fmt"
	"image"
	"math"
	"time"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/contextutil"
)

// MotionOptions selects optional bounded pointer interpolation in backend coordinates.
type MotionOptions struct {
	Duration  time.Duration
	Easing    string  // "", "linear", or "ease-in-out"
	Curve     float64 // perpendicular bend fraction, -0.5..0.5
	Variation int     // seeded coordinate variation in backend units, 0..16
	Seed      uint64
}
type motionPoint struct{ x, y int }

const (
	motionInterval    = time.Second / 60
	motionMaxDuration = 10 * time.Second
	motionMaxSamples  = 120
)

// ValidateMotionOptions checks a motion request before opening an input session.
func ValidateMotionOptions(o MotionOptions) error { return o.validate() }
func (o MotionOptions) validate() error {
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
func motionSamples(a, b motionPoint, o MotionOptions) []motionPoint {
	if o.Duration == 0 || a == b {
		return []motionPoint{a, b}
	}
	n := int(o.Duration / motionInterval)
	if n < 2 {
		n = 2
	}
	if n > motionMaxSamples {
		n = motionMaxSamples
	}
	points := make([]motionPoint, n+1)
	points[0], points[n] = a, b
	ax, ay := float64(a.x), float64(a.y)
	dx, dy := float64(b.x)-ax, float64(b.y)-ay
	// The coordinate envelope is the endpoint rectangle expanded by the
	// maximum perpendicular bend (|Curve| <= 0.5) and variation on each
	// relevant axis. Unlike the endpoint rectangle, this permits horizontal
	// and vertical paths to bend normally without flattening.
	xMargin := .5*math.Abs(dy) + float64(o.Variation)
	yMargin := .5*math.Abs(dx) + float64(o.Variation)
	minX, maxX := math.Min(ax, float64(b.x))-xMargin, math.Max(ax, float64(b.x))+xMargin
	minY, maxY := math.Min(ay, float64(b.y))-yMargin, math.Max(ay, float64(b.y))+yMargin
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
		points[i] = motionPoint{motionCoordinate(x), motionCoordinate(y)}
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

// validateMotionSpace uses the existing Session-authorized coordinate
// provenance reader. Smooth operations never guess whether coordinates are
// logical or physical; the caller supplies units in this proven backend space.
func (b *InputBundle) validateMotionSpace(ctx context.Context) error {
	_, err := b.PointerCoordinateSpace(ctx)
	return err
}

func (b *InputBundle) validateMotionPoints(ctx context.Context, points []motionPoint) error {
	if v, ok := b.backend.(input.PointerTargetValidator); ok {
		for _, p := range points {
			if err := v.ValidatePointerTarget(ctx, p.x, p.y); err != nil {
				return b.operationError("pointer", err)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := b.session.ensureOpen(); err != nil {
				return err
			}
		}
	}
	return nil
}
func (b *InputBundle) motionContext(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return b.backendContext(ctx, d+b.session.Timeouts().Short)
}

//nolint:gocyclo // Per-frame timing, cancellation and session authority are explicit.
func (b *InputBundle) sendMotion(ctx context.Context, points []motionPoint, d time.Duration, first int) error {
	started := time.Now()
	last := len(points) - 1
	for i := first; i <= last; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.session.ensureOpen(); err != nil {
			return err
		}
		if d > 0 && i > 0 {
			due := started.Add(time.Duration(i) * d / time.Duration(last))
			if i < last && time.Now().After(due.Add(d/time.Duration(last))) {
				continue
			}
			if wait := time.Until(due); wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.session.ensureOpen(); err != nil {
			return err
		}
		if err := b.operationError("pointer", b.backend.MouseMove(ctx, points[i].x, points[i].y)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.session.ensureOpen(); err != nil {
			return err
		}
	}
	return nil
}

// MouseMoveSmooth moves from an explicit start, or the reported pointer position.
// Unknown pointer origins fail rather than using a guessed location.
func (b *InputBundle) MouseMoveSmooth(ctx context.Context, start *image.Point, end image.Point, o MotionOptions) (err error) {
	if checkErr := o.validate(); checkErr != nil {
		return checkErr
	}
	if o.Duration == 0 {
		return b.MouseMove(ctx, end.X, end.Y)
	}
	if checkErr := b.checkAvailable("pointer"); checkErr != nil {
		return checkErr
	}
	ctx = contextutil.Default(ctx)
	ctx, cancel := b.motionContext(ctx, o.Duration)
	defer cancel()
	begun := time.Now()
	defer func() { err = b.recordInputReceipt("mouse-move-smooth", begun, err) }()
	if checkErr := ctx.Err(); checkErr != nil {
		return checkErr
	}
	if checkErr := b.validateMotionSpace(ctx); checkErr != nil {
		return checkErr
	}
	if start == nil {
		if checkErr := b.checkAvailable("pointer-location"); checkErr != nil {
			return checkErr
		}
		var x, y int
		x, y, err = b.backend.PointerLocation(ctx)
		if err != nil {
			return b.operationError("pointer-location", err)
		}
		if checkErr := ctx.Err(); checkErr != nil {
			return checkErr
		}
		if checkErr := b.session.ensureOpen(); checkErr != nil {
			return checkErr
		}
		start = &image.Point{X: x, Y: y}
	}
	points := motionSamples(motionPoint{start.X, start.Y}, motionPoint{end.X, end.Y}, o)
	if checkErr := b.validateMotionPoints(ctx, points); checkErr != nil {
		return checkErr
	}
	if checkErr := ctx.Err(); checkErr != nil {
		return checkErr
	}
	if checkErr := b.session.ensureOpen(); checkErr != nil {
		return checkErr
	}
	if points[0] == points[len(points)-1] {
		return nil
	}
	return b.sendMotion(ctx, points, o.Duration, 0)
}

// DragAndDropSmooth shares the direct drag's button ownership and cleanup.
func (b *InputBundle) DragAndDropSmooth(ctx context.Context, start, end image.Point, o MotionOptions) error {
	if checkErr := o.validate(); checkErr != nil {
		return checkErr
	}
	// An identical-origin drag is an immediate press/release gesture,
	// regardless of the requested duration. It owns no timed movement.
	if o.Duration == 0 {
		return b.DragAndDrop(ctx, start.X, start.Y, end.X, end.Y)
	}
	if start == end {
		return b.dragPoints(ctx, []motionPoint{{start.X, start.Y}}, 0)
	}
	points := motionSamples(motionPoint{start.X, start.Y}, motionPoint{end.X, end.Y}, o)
	return b.dragPoints(ctx, points, o.Duration)
}
