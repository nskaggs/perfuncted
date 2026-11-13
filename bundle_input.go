package perfuncted

import (
	"context"
	"errors"
	"image"
	"sync"
	"time"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/contextutil"
)

// InputBundle exposes input operations through a capability-safe facade.
type InputBundle struct {
	backend      input.Inputter
	receiptMu    sync.Mutex
	receiptSeq   uint64
	receiptTrail []InputActionReceipt
	bundleBase
}

func (b *InputBundle) checkAvailable(operation string) error {
	if b == nil {
		return (&bundleBase{}).unavailable(operation)
	}
	return b.checkBackend(operation, b.backend)
}

func (b *InputBundle) close() error {
	if b == nil {
		return nil
	}
	return closeBackend(b.backend)
}

func (b *InputBundle) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return b.backendContext(ctx, b.session.Timeouts().Short)
}

// singleInputAction enforces request and session authority before injecting
// one input action and again before acknowledging its backend receipt. A
// backend may ignore cancellation while an event is in flight, so a late
// success cannot certify authorized completion.
func (b *InputBundle) singleInputAction(ctx context.Context, operation, capability string, started time.Time, run func() error) error {
	if err := ctx.Err(); err != nil {
		return b.recordInputReceipt(operation, started, err)
	}
	err := b.operationError(capability, run())
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = errors.Join(ctxErr, err)
	} else if openErr := b.session.ensureOpen(); openErr != nil {
		err = errors.Join(openErr, err)
	}
	return b.recordInputReceipt(operation, started, err)
}

// KeyDown presses and holds key.
func (b *InputBundle) KeyDown(ctx context.Context, key string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.traceAction("input", "key-down %s", key)
	started := time.Now()
	return b.singleInputAction(ctx, "key-down", "keyboard", started, func() error {
		return b.backend.KeyDown(ctx, key)
	})
}

// KeyUp releases key.
func (b *InputBundle) KeyUp(ctx context.Context, key string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.traceAction("input", "key-up %s", key)
	started := time.Now()
	return b.singleInputAction(ctx, "key-up", "keyboard", started, func() error {
		return b.backend.KeyUp(ctx, key)
	})
}

// Type sends text using the input backend's key syntax.
func (b *InputBundle) Type(ctx context.Context, text string) error {
	return b.typeContext(ctx, text)
}

func (b *InputBundle) typeContext(ctx context.Context, text string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.traceAction("input", "type")
	started := time.Now()
	return b.singleInputAction(ctx, "type", "keyboard", started, func() error {
		return b.backend.Type(ctx, text)
	})
}

// TypeLiteral sends text as-is without interpreting key syntax, so arbitrary
// text (including braces and modifier characters) round-trips unchanged.
func (b *InputBundle) TypeLiteral(ctx context.Context, text string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.traceAction("input", "type-literal")
	started := time.Now()
	return b.singleInputAction(ctx, "type-literal", "keyboard", started, func() error {
		return b.backend.TypeLiteral(ctx, text)
	})
}

// MouseMove moves the pointer to screen coordinates x and y.
func (b *InputBundle) MouseMove(
	ctx context.Context,
	x int,
	y int,
) error {
	if err := b.checkAvailable("pointer"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.traceAction("input", "mouse-move %d,%d", x, y)
	started := time.Now()
	return b.singleInputAction(ctx, "mouse-move", "pointer", started, func() error {
		return b.backend.MouseMove(ctx, x, y)
	})
}

// MouseClick moves to x and y and clicks button.
func (b *InputBundle) MouseClick(
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
	b.traceAction("input", "mouse-click %d,%d,%d", x, y, button)
	started := time.Now()
	return b.singleInputAction(ctx, "mouse-click", "click", started, func() error {
		return b.backend.MouseClick(ctx, x, y, button)
	})
}

// ClickCenter clicks the center of rect with the primary button.
func (b *InputBundle) ClickCenter(
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
func (b *InputBundle) DoubleClick(
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
			cleanupErr := b.operationError(
				"click",
				b.backend.MouseUp(context.WithoutCancel(ctx), 1),
			)
			if cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()
	if err := b.operationError("pointer", b.backend.MouseMove(ctx, x, y)); err != nil {
		return err
	}
	// Initial positioning never acquires a button hold. Do not inject the
	// first click or issue a cleanup release if authority ended mid-move.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.session.ensureOpen(); err != nil {
		return err
	}
	released = false
	if err := b.operationError("click", b.backend.MouseDown(ctx, 1)); err != nil {
		return err
	}
	if err := b.operationError("click", b.backend.MouseUp(ctx, 1)); err != nil {
		return err
	}
	released = true
	// The first click can finish after Session.Close. Refuse to schedule a
	// second click after the owning desktop session is gone.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.session.ensureOpen(); err != nil {
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
	if err := b.session.ensureOpen(); err != nil {
		return err
	}
	released = false
	if err := b.operationError("click", b.backend.MouseDown(ctx, 1)); err != nil {
		return err
	}
	if err := b.operationError("click", b.backend.MouseUp(ctx, 1)); err != nil {
		return err
	}
	released = true
	// A successful final release does not certify a gesture acknowledged
	// after cancellation or session closure.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.session.ensureOpen(); err != nil {
		return err
	}
	return nil
}

