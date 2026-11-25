package perfuncted

import (
	"context"
	"github.com/nskaggs/perfuncted/internal/motion"
	"image"
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
type motionPoint = motion.Point

// ValidateMotionOptions checks a motion request before opening an input session.
func ValidateMotionOptions(o MotionOptions) error { return o.validate() }
func (o MotionOptions) validate() error           { return motion.Options(o).Validate() }

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
			if err := v.ValidatePointerTarget(ctx, p.X, p.Y); err != nil {
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
		if err := b.operationError("pointer", b.backend.MouseMove(ctx, points[i].X, points[i].Y)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.session.ensureOpen(); err != nil {
			return err
		}
	}
	return b.flushMotionRequests(ctx)
}

// flushMotionRequests is a backend-specific protocol delivery barrier. It
// deliberately does not imply that any target application consumed the input.
func (b *InputBundle) flushMotionRequests(ctx context.Context) error {
	flusher, ok := b.backend.(interface{ FlushMotionRequests(context.Context) error })
	if !ok {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.session.ensureOpen(); err != nil {
		return err
	}
	if err := b.operationError("pointer", flusher.FlushMotionRequests(ctx)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.session.ensureOpen()
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
	points := motion.Samples(motionPoint{X: start.X, Y: start.Y}, motionPoint{X: end.X, Y: end.Y}, motion.Options(o))
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
		return b.dragPoints(ctx, []motionPoint{{X: start.X, Y: start.Y}}, 0)
	}
	points := motion.Samples(motionPoint{X: start.X, Y: start.Y}, motionPoint{X: end.X, Y: end.Y}, motion.Options(o))
	return b.dragPoints(ctx, points, o.Duration)
}
