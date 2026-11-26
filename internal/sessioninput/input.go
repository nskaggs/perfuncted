package sessioninput

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"github.com/nskaggs/perfuncted/v2/internal/contextutil"
	"github.com/nskaggs/perfuncted/v2/internal/motion"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"image"
	"sync"
	"time"
)

// InputBundle exposes input operations through a capability-safe facade.
type state struct {
	backend      input.Inputter
	receiptMu    sync.Mutex
	receiptSeq   uint64
	receiptTrail []input.ActionReceipt
	gate         Admission
}

func (b *state) checkAvailable(operation string) error {
	if b == nil {
		return capability.ErrNilSession
	}
	if b.gate == nil {
		return capability.ErrNilSession
	}
	return b.gate.CheckBackend(operation, b.backend)
}

func (b *state) close() error {
	if b == nil {
		return nil
	}
	if util.IsNil(b.backend) {
		return nil
	}
	return b.backend.Close()
}

func (b *state) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return b.gate.InputContext(ctx, 0)
}

// singleInputAction enforces request and session authority before injecting
// one input action and again before acknowledging its backend receipt. A
// backend may ignore cancellation while an event is in flight, so a late
// success cannot certify authorized completion.
func (b *state) singleInputAction(ctx context.Context, operation, capability string, started time.Time, run func() error) error {
	if err := ctx.Err(); err != nil {
		return b.recordInputReceipt(operation, started, err)
	}
	err := b.gate.OperationError(capability, run())
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = errors.Join(ctxErr, err)
	} else if openErr := b.gate.EnsureOpen(); openErr != nil {
		err = errors.Join(openErr, err)
	}
	return b.recordInputReceipt(operation, started, err)
}

// KeyDown presses and holds key.
func (b *state) KeyDown(ctx context.Context, key string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.gate.Tracef("input", "key-down %s", key)
	started := time.Now()
	return b.singleInputAction(ctx, "key-down", "keyboard", started, func() error {
		return b.backend.KeyDown(ctx, key)
	})
}

// KeyUp releases key.
func (b *state) KeyUp(ctx context.Context, key string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.gate.Tracef("input", "key-up %s", key)
	started := time.Now()
	return b.singleInputAction(ctx, "key-up", "keyboard", started, func() error {
		return b.backend.KeyUp(ctx, key)
	})
}

// Type sends text using the input backend's key syntax.
func (b *state) Type(ctx context.Context, text string) error {
	return b.typeContext(ctx, text)
}

func (b *state) typeContext(ctx context.Context, text string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.gate.Tracef("input", "type")
	started := time.Now()
	return b.singleInputAction(ctx, "type", "keyboard", started, func() error {
		return b.backend.Type(ctx, text)
	})
}

// TypeLiteral sends text as-is without interpreting key syntax, so arbitrary
// text (including braces and modifier characters) round-trips unchanged.
func (b *state) TypeLiteral(ctx context.Context, text string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.gate.Tracef("input", "type-literal")
	started := time.Now()
	return b.singleInputAction(ctx, "type-literal", "keyboard", started, func() error {
		return b.backend.TypeLiteral(ctx, text)
	})
}

