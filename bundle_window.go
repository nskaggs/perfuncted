package perfuncted

import (
	"context"
	"iter"

	"github.com/nskaggs/perfuncted/window"
)

type WindowBundle struct {
	window.Manager
	bundleBase
}

func (b *WindowBundle) List(ctx context.Context) ([]window.Info, error) {
	if b.Manager == nil {
		return nil, &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "list")
	return b.Manager.List(ctx)
}

func (b *WindowBundle) IterateWindows(ctx context.Context) iter.Seq2[window.Info, error] {
	if b.Manager == nil {
		return func(yield func(window.Info, error) bool) {
			yield(window.Info{}, &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable})
		}
	}
	b.traceAction("window", "iterate")
	return b.Manager.IterateWindows(ctx)
}

func (b *WindowBundle) Activate(ctx context.Context, title string) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "activate %s", title)
	return b.Manager.Activate(ctx, title)
}

func (b *WindowBundle) Move(ctx context.Context, title string, x, y int) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "move %s %d,%d", title, x, y)
	return b.Manager.Move(ctx, title, x, y)
}

func (b *WindowBundle) Resize(ctx context.Context, title string, w, h int) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "resize %s %dx%d", title, w, h)
	return b.Manager.Resize(ctx, title, w, h)
}

func (b *WindowBundle) ActiveTitle(ctx context.Context) (string, error) {
	if b.Manager == nil {
		return "", &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "active-title")
	return b.Manager.ActiveTitle(ctx)
}

func (b *WindowBundle) CloseWindow(ctx context.Context, title string) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "close-window %s", title)
	return b.Manager.CloseWindow(ctx, title)
}

func (b *WindowBundle) Minimize(ctx context.Context, title string) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "minimize %s", title)
	return b.Manager.Minimize(ctx, title)
}

func (b *WindowBundle) Maximize(ctx context.Context, title string) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "maximize %s", title)
	return b.Manager.Maximize(ctx, title)
}

func (b *WindowBundle) Fullscreen(ctx context.Context, title string) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "fullscreen %s", title)
	return b.Manager.Fullscreen(ctx, title)
}

func (b *WindowBundle) Unfullscreen(ctx context.Context, title string) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "unfullscreen %s", title)
	return b.Manager.Unfullscreen(ctx, title)
}

func (b *WindowBundle) Restore(ctx context.Context, title string) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "restore %s", title)
	return b.Manager.Restore(ctx, title)
}

func (b *WindowBundle) close() error {
	if b.Manager == nil {
		return nil
	}
	return b.Manager.Close()
}
