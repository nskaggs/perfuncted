package perfuncted

import (
	"context"
	"image"
	"time"

	"github.com/nskaggs/perfuncted/ctxutil"
	"github.com/nskaggs/perfuncted/input"
)

// InputBundle exposes input operations through a capability-safe facade.
type InputBundle struct {
	backend input.Inputter
	bundleBase
}

func (b *InputBundle) close() error {
	if b == nil || b.backend == nil {
		return nil
	}
	b.traceAction("input", "close")
	return b.backend.Close()
}

func (b *InputBundle) checkAvailable(operation string) error {
	if b == nil || b.backend == nil {
		return b.unavailable(operation)
	}
	return nil
}

func (b *InputBundle) KeyDown(ctx context.Context, key string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	b.traceAction("input", "key-down %s", key)
	return b.backend.KeyDown(ctx, key)
}

func (b *InputBundle) KeyUp(ctx context.Context, key string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	b.traceAction("input", "key-up %s", key)
	return b.backend.KeyUp(ctx, key)
}

func (b *InputBundle) Type(ctx context.Context, text string) error {
	return b.typeContext(ctx, text)
}

func (b *InputBundle) typeContext(ctx context.Context, text string) error {
	if err := b.checkAvailable("keyboard"); err != nil {
		return err
	}
	b.traceAction("input", "type")
	return b.backend.Type(ctx, text)
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
	return b.backend.MouseMove(ctx, x, y)
}

func (b *InputBundle) MouseClick(
	ctx context.Context,
	x int,
	y int,
	button int,
) error {
	if err := b.checkAvailable("click"); err != nil {
		return err
	}
	b.traceAction("input", "mouse-click %d,%d,%d", x, y, button)
	return b.backend.MouseClick(ctx, x, y, button)
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
) error {
	ctx = ctxutil.Default(ctx)
	if err := b.checkAvailable("click"); err != nil {
		return err
	}
	if err := b.backend.MouseMove(ctx, x, y); err != nil {
		return err
	}
	if err := b.backend.MouseDown(ctx, 1); err != nil {
		return err
	}
	if err := b.backend.MouseUp(ctx, 1); err != nil {
		return err
	}
	timer := time.NewTimer(20 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	if err := b.backend.MouseDown(ctx, 1); err != nil {
		return err
	}
	return b.backend.MouseUp(ctx, 1)
}

func (b *InputBundle) MouseDown(ctx context.Context, button int) error {
	if err := b.checkAvailable("click"); err != nil {
		return err
	}
	b.traceAction("input", "mouse-down %d", button)
	return b.backend.MouseDown(ctx, button)
}

func (b *InputBundle) MouseUp(ctx context.Context, button int) error {
	if err := b.checkAvailable("click"); err != nil {
		return err
	}
	b.traceAction("input", "mouse-up %d", button)
	return b.backend.MouseUp(ctx, button)
}

func (b *InputBundle) ScrollUp(ctx context.Context, clicks int) error {
	if err := b.checkAvailable("scroll"); err != nil {
		return err
	}
	return b.backend.ScrollUp(ctx, clicks)
}

func (b *InputBundle) ScrollDown(ctx context.Context, clicks int) error {
	if err := b.checkAvailable("scroll"); err != nil {
		return err
	}
	return b.backend.ScrollDown(ctx, clicks)
}

func (b *InputBundle) ScrollLeft(ctx context.Context, clicks int) error {
	if err := b.checkAvailable("scroll"); err != nil {
		return err
	}
	return b.backend.ScrollLeft(ctx, clicks)
}

func (b *InputBundle) ScrollRight(ctx context.Context, clicks int) error {
	if err := b.checkAvailable("scroll"); err != nil {
		return err
	}
	return b.backend.ScrollRight(ctx, clicks)
}

func (b *InputBundle) PointerLocation(ctx context.Context) (int, int, error) {
	if err := b.checkAvailable("pointer"); err != nil {
		return 0, 0, err
	}
	return b.backend.PointerLocation(ctx)
}

func (b *InputBundle) Sync(ctx context.Context) error {
	if err := b.checkAvailable("sync"); err != nil {
		return err
	}
	type syncer interface {
		Sync(context.Context) error
	}
	if backend, ok := b.backend.(syncer); ok {
		return backend.Sync(ctx)
	}
	return nil
}

func (b *InputBundle) DragAndDrop(
	ctx context.Context,
	x1 int,
	y1 int,
	x2 int,
	y2 int,
) error {
	ctx = ctxutil.Default(ctx)
	if err := b.checkAvailable("drag"); err != nil {
		return err
	}
	if err := b.backend.MouseMove(ctx, x1, y1); err != nil {
		return err
	}
	if err := b.backend.MouseDown(ctx, 1); err != nil {
		return err
	}
	released := false
	defer func() {
		if !released {
			_ = b.backend.MouseUp(context.WithoutCancel(ctx), 1)
		}
	}()
	if err := b.backend.MouseMove(ctx, x2, y2); err != nil {
		return err
	}
	released = true
	return b.backend.MouseUp(ctx, 1)
}
