package perfuncted

import (
	"context"
	"errors"
	"image"
	"time"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/contextutil"
)

// InputBundle exposes input operations through a capability-safe facade.
type InputBundle struct {
	backend input.Inputter
	bundleBase
}

func (b *InputBundle) checkAvailable(operation string) error {
	if b == nil {
		return (&bundleBase{}).unavailable(operation)
	}
	return b.bundleBase.checkAvailable(operation, b.backend != nil)
}

func (b *InputBundle) KeyDown(ctx context.Context, key string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	b.traceAction("input", "key-down %s", key)
	return b.operationError("keyboard", b.backend.KeyDown(ctx, key))
}

func (b *InputBundle) KeyUp(ctx context.Context, key string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	b.traceAction("input", "key-up %s", key)
	return b.operationError("keyboard", b.backend.KeyUp(ctx, key))
}

func (b *InputBundle) Type(ctx context.Context, text string) error {
	return b.typeContext(ctx, text)
}

func (b *InputBundle) typeContext(ctx context.Context, text string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	b.traceAction("input", "type")
	return b.operationError("keyboard", b.backend.Type(ctx, text))
}

func (b *InputBundle) MouseMove(
	ctx context.Context,
	x int,
	y int,
) error {
	if err := b.checkAvailable("pointer"); err != nil {
		return err
	}
	b.traceAction("input", "mouse-move %d,%d", x, y)
	return b.operationError("pointer", b.backend.MouseMove(ctx, x, y))
}

func (b *InputBundle) MouseClick(
	ctx context.Context,
	x int,
	y int,
	button int,
) error {
	if checkErr := b.checkAvailable("click"); checkErr != nil {
		return checkErr
	}
	b.traceAction("input", "mouse-click %d,%d,%d", x, y, button)
	return b.operationError("click", b.backend.MouseClick(ctx, x, y, button))
}

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

func (b *InputBundle) DoubleClick(
	ctx context.Context,
	x int,
	y int,
) (err error) {
	ctx = contextutil.Default(ctx)
	if checkErr := b.checkAvailable("click"); checkErr != nil {
		return checkErr
	}
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
	released = false
	if err := b.operationError("click", b.backend.MouseDown(ctx, 1)); err != nil {
		return err
	}
	if err := b.operationError("click", b.backend.MouseUp(ctx, 1)); err != nil {
		return err
	}
	released = true
	timer := time.NewTimer(20 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	released = false
	if err := b.operationError("click", b.backend.MouseDown(ctx, 1)); err != nil {
		return err
	}
	if err := b.operationError("click", b.backend.MouseUp(ctx, 1)); err != nil {
		return err
	}
	released = true
	return nil
}

func (b *InputBundle) MouseDown(ctx context.Context, button int) error {
	if err := b.checkAvailable("click"); err != nil {
		return err
	}
	b.traceAction("input", "mouse-down %d", button)
	return b.operationError("click", b.backend.MouseDown(ctx, button))
}

func (b *InputBundle) MouseUp(ctx context.Context, button int) error {
	if err := b.checkAvailable("click"); err != nil {
		return err
	}
	b.traceAction("input", "mouse-up %d", button)
	return b.operationError("click", b.backend.MouseUp(ctx, button))
}

func (b *InputBundle) ScrollUp(ctx context.Context, clicks int) error {
	if err := b.checkAvailable("scroll"); err != nil {
		return err
	}
	return b.operationError("scroll", b.backend.ScrollUp(ctx, clicks))
}

func (b *InputBundle) ScrollDown(ctx context.Context, clicks int) error {
	if err := b.checkAvailable("scroll"); err != nil {
		return err
	}
	return b.operationError("scroll", b.backend.ScrollDown(ctx, clicks))
}

func (b *InputBundle) ScrollLeft(ctx context.Context, clicks int) error {
	if err := b.checkAvailable("scroll"); err != nil {
		return err
	}
	return b.operationError("scroll", b.backend.ScrollLeft(ctx, clicks))
}

func (b *InputBundle) ScrollRight(ctx context.Context, clicks int) error {
	if err := b.checkAvailable("scroll"); err != nil {
		return err
	}
	return b.operationError("scroll", b.backend.ScrollRight(ctx, clicks))
}

func (b *InputBundle) PointerLocation(ctx context.Context) (int, int, error) {
	if err := b.checkAvailable("pointer-location"); err != nil {
		return 0, 0, err
	}
	x, y, err := b.backend.PointerLocation(ctx)
	return x, y, b.operationError("pointer-location", err)
}

func (b *InputBundle) Sync(ctx context.Context) error {
	if err := b.checkAvailable("sync"); err != nil {
		return err
	}
	type syncer interface {
		Sync(context.Context) error
	}
	if backend, ok := b.backend.(syncer); ok {
		return b.operationError("sync", backend.Sync(ctx))
	}
	return b.operationError("sync", ErrUnsupported)
}

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
	if moveErr := b.operationError("pointer", b.backend.MouseMove(ctx, x1, y1)); moveErr != nil {
		return moveErr
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
	if err := b.operationError("click", b.backend.MouseUp(ctx, 1)); err != nil {
		return err
	}
	released = true
	return nil
}
