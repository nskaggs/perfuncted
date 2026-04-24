package perfuncted

import (
	"context"
	"image"
	"time"

	"github.com/nskaggs/perfuncted/internal/util"
	"github.com/nskaggs/perfuncted/window"
)

// WindowBundle wraps window management utilities.
type WindowBundle struct {
	window.Manager
}

func (w WindowBundle) checkAvailable() error {
	return util.CheckAvailable("window", w.Manager)
}

func (w WindowBundle) Activate(pattern string) error {
	return w.ActivateContext(context.Background(), pattern)
}

func (w WindowBundle) ActivateContext(ctx context.Context, pattern string) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Activate(ctx, pattern)
}

func (w WindowBundle) ActiveTitle() (string, error) {
	return w.ActiveTitleContext(context.Background())
}

func (w WindowBundle) ActiveTitleContext(ctx context.Context) (string, error) {
	if err := w.checkAvailable(); err != nil {
		return "", err
	}
	return w.Manager.ActiveTitle(ctx)
}

func (w WindowBundle) CloseWindow(pattern string) error {
	return w.CloseWindowContext(context.Background(), pattern)
}

func (w WindowBundle) CloseWindowContext(ctx context.Context, pattern string) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.CloseWindow(ctx, pattern)
}

func (w WindowBundle) Resize(pattern string, width, height int) error {
	return w.ResizeContext(context.Background(), pattern, width, height)
}

func (w WindowBundle) ResizeContext(ctx context.Context, pattern string, width, height int) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Resize(ctx, pattern, width, height)
}

func (w WindowBundle) Minimize(pattern string) error {
	return w.MinimizeContext(context.Background(), pattern)
}

func (w WindowBundle) MinimizeContext(ctx context.Context, pattern string) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Minimize(ctx, pattern)
}

func (w WindowBundle) Maximize(pattern string) error {
	return w.MaximizeContext(context.Background(), pattern)
}

func (w WindowBundle) MaximizeContext(ctx context.Context, pattern string) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Maximize(ctx, pattern)
}

func (w WindowBundle) Restore(pattern string) error {
	return w.RestoreContext(context.Background(), pattern)
}

func (w WindowBundle) RestoreContext(ctx context.Context, pattern string) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return w.Manager.Restore(ctx, pattern)
}

func (w WindowBundle) WaitFor(ctx context.Context, pattern string, poll time.Duration) (window.Info, error) {
	if err := w.checkAvailable(); err != nil {
		return window.Info{}, err
	}
	return window.WaitFor(ctx, w.Manager, pattern, poll)
}

func (w WindowBundle) WaitForClose(ctx context.Context, pattern string, poll time.Duration) error {
	if err := w.checkAvailable(); err != nil {
		return err
	}
	return window.WaitForClose(ctx, w.Manager, pattern, poll)
}

func (w WindowBundle) WaitForTitleChange(ctx context.Context, poll time.Duration) (string, error) {
	if err := w.checkAvailable(); err != nil {
		return "", err
	}
	initial, err := w.Manager.ActiveTitle(ctx)
	if err != nil {
		return "", err
	}
	for {
		current, err := w.Manager.ActiveTitle(ctx)
		if err != nil {
			return "", err
		}
		if current != initial {
			return current, nil
		}
		t := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return "", ctx.Err()
		case <-t.C:
		}
		if !t.Stop() {
			// drained
		}
	}
}

func (w WindowBundle) IsVisible(pattern string) bool {
	return w.IsVisibleContext(context.Background(), pattern)
}

func (w WindowBundle) IsVisibleContext(ctx context.Context, pattern string) bool {
	if err := w.checkAvailable(); err != nil {
		return false
	}
	_, err := window.FindByTitle(ctx, w.Manager, pattern)
	return err == nil
}

func (w WindowBundle) FindByTitle(pattern string) (window.Info, error) {
	return w.FindByTitleContext(context.Background(), pattern)
}

func (w WindowBundle) FindByTitleContext(ctx context.Context, pattern string) (window.Info, error) {
	if err := w.checkAvailable(); err != nil {
		return window.Info{}, err
	}
	return window.FindByTitle(ctx, w.Manager, pattern)
}

func (w WindowBundle) GetGeometry(pattern string) (image.Rectangle, error) {
	return w.GetGeometryContext(context.Background(), pattern)
}

func (w WindowBundle) GetGeometryContext(ctx context.Context, pattern string) (image.Rectangle, error) {
	if err := w.checkAvailable(); err != nil {
		return image.Rectangle{}, err
	}
	info, err := window.FindByTitle(ctx, w.Manager, pattern)
	if err != nil {
		return image.Rectangle{}, err
	}
	return image.Rect(info.X, info.Y, info.X+info.W, info.Y+info.H), nil
}

func (w WindowBundle) GetProcess(pattern string) (int, error) {
	return w.GetProcessContext(context.Background(), pattern)
}

func (w WindowBundle) GetProcessContext(ctx context.Context, pattern string) (int, error) {
	if err := w.checkAvailable(); err != nil {
		return 0, err
	}
	info, err := window.FindByTitle(ctx, w.Manager, pattern)
	if err != nil {
		return 0, err
	}
	return int(info.PID), nil
}
