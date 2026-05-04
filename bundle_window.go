package perfuncted

import (
	"context"
	"fmt"
	"time"

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

// Activate raises and focuses the window matching pattern.
func (w WindowBundle) Activate(pattern string) error {
	w.traceAction(fmt.Sprintf("activate pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Activate(context.Background(), pattern)
}

// ActiveTitle returns the title of the currently focused window.
func (w WindowBundle) ActiveTitle() (string, error) {
	w.traceAction("active-title")
	if err := w.checkAvailable(); err != nil {
		return "", err
	}
	return w.Manager.ActiveTitle(context.Background())
}

// CloseWindow closes the window matching pattern.
func (w WindowBundle) CloseWindow(pattern string) error {
	w.traceAction(fmt.Sprintf("close-window pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.CloseWindow(context.Background(), pattern)
}

// Resize sets the dimensions of the window matching pattern.
func (w WindowBundle) Resize(pattern string, width, height int) error {
	w.traceAction(fmt.Sprintf("resize pattern=%q width=%d height=%d", pattern, width, height))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Resize(context.Background(), pattern, width, height)
}

// Minimize minimises the window matching pattern.
func (w WindowBundle) Minimize(pattern string) error {
	w.traceAction(fmt.Sprintf("minimize pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Minimize(context.Background(), pattern)
}

// Maximize maximises the window matching pattern.
func (w WindowBundle) Maximize(pattern string) error {
	w.traceAction(fmt.Sprintf("maximize pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Maximize(context.Background(), pattern)
}

// Restore restores the window matching pattern to its previous size.
func (w WindowBundle) Restore(pattern string) error {
	w.traceAction(fmt.Sprintf("restore pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Restore(context.Background(), pattern)
}

// FindByTitle returns the first window whose title contains pattern
// (case-insensitive).
func (w WindowBundle) FindByTitle(pattern string) (window.Info, error) {
	w.traceAction(fmt.Sprintf("find-by-title pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return window.Info{}, err
	}
	return window.FindByTitle(context.Background(), w.Manager, pattern)
}

// WaitForWindow polls until a window matching pattern is found, or ctx is
// cancelled.
func (w WindowBundle) WaitForWindow(ctx context.Context, pattern string, poll time.Duration) (window.Info, error) {
	w.traceAction(fmt.Sprintf("wait-for-window pattern=%q poll=%s", pattern, poll))
	if err := w.checkAvailable(); err != nil {
		return window.Info{}, err
	}
	return window.WaitFor(ctx, w.Manager, pattern, poll)
}

// WaitForClose polls until no window matches pattern (window is gone), or ctx
// is cancelled.
func (w WindowBundle) WaitForClose(ctx context.Context, pattern string, poll time.Duration) error {
	w.traceAction(fmt.Sprintf("wait-for-close pattern=%q poll=%s", pattern, poll))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return window.WaitForClose(ctx, w.Manager, pattern, poll)
}
