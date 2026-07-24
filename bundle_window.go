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

// --- Handle-based operations ---

func (b *WindowBundle) ActivateByID(ctx context.Context, id uint64) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "activate-by-id %d", id)
	return b.Manager.ActivateByID(ctx, id)
}

func (b *WindowBundle) MoveByID(ctx context.Context, id uint64, x, y int) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "move-by-id %d %d,%d", id, x, y)
	return b.Manager.MoveByID(ctx, id, x, y)
}

func (b *WindowBundle) ResizeByID(ctx context.Context, id uint64, w, h int) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "resize-by-id %d %dx%d", id, w, h)
	return b.Manager.ResizeByID(ctx, id, w, h)
}

func (b *WindowBundle) CloseWindowByID(ctx context.Context, id uint64) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "close-window-by-id %d", id)
	return b.Manager.CloseWindowByID(ctx, id)
}

func (b *WindowBundle) MinimizeByID(ctx context.Context, id uint64) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "minimize-by-id %d", id)
	return b.Manager.MinimizeByID(ctx, id)
}

func (b *WindowBundle) MaximizeByID(ctx context.Context, id uint64) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "maximize-by-id %d", id)
	return b.Manager.MaximizeByID(ctx, id)
}

func (b *WindowBundle) FullscreenByID(ctx context.Context, id uint64) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "fullscreen-by-id %d", id)
	return b.Manager.FullscreenByID(ctx, id)
}

func (b *WindowBundle) UnfullscreenByID(ctx context.Context, id uint64) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "unfullscreen-by-id %d", id)
	return b.Manager.UnfullscreenByID(ctx, id)
}

func (b *WindowBundle) RestoreByID(ctx context.Context, id uint64) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "restore-by-id %d", id)
	return b.Manager.RestoreByID(ctx, id)
}

func (b *WindowBundle) InfoByID(ctx context.Context, id uint64) (window.Info, error) {
	if b.Manager == nil {
		return window.Info{}, &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "info-by-id %d", id)
	return b.Manager.InfoByID(ctx, id)
}

func (b *WindowBundle) WaitClosedByID(ctx context.Context, id uint64) error {
	if b.Manager == nil {
		return &CapabilityError{Cap: CapabilityWindows, Err: ErrNotAvailable}
	}
	b.traceAction("window", "wait-closed-by-id %d", id)
	return b.Manager.WaitClosedByID(ctx, id)
}
