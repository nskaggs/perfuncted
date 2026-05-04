package perfuncted

import (
	"context"
	"fmt"

	"github.com/nskaggs/perfuncted/internal/util"
	"github.com/nskaggs/perfuncted/window"
)

// WindowBundle wraps window management utilities.
type WindowBundle struct {
	window.Manager
	tracer *actionTracer
}

// close delegates to the underlying Manager Close method.
func (w WindowBundle) close() error {
	if w.Manager == nil {
		return nil
	}
	w.traceAction("close")
	return w.Manager.Close()
}

func (w WindowBundle) checkAvailable() error {
	return util.CheckAvailable("window", w.Manager)
}

func (w WindowBundle) traceAction(msg string) {
	if w.tracer == nil {
		return
	}
	w.tracer.Tracef("window", "%s", msg)
}

func (w WindowBundle) ActivateContext(ctx context.Context, pattern string) error {
	w.traceAction(fmt.Sprintf("activate pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Activate(ctx, pattern)
}

func (w WindowBundle) ActiveTitleContext(ctx context.Context) (string, error) {
	w.traceAction("active-title")
	if err := w.checkAvailable(); err != nil {
		return "", err
	}
	return w.Manager.ActiveTitle(ctx)
}

func (w WindowBundle) CloseWindowContext(ctx context.Context, pattern string) error {
	w.traceAction(fmt.Sprintf("close-window pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.CloseWindow(ctx, pattern)
}

func (w WindowBundle) ResizeContext(ctx context.Context, pattern string, width, height int) error {
	w.traceAction(fmt.Sprintf("resize pattern=%q width=%d height=%d", pattern, width, height))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Resize(ctx, pattern, width, height)
}

func (w WindowBundle) MinimizeContext(ctx context.Context, pattern string) error {
	w.traceAction(fmt.Sprintf("minimize pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Minimize(ctx, pattern)
}

func (w WindowBundle) MaximizeContext(ctx context.Context, pattern string) error {
	w.traceAction(fmt.Sprintf("maximize pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Maximize(ctx, pattern)
}

func (w WindowBundle) RestoreContext(ctx context.Context, pattern string) error {
	w.traceAction(fmt.Sprintf("restore pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Restore(ctx, pattern)
}

func (w WindowBundle) FindByTitleContext(ctx context.Context, pattern string) (window.Info, error) {
	w.traceAction(fmt.Sprintf("find-by-title pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return window.Info{}, err
	}
	return window.FindByTitle(ctx, w.Manager, pattern)
}