// MouseDown presses button without releasing it.
func (b *InputBundle) MouseDown(ctx context.Context, button int) error {
	if err := b.checkAvailable("click"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.traceAction("input", "mouse-down %d", button)
	started := time.Now()
	return b.singleInputAction(ctx, "mouse-down", "click", started, func() error {
		return b.backend.MouseDown(ctx, button)
	})
}

// MouseUp releases button.
func (b *InputBundle) MouseUp(ctx context.Context, button int) error {
	if err := b.checkAvailable("click"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	b.traceAction("input", "mouse-up %d", button)
	started := time.Now()
	return b.singleInputAction(ctx, "mouse-up", "click", started, func() error {
		return b.backend.MouseUp(ctx, button)
	})
}

// ScrollUp scrolls upward by clicks.
func (b *InputBundle) ScrollUp(ctx context.Context, clicks int) error {
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
func (b *InputBundle) ScrollDown(ctx context.Context, clicks int) error {
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
func (b *InputBundle) ScrollLeft(ctx context.Context, clicks int) error {
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
func (b *InputBundle) ScrollRight(ctx context.Context, clicks int) error {
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
func (b *InputBundle) PointerLocation(ctx context.Context) (int, int, error) {
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
		return 0, 0, errors.Join(ctxErr, b.operationError("pointer-location", err))
	}
	if openErr := b.session.ensureOpen(); openErr != nil {
		return 0, 0, errors.Join(openErr, b.operationError("pointer-location", err))
	}
	return x, y, b.operationError("pointer-location", err)
}

// PointerCoordinateSpace reports the absolute coordinate space accepted by
// the active input backend. It is optional: backends without authoritative
// scale/provenance metadata return ErrUnsupported so callers cannot mistake a
// browser CSS coordinate for a physical pointer coordinate.
func (b *InputBundle) PointerCoordinateSpace(ctx context.Context) (input.CoordinateSpaceInfo, error) {
	if err := b.checkAvailable("pointer-coordinate-space"); err != nil {
		return input.CoordinateSpaceInfo{}, err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return input.CoordinateSpaceInfo{}, err
	}
	reporter, ok := b.backend.(input.PointerCoordinateSpaceReporter)
	if !ok {
		return input.CoordinateSpaceInfo{}, b.operationError("pointer-coordinate-space", input.ErrNotSupported)
	}
	info, err := reporter.PointerCoordinateSpace(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return input.CoordinateSpaceInfo{}, errors.Join(ctxErr, b.operationError("pointer-coordinate-space", err))
	}
	if openErr := b.session.ensureOpen(); openErr != nil {
		return input.CoordinateSpaceInfo{}, errors.Join(openErr, b.operationError("pointer-coordinate-space", err))
	}
	if err != nil {
		return input.CoordinateSpaceInfo{}, b.operationError("pointer-coordinate-space", err)
	}
	if err := info.Validate(); err != nil {
		return input.CoordinateSpaceInfo{}, b.operationError("pointer-coordinate-space", err)
	}
	return info, nil
}

// Sync flushes pending input when the backend supports synchronization.
func (b *InputBundle) Sync(ctx context.Context) error {
	if err := b.checkAvailable("sync"); err != nil {
		return err
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if backend, ok := b.backend.(input.Syncer); ok {
		err := b.operationError("sync", backend.Sync(ctx))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errors.Join(ctxErr, err)
		}
		if openErr := b.session.ensureOpen(); openErr != nil {
			return errors.Join(openErr, err)
		}
		return err
	}
	return b.operationError("sync", ErrUnsupported)
}

// DragAndDrop drags the primary button from (x1, y1) to (x2, y2).
func (b *InputBundle) DragAndDrop(
	ctx context.Context,
	x1 int,
	y1 int,
	x2 int,
	y2 int,
) (err error) {
	ctx = contextutil.Default(ctx)
	if checkErr := b.checkAvailable("drag"); checkErr != nil {
		return checkErr
	}
	ctx, cancel := b.operationContext(ctx)
	defer cancel()
	started := time.Now()
	defer func() { err = b.recordInputReceipt("drag-and-drop", started, err) }()
	// A rejected destination must not first press the button at a valid origin.
	// Backends with authoritative bounds can validate both points without input.
	if validator, ok := b.backend.(input.PointerTargetValidator); ok {
		if checkErr := validator.ValidatePointerTarget(ctx, x1, y1); checkErr != nil {
			return b.operationError("pointer", checkErr)
		}
		if checkErr := validator.ValidatePointerTarget(ctx, x2, y2); checkErr != nil {
			return b.operationError("pointer", checkErr)
		}
	}
	// A target validator can finish after cancellation or Session.Close.
	// Validation is non-mutating: refuse the gesture before its first event.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.session.ensureOpen(); err != nil {
		return err
	}
	if moveErr := b.operationError("pointer", b.backend.MouseMove(ctx, x1, y1)); moveErr != nil {
		return moveErr
	}
	// The initial move is not a button hold. If it completed after
	// cancellation or session closure, do not begin a drag or issue cleanup
	// releases for a button this operation never pressed.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.session.ensureOpen(); err != nil {
		return err
	}
	released := false
	defer func() {
		if !released {
			cleanupErr := b.operationError(
				"click",
				b.backend.MouseUp(context.WithoutCancel(ctx), 1),
			)
			if cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()
	if err := b.operationError("click", b.backend.MouseDown(ctx, 1)); err != nil {
		return err
	}
	if err := b.operationError("pointer", b.backend.MouseMove(ctx, x2, y2)); err != nil {
		return err
	}
	// Once the button is held, authority loss must trigger the deferred
	// best-effort release rather than an additional authorized gesture.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.session.ensureOpen(); err != nil {
		return err
	}
	if err := b.operationError("click", b.backend.MouseUp(ctx, 1)); err != nil {
		return err
	}
	released = true
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.session.ensureOpen(); err != nil {
		return err
	}
	return nil
}