// MouseMove moves the pointer to screen coordinates x and y.
func (b *state) MouseMove(
	ctx context.Context,
	x int,
	y int,
) error {
	if err := b.checkAvailable("pointer"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.gate.Tracef("input", "mouse-move %d,%d", x, y)
	started := time.Now()
	return b.singleInputAction(ctx, "mouse-move", "pointer", started, func() error {
		return b.backend.MouseMove(ctx, x, y)
	})
}

// MouseClick moves to x and y and clicks button.
func (b *state) MouseClick(
	ctx context.Context,
	x int,
	y int,
	button int,
) error {
	if checkErr := b.checkAvailable("click"); checkErr != nil {
		return checkErr
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.gate.Tracef("input", "mouse-click %d,%d,%d", x, y, button)
	started := time.Now()
	return b.singleInputAction(ctx, "mouse-click", "click", started, func() error {
		return b.backend.MouseClick(ctx, x, y, button)
	})
}

// ClickCenter clicks the center of rect with the primary button.
func (b *state) ClickCenter(
	ctx context.Context,
	rect image.Rectangle,
) error {
	return b.MouseClick(
		ctx,
		rect.Min.X+rect.Dx()/2,
		rect.Min.Y+rect.Dy()/2,
		1,
	)
}

// DoubleClick performs two primary-button clicks at x and y.
//
//nolint:gocyclo // Held-button cleanup and cancellation require explicit guards.
func (b *state) DoubleClick(
	ctx context.Context,
	x int,
	y int,
) (err error) {
	ctx = contextutil.Default(ctx)
	if checkErr := b.checkAvailable("click"); checkErr != nil {
		return checkErr
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	started := time.Now()
	defer func() { err = b.recordInputReceipt("double-click", started, err) }()
	released := true
	defer func() {
		if !released {
			cleanupErr := b.gate.OperationError(
				"click",
				b.backend.MouseUp(context.WithoutCancel(ctx), 1),
			)
			if cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()
	if err := b.gate.OperationError("pointer", b.backend.MouseMove(ctx, x, y)); err != nil {
		return err
	}
	// Initial positioning never acquires a button hold. Do not inject the
	// first click or issue a cleanup release if authority ended mid-move.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return err
	}
	released = false
	if err := b.gate.OperationError("click", b.backend.MouseDown(ctx, 1)); err != nil {
		return err
	}
	if err := b.gate.OperationError("click", b.backend.MouseUp(ctx, 1)); err != nil {
		return err
	}
	released = true
	// The first click can finish after Session.Close. Refuse to schedule a
	// second click after the owning desktop session is gone.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return err
	}
	// Double-click inter-click spacing is an input protocol gesture interval;
	// cancellation of the caller context still ends it immediately.
	timer := time.NewTimer(20 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return err
	}
	released = false
	if err := b.gate.OperationError("click", b.backend.MouseDown(ctx, 1)); err != nil {
		return err
	}
	if err := b.gate.OperationError("click", b.backend.MouseUp(ctx, 1)); err != nil {
		return err
	}
	released = true
	// A successful final release does not certify a gesture acknowledged
	// after cancellation or session closure.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return err
	}
	return nil
}

// MouseDown presses button without releasing it.
func (b *state) MouseDown(ctx context.Context, button int) error {
	if err := b.checkAvailable("click"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.gate.Tracef("input", "mouse-down %d", button)
	started := time.Now()
	return b.singleInputAction(ctx, "mouse-down", "click", started, func() error {
		return b.backend.MouseDown(ctx, button)
	})
}

// MouseUp releases button.
func (b *state) MouseUp(ctx context.Context, button int) error {
	if err := b.checkAvailable("click"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.gate.Tracef("input", "mouse-up %d", button)
	started := time.Now()
	return b.singleInputAction(ctx, "mouse-up", "click", started, func() error {
		return b.backend.MouseUp(ctx, button)
	})
}

// ScrollUp scrolls upward by clicks.
func (b *state) ScrollUp(ctx context.Context, clicks int) error {
	if err := b.checkAvailable("scroll"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	started := time.Now()
	return b.singleInputAction(ctx, "scroll-up", "scroll", started, func() error {
		return b.backend.ScrollUp(ctx, clicks)
	})
}

// ScrollDown scrolls downward by clicks.
func (b *state) ScrollDown(ctx context.Context, clicks int) error {
	if err := b.checkAvailable("scroll"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	started := time.Now()
	return b.singleInputAction(ctx, "scroll-down", "scroll", started, func() error {
		return b.backend.ScrollDown(ctx, clicks)
	})
}

// ScrollLeft scrolls left by clicks.
func (b *state) ScrollLeft(ctx context.Context, clicks int) error {
	if err := b.checkAvailable("scroll"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	started := time.Now()
	return b.singleInputAction(ctx, "scroll-left", "scroll", started, func() error {
		return b.backend.ScrollLeft(ctx, clicks)
	})
}

// ScrollRight scrolls right by clicks.
func (b *state) ScrollRight(ctx context.Context, clicks int) error {
	if err := b.checkAvailable("scroll"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	started := time.Now()
	return b.singleInputAction(ctx, "scroll-right", "scroll", started, func() error {
		return b.backend.ScrollRight(ctx, clicks)
	})
}

// PointerLocation returns the current pointer coordinates.
func (b *state) PointerLocation(ctx context.Context) (int, int, error) {
	if err := b.checkAvailable("pointer-location"); err != nil {
		return 0, 0, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	x, y, err := b.backend.PointerLocation(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, 0, errors.Join(ctxErr, b.gate.OperationError("pointer-location", err))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return 0, 0, errors.Join(openErr, b.gate.OperationError("pointer-location", err))
	}
	return x, y, b.gate.OperationError("pointer-location", err)
}

// PointerCoordinateSpace reports the absolute coordinate space accepted by
// the active input backend. It is optional: backends without authoritative
// scale/provenance metadata return capability.ErrUnsupported so callers cannot mistake a
// browser CSS coordinate for a physical pointer coordinate.
func (b *state) PointerCoordinateSpace(ctx context.Context) (input.CoordinateSpaceInfo, error) {
	return observeInput(ctx, b, "pointer-coordinate-space", func(ctx context.Context) (input.CoordinateSpaceInfo, error) {
		reporter, ok := b.backend.(input.PointerCoordinateSpaceReporter)
		if !ok {
			return input.CoordinateSpaceInfo{}, input.ErrNotSupported
		}
		return reporter.PointerCoordinateSpace(ctx)
	})
}

// Sync flushes pending input when the backend supports synchronization.
func (b *state) Sync(ctx context.Context) error {
	if err := b.checkAvailable("sync"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if backend, ok := b.backend.(input.Syncer); ok {
		err := b.gate.OperationError("sync", backend.Sync(ctx))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errors.Join(ctxErr, err)
		}
		if openErr := b.gate.EnsureOpen(); openErr != nil {
			return errors.Join(openErr, err)
		}
		return err
	}
	return b.gate.OperationError("sync", capability.ErrUnsupported)
}

// DragAndDrop drags the primary button from (x1, y1) to (x2, y2).
func (b *state) DragAndDrop(ctx context.Context, x1, y1, x2, y2 int) error {
	return b.dragPoints(ctx, []motionPoint{{X: x1, Y: y1}, {X: x2, Y: y2}}, 0)
}

//nolint:gocyclo // Keep explicit authority and owned-button cleanup checks.
func (b *state) dragPoints(ctx context.Context, points []motionPoint, duration time.Duration) (err error) {
	ctx = contextutil.Default(ctx)
	if checkErr := b.checkAvailable("drag"); checkErr != nil {
		return checkErr
	}
	ctx, cancel := b.motionContext(ctx, duration)
	defer cancel()
	started := time.Now()
	defer func() { err = b.recordInputReceipt("drag-and-drop", started, err) }()
	if duration > 0 {
		if checkErr := b.validateMotionSpace(ctx); checkErr != nil {
			return checkErr
		}
	}
	if checkErr := b.validateMotionPoints(ctx, points); checkErr != nil {
		return checkErr
	}
	if checkErr := ctx.Err(); checkErr != nil {
		return checkErr
	}
	if checkErr := b.gate.EnsureOpen(); checkErr != nil {
		return checkErr
	}
	if checkErr := b.gate.OperationError("pointer", b.backend.MouseMove(ctx, points[0].X, points[0].Y)); checkErr != nil {
		return checkErr
	}
	if checkErr := ctx.Err(); checkErr != nil {
		return checkErr
	}
	if checkErr := b.gate.EnsureOpen(); checkErr != nil {
		return checkErr
	}
	released := false
	defer func() {
		if !released {
			cleanupErr := b.gate.OperationError("click", b.backend.MouseUp(context.WithoutCancel(ctx), 1))
			if cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()
	if checkErr := b.gate.OperationError("click", b.backend.MouseDown(ctx, 1)); checkErr != nil {
		return checkErr
	}
	if checkErr := ctx.Err(); checkErr != nil {
		return checkErr
	}
	if checkErr := b.gate.EnsureOpen(); checkErr != nil {
		return checkErr
	}
	if checkErr := b.sendMotion(ctx, points, duration, 1); checkErr != nil {
		return checkErr
	}
	if checkErr := b.gate.OperationError("click", b.backend.MouseUp(ctx, 1)); checkErr != nil {
		return checkErr
	}
	released = true
	if checkErr := b.flushMotionRequests(ctx); checkErr != nil {
		return checkErr
	}
	if checkErr := ctx.Err(); checkErr != nil {
		return checkErr
	}
	if checkErr := b.gate.EnsureOpen(); checkErr != nil {
		return checkErr
	}
	return nil
}

// observeInput preserves cancellation and session authority around native observations.
func observeInput[T interface{ Validate() error }](ctx context.Context, b *state, operation string, observe func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := b.checkAvailable(operation); err != nil {
		return zero, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	result, err := observe(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return zero, errors.Join(ctxErr, b.gate.OperationError(operation, err))
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return zero, errors.Join(openErr, b.gate.OperationError(operation, err))
	}
	if err != nil {
		return zero, b.gate.OperationError(operation, err)
	}
	if err := result.Validate(); err != nil {
		return zero, b.gate.OperationError(operation, err)
	}
	return result, nil
}

// ObservePointer returns an authoritative position and its provider-native units.
// It never falls back to another capability or to remembered injection targets.
func (b *state) ObservePointer(ctx context.Context) (input.PointerObservation, error) {
	return observeInput(ctx, b, "pointer-location", func(ctx context.Context) (input.PointerObservation, error) {
		observer, ok := b.backend.(input.PointerObserver)
		if !ok {
			return input.PointerObservation{}, input.ErrNotSupported
		}
		return observer.ObservePointer(ctx)
	})
}

type motionPoint = motion.Point

// validateMotionSpace uses the existing Session-authorized coordinate
// provenance reader. Smooth operations never guess whether coordinates are
// logical or physical; the caller supplies units in this proven backend space.
func (b *state) validateMotionSpace(ctx context.Context) error {
	_, err := b.PointerCoordinateSpace(ctx)
	return err
}

func (b *state) validateMotionPoints(ctx context.Context, points []motionPoint) error {
	if v, ok := b.backend.(input.PointerTargetValidator); ok {
		for _, p := range points {
			if err := v.ValidatePointerTarget(ctx, p.X, p.Y); err != nil {
				return b.gate.OperationError("pointer", err)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := b.gate.EnsureOpen(); err != nil {
				return err
			}
		}
	}
	return nil
}
func (b *state) motionContext(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return b.gate.InputContext(ctx, d)
}

//nolint:gocyclo // Per-frame timing, cancellation and session authority are explicit.
func (b *state) sendMotion(ctx context.Context, points []motionPoint, d time.Duration, first int) error {
	started := time.Now()
	last := len(points) - 1
	for i := first; i <= last; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.gate.EnsureOpen(); err != nil {
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
		if err := b.gate.EnsureOpen(); err != nil {
			return err
		}
		if err := b.gate.OperationError("pointer", b.backend.MouseMove(ctx, points[i].X, points[i].Y)); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.gate.EnsureOpen(); err != nil {
			return err
		}
	}
	return b.flushMotionRequests(ctx)
}

// flushMotionRequests is a backend-specific protocol delivery barrier. It
// deliberately does not imply that any target application consumed the input.
func (b *state) flushMotionRequests(ctx context.Context) error {
	flusher, ok := b.backend.(interface{ FlushMotionRequests(context.Context) error })
	if !ok {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return err
	}
	if err := b.gate.OperationError("pointer", flusher.FlushMotionRequests(ctx)); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.gate.EnsureOpen()
}

// MouseMoveSmooth moves from an explicit start, or the reported pointer position.
// Unknown pointer origins fail rather than using a guessed location.
func (b *state) MouseMoveSmooth(ctx context.Context, start *image.Point, end image.Point, o input.MotionOptions) (err error) {
	if checkErr := input.ValidateMotionOptions(o); checkErr != nil {
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
			return b.gate.OperationError("pointer-location", err)
		}
		if checkErr := ctx.Err(); checkErr != nil {
			return checkErr
		}
		if checkErr := b.gate.EnsureOpen(); checkErr != nil {
			return checkErr
		}
		start = &image.Point{X: x, Y: y}
	}
	points := motion.Samples(motionPoint{X: start.X, Y: start.Y}, motionPoint{X: end.X, Y: end.Y}, o)
	if checkErr := b.validateMotionPoints(ctx, points); checkErr != nil {
		return checkErr
	}
	if checkErr := ctx.Err(); checkErr != nil {
		return checkErr
	}
	if checkErr := b.gate.EnsureOpen(); checkErr != nil {
		return checkErr
	}
	if points[0] == points[len(points)-1] {
		return nil
	}
	return b.sendMotion(ctx, points, o.Duration, 0)
}

// DragAndDropSmooth shares the direct drag's button ownership and cleanup.
func (b *state) DragAndDropSmooth(ctx context.Context, start, end image.Point, o input.MotionOptions) error {
	if checkErr := input.ValidateMotionOptions(o); checkErr != nil {
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
	points := motion.Samples(motionPoint{X: start.X, Y: start.Y}, motionPoint{X: end.X, Y: end.Y}, o)
	return b.dragPoints(ctx, points, o.Duration)
}

const inputReceiptHistoryLimit = 32

func (b *state) recordInputReceipt(operation string, started time.Time, err error) error {
	if b == nil {
		return err
	}
	receipt := input.ActionReceipt{
		Operation:   operation,
		Dispatch:    inputDispatchResult(err),
		StartedAt:   started.UTC(),
		CompletedAt: time.Now().UTC(),
	}
	// The input backend is fixed for the life of the session. Resolving its
	// metadata on every key, click, scroll, and type action is unnecessary:
	// only diagnostic snapshots consume the mechanism field.
	if err != nil {
		receipt.FailureClass = inputFailureClass(err)
	}

	b.receiptMu.Lock()
	b.receiptSeq++
	receipt.Sequence = b.receiptSeq
	if len(b.receiptTrail) == inputReceiptHistoryLimit {
		copy(b.receiptTrail, b.receiptTrail[1:])
		b.receiptTrail[len(b.receiptTrail)-1] = receipt
	} else {
		b.receiptTrail = append(b.receiptTrail, receipt)
	}
	b.receiptMu.Unlock()
	return err
}

func (b *state) recentInputReceipts() []input.ActionReceipt {
	if b == nil {
		return nil
	}
	b.receiptMu.Lock()
	receipts := append([]input.ActionReceipt(nil), b.receiptTrail...)
	b.receiptMu.Unlock()

	if len(receipts) > 0 && b.gate != nil {
		mechanism := b.gate.Mechanism()
		for i := range receipts {
			receipts[i].Mechanism = mechanism
		}
	}
	return receipts
}

func inputDispatchResult(err error) string {
	switch {
	case err == nil:
		return "accepted"
	case errors.Is(err, capability.ErrInvalidArgument), errors.Is(err, capability.ErrUnsupported), errors.Is(err, input.ErrNotSupported):
		return "not-sent"
	default:
		return "unknown"
	}
}

func inputFailureClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline-exceeded"
	case errors.Is(err, capability.ErrInvalidArgument), errors.Is(err, capability.ErrUnsupported), errors.Is(err, input.ErrNotSupported):
		return "rejected-before-dispatch"
	default:
		return "operation-failed"
	}
}

// Admission is the injection-specific session boundary. It cannot obtain
// other backends or alter session ownership.
type Admission interface {
	capability.BackendAuthority
	InputContext(context.Context, time.Duration) (context.Context, context.CancelFunc)
	Tracef(component, format string, args ...any)
	Mechanism() string
}

// Owner holds the session-owned state; only the composition boundary receives it.
type Owner struct{ *state }

// Handle references private state, and does not expose Owner's lifecycle methods.
type Handle struct{ *state }

func New(gate Admission, backend input.Inputter) *Owner {
	return &Owner{&state{gate: gate, backend: backend}}
}
func (o *Owner) Handle() Handle {
	if o == nil {
		return Handle{}
	}
	return Handle{o.state}
}
func (o *Owner) Install(backend input.Inputter) { o.backend = backend }
func (o *Owner) Close() error {
	if o == nil {
		return nil
	}
	return o.close()
}
func (o *Owner) CheckAvailable(operation string) error {
	if o == nil {
		return capability.ErrNilSession
	}
	return o.checkAvailable(operation)
}
func (o *Owner) RecentInputReceipts() []input.ActionReceipt {
	if o == nil {
		return nil
	}
	return o.recentInputReceipts()
}
func (o *Owner) RecordInputReceipt(operation string, started time.Time, err error) error {
	if o == nil {
		return err
	}
	return o.recordInputReceipt(operation, started, err)
}
func (o *Owner) SendMotion(ctx context.Context, points []motion.Point, d time.Duration, first int) error {
	return o.sendMotion(ctx, points, d, first)
}
func (o *Owner) DragPoints(ctx context.Context, points []motion.Point, d time.Duration) error {
	return o.dragPoints(ctx, points, d)
}

const ReceiptHistoryLimit = inputReceiptHistoryLimit
