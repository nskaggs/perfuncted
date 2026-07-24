package perfuncted

import (
	"context"

	"github.com/nskaggs/perfuncted/input"
)

type InputBundle struct {
	input.Inputter
	bundleBase
}

func (b *InputBundle) KeyDown(ctx context.Context, key string) error {
	if b.Inputter == nil {
		return &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "key-down %s", key)
	return b.Inputter.KeyDown(ctx, key)
}

func (b *InputBundle) KeyUp(ctx context.Context, key string) error {
	if b.Inputter == nil {
		return &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "key-up %s", key)
	return b.Inputter.KeyUp(ctx, key)
}

func (b *InputBundle) Type(ctx context.Context, text string) error {
	if b.Inputter == nil {
		return &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "type")
	return b.Inputter.Type(ctx, text)
}

func (b *InputBundle) MouseMove(ctx context.Context, x, y int) error {
	if b.Inputter == nil {
		return &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "mouse-move %d,%d", x, y)
	return b.Inputter.MouseMove(ctx, x, y)
}

func (b *InputBundle) MouseClick(ctx context.Context, x, y, button int) error {
	if b.Inputter == nil {
		return &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "mouse-click %d,%d,%d", x, y, button)
	return b.Inputter.MouseClick(ctx, x, y, button)
}

func (b *InputBundle) MouseDown(ctx context.Context, button int) error {
	if b.Inputter == nil {
		return &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "mouse-down %d", button)
	return b.Inputter.MouseDown(ctx, button)
}

func (b *InputBundle) MouseUp(ctx context.Context, button int) error {
	if b.Inputter == nil {
		return &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "mouse-up %d", button)
	return b.Inputter.MouseUp(ctx, button)
}

func (b *InputBundle) ScrollUp(ctx context.Context, clicks int) error {
	if b.Inputter == nil {
		return &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "scroll-up %d", clicks)
	return b.Inputter.ScrollUp(ctx, clicks)
}

func (b *InputBundle) ScrollDown(ctx context.Context, clicks int) error {
	if b.Inputter == nil {
		return &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "scroll-down %d", clicks)
	return b.Inputter.ScrollDown(ctx, clicks)
}

func (b *InputBundle) ScrollLeft(ctx context.Context, clicks int) error {
	if b.Inputter == nil {
		return &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "scroll-left %d", clicks)
	return b.Inputter.ScrollLeft(ctx, clicks)
}

func (b *InputBundle) ScrollRight(ctx context.Context, clicks int) error {
	if b.Inputter == nil {
		return &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "scroll-right %d", clicks)
	return b.Inputter.ScrollRight(ctx, clicks)
}

func (b *InputBundle) PointerLocation(ctx context.Context) (int, int, error) {
	if b.Inputter == nil {
		return 0, 0, &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "pointer-location")
	return b.Inputter.PointerLocation(ctx)
}

func (b *InputBundle) Sync(ctx context.Context) error {
	if b.Inputter == nil {
		return &CapabilityError{Cap: CapabilityInput, Err: ErrNotAvailable}
	}
	b.traceAction("input", "sync")
	return b.Inputter.Sync(ctx)
}
