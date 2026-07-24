package perfuncted

import (
	"context"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

type WindowBundle struct {
	window.Manager
	bundleBase
}

func (w WindowBundle) close() error {
	if w.Manager == nil {
		return nil
	}
	w.traceAction("window", "close")
	return w.Manager.Close()
}

func (w WindowBundle) checkAvailable() error {
	return checkAvailable(w.Manager, "window")
}

func (w WindowBundle) sync(ctx context.Context) error {
	type syncer interface {
		Sync(context.Context) error
	}
	if s, ok := w.Manager.(syncer); ok {
		return s.Sync(ctx)
	}
	return nil
}

func (w WindowBundle) Sync(ctx context.Context) error {
	return w.sync(ctx)
}

func (w WindowBundle) List(ctx context.Context) ([]window.Info, error) {
	w.traceAction("window", "list")
	if err := w.checkAvailable(); err != nil {
		return nil, err
	}
	return w.Manager.List(ctx)
}

func (w WindowBundle) Activate(ctx context.Context, pattern string) error {
	w.traceAction("window", "activate pattern=%q", pattern)
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Activate(ctx, pattern)
}

func (w WindowBundle) Move(ctx context.Context, pattern string, x, y int) error {
	w.traceAction("window", "move pattern=%q x=%d y=%d", pattern, x, y)
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Move(ctx, pattern, x, y)
}

func (w WindowBundle) ActiveTitle(ctx context.Context) (string, error) {
	w.traceAction("window", "active-title")
	if err := w.checkAvailable(); err != nil {
		return "", err
	}
	return w.Manager.ActiveTitle(ctx)
}

func (w WindowBundle) CloseWindow(ctx context.Context, pattern string) error {
	w.traceAction("window", "close-window pattern=%q", pattern)
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.CloseWindow(ctx, pattern)
}

func (w WindowBundle) Resize(ctx context.Context, pattern string, width, height int) error {
	w.traceAction("window", "resize pattern=%q width=%d height=%d", pattern, width, height)
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Resize(ctx, pattern, width, height)
}

func (w WindowBundle) Minimize(ctx context.Context, pattern string) error {
	w.traceAction("window", "minimize pattern=%q", pattern)
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Minimize(ctx, pattern)
}

func (w WindowBundle) Maximize(ctx context.Context, pattern string) error {
	w.traceAction("window", "maximize pattern=%q", pattern)
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Maximize(ctx, pattern)
}

func (w WindowBundle) Fullscreen(ctx context.Context, pattern string) error {
	w.traceAction("window", "fullscreen pattern=%q", pattern)
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Fullscreen(ctx, pattern)
}

func (w WindowBundle) Unfullscreen(ctx context.Context, pattern string) error {
	w.traceAction("window", "unfullscreen pattern=%q", pattern)
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Unfullscreen(ctx, pattern)
}

func (w WindowBundle) Restore(ctx context.Context, pattern string) error {
	w.traceAction("window", "restore pattern=%q", pattern)
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Restore(ctx, pattern)
}

func (w WindowBundle) FindByTitle(ctx context.Context, pattern string) (window.Info, error) {
	w.traceAction("window", "find-by-title pattern=%q", pattern)
	if err := w.checkAvailable(); err != nil {
		return window.Info{}, err
	}
	return window.FindByTitle(ctx, w.Manager, pattern)
}

func (w WindowBundle) WaitForClose(ctx context.Context, pattern string, poll time.Duration) error {
	w.traceAction("window", "wait-for-close pattern=%q poll=%s", pattern, poll)
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return window.WaitForClose(ctx, w.Manager, pattern, poll)
}
