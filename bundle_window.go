package perfuncted

import (
	"context"
	"fmt"
	"image"
	"iter"
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

func (w WindowBundle) Activate(pattern string) error {
	return w.activateContext(context.Background(), pattern)
}

func (w WindowBundle) activateContext(ctx context.Context, pattern string) error {
	w.traceAction(fmt.Sprintf("activate pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Activate(ctx, pattern)
}

func (w WindowBundle) ActiveTitle() (string, error) {
	return w.activeTitleContext(context.Background())
}

func (w WindowBundle) activeTitleContext(ctx context.Context) (string, error) {
	w.traceAction("active-title")
	if err := w.checkAvailable(); err != nil {
		return "", err
	}
	return w.Manager.ActiveTitle(ctx)
}

func (w WindowBundle) CloseWindow(pattern string) error {
	return w.closeWindowContext(context.Background(), pattern)
}

func (w WindowBundle) closeWindowContext(ctx context.Context, pattern string) error {
	w.traceAction(fmt.Sprintf("close-window pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.CloseWindow(ctx, pattern)
}

func (w WindowBundle) Resize(pattern string, width, height int) error {
	return w.resizeContext(context.Background(), pattern, width, height)
}

func (w WindowBundle) resizeContext(ctx context.Context, pattern string, width, height int) error {
	w.traceAction(fmt.Sprintf("resize pattern=%q width=%d height=%d", pattern, width, height))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Resize(ctx, pattern, width, height)
}

func (w WindowBundle) Minimize(pattern string) error {
	return w.minimizeContext(context.Background(), pattern)
}

func (w WindowBundle) minimizeContext(ctx context.Context, pattern string) error {
	w.traceAction(fmt.Sprintf("minimize pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Minimize(ctx, pattern)
}

func (w WindowBundle) Maximize(pattern string) error {
	return w.maximizeContext(context.Background(), pattern)
}

func (w WindowBundle) maximizeContext(ctx context.Context, pattern string) error {
	w.traceAction(fmt.Sprintf("maximize pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Maximize(ctx, pattern)
}

func (w WindowBundle) Restore(pattern string) error {
	return w.restoreContext(context.Background(), pattern)
}

func (w WindowBundle) restoreContext(ctx context.Context, pattern string) error {
	w.traceAction(fmt.Sprintf("restore pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Restore(ctx, pattern)
}

func (w WindowBundle) waitFor(ctx context.Context, pattern string, poll time.Duration) (window.Info, error) {
	w.traceAction(fmt.Sprintf("wait-for pattern=%q poll=%s", pattern, poll))
	if err := w.checkAvailable(); err != nil {
		return window.Info{}, err
	}
	return window.WaitFor(ctx, w.Manager, pattern, poll)
}

func (w WindowBundle) waitForClose(ctx context.Context, pattern string, poll time.Duration) error {
	w.traceAction(fmt.Sprintf("wait-for-close pattern=%q poll=%s", pattern, poll))
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return window.WaitForClose(ctx, w.Manager, pattern, poll)
}

func (w WindowBundle) iterateWindows() iter.Seq2[window.Info, error] {
	return w.iterateWindowsContext(context.Background())
}

func (w WindowBundle) iterateWindowsContext(ctx context.Context) iter.Seq2[window.Info, error] {
	w.traceAction("iterate-windows")
	if err := w.checkAvailable(); err != nil {
		return func(yield func(window.Info, error) bool) {
			yield(window.Info{}, err)
		}
	}
	return w.Manager.IterateWindows(ctx)
}

func (w WindowBundle) waitForTitleChange(ctx context.Context, poll time.Duration) (string, error) {
	w.traceAction(fmt.Sprintf("wait-for-title-change poll=%s", poll))
	if err := w.checkAvailable(); err != nil {
		return "", err
	}
	initial, err := w.Manager.ActiveTitle(ctx)
	if err != nil {
		return "", err
	}

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
			current, err := w.Manager.ActiveTitle(ctx)
			if err != nil {
				return "", err
			}
			if current != initial {
				return current, nil
			}
		}
	}
}

func (w WindowBundle) isVisible(pattern string) bool {
	return w.isVisibleContext(context.Background(), pattern)
}

func (w WindowBundle) isVisibleContext(ctx context.Context, pattern string) bool {
	w.traceAction(fmt.Sprintf("is-visible pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return false
	}
	_, err := window.FindByTitle(ctx, w.Manager, pattern)
	return err == nil
}

func (w WindowBundle) FindByTitle(pattern string) (window.Info, error) {
	return w.findByTitleContext(context.Background(), pattern)
}

func (w WindowBundle) findByTitleContext(ctx context.Context, pattern string) (window.Info, error) {
	w.traceAction(fmt.Sprintf("find-by-title pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return window.Info{}, err
	}
	return window.FindByTitle(ctx, w.Manager, pattern)
}

func (w WindowBundle) getGeometry(pattern string) (image.Rectangle, error) {
	return w.getGeometryContext(context.Background(), pattern)
}

func (w WindowBundle) getGeometryContext(ctx context.Context, pattern string) (image.Rectangle, error) {
	w.traceAction(fmt.Sprintf("get-geometry pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return image.Rectangle{}, err
	}
	info, err := window.FindByTitle(ctx, w.Manager, pattern)
	if err != nil {
		return image.Rectangle{}, err
	}
	return image.Rect(info.X, info.Y, info.X+info.W, info.Y+info.H), nil
}

func (w WindowBundle) getProcess(pattern string) (int, error) {
	return w.getProcessContext(context.Background(), pattern)
}

func (w WindowBundle) getProcessContext(ctx context.Context, pattern string) (int, error) {
	w.traceAction(fmt.Sprintf("get-process pattern=%q", pattern))
	if err := w.checkAvailable(); err != nil {
		return 0, err
	}
	info, err := window.FindByTitle(ctx, w.Manager, pattern)
	if err != nil {
		return 0, err
	}
	return int(info.PID), nil
}
