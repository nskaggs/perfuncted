package perfuncted

import (
	"context"
	"fmt"
	"image"
	"time"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/util"
)

// InputBundle wraps per-compositor input backends.
type InputBundle struct {
	input.Inputter
	tracer *actionTracer
}

// close delegates to the underlying Inputter Close method.
func (i InputBundle) close() error {
	if i.Inputter == nil {
		return nil
	}
	i.traceAction("close")
	return i.Inputter.Close()
}

func (i InputBundle) checkAvailable() error {
	return util.CheckAvailable("input", i.Inputter)
}

func (i InputBundle) traceAction(msg string) {
	if i.tracer == nil {
		return
	}
	i.tracer.Tracef("input", "%s", msg)
}

func (i InputBundle) Type(text string) error {
	return i.typeContext(context.Background(), text)
}

func (i InputBundle) typeContext(ctx context.Context, text string) error {
	i.traceAction(fmt.Sprintf("type text=%q", text))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.Type(ctx, text)
}

func (i InputBundle) KeyDown(key string) error {
	return i.keyDownContext(context.Background(), key)
}

func (i InputBundle) keyDownContext(ctx context.Context, key string) error {
	i.traceAction(fmt.Sprintf("key-down key=%q", key))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.KeyDown(ctx, key)
}

func (i InputBundle) KeyUp(key string) error {
	return i.keyUpContext(context.Background(), key)
}

func (i InputBundle) keyUpContext(ctx context.Context, key string) error {
	i.traceAction(fmt.Sprintf("key-up key=%q", key))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.KeyUp(ctx, key)
}

func (i InputBundle) MouseClick(x, y, button int) error {
	return i.mouseClickContext(context.Background(), x, y, button)
}

func (i InputBundle) mouseClickContext(ctx context.Context, x, y, button int) error {
	i.traceAction(fmt.Sprintf("mouse-click x=%d y=%d button=%d", x, y, button))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.MouseClick(ctx, x, y, button)
}

func (i InputBundle) ClickCenter(rect image.Rectangle) error {
	return i.clickCenterContext(context.Background(), rect)
}

func (i InputBundle) clickCenterContext(ctx context.Context, rect image.Rectangle) error {
	i.traceAction(fmt.Sprintf("click-center rect=%s", rect))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	x, y := rect.Min.X+rect.Dx()/2, rect.Min.Y+rect.Dy()/2
	return i.Inputter.MouseClick(ctx, x, y, 1)
}

func (i InputBundle) DoubleClick(x, y int) error {
	return i.doubleClickContext(context.Background(), x, y)
}

func (i InputBundle) doubleClickContext(ctx context.Context, x, y int) error {
	i.traceAction(fmt.Sprintf("double-click x=%d y=%d", x, y))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	if err := i.Inputter.MouseMove(ctx, x, y); err != nil {
		return err
	}
	if err := i.Inputter.MouseDown(ctx, 1); err != nil {
		return err
	}
	if err := i.Inputter.MouseUp(ctx, 1); err != nil {
		return err
	}
	// Small pause to emulate human double-click timing.
	time.Sleep(20 * time.Millisecond)
	if err := i.Inputter.MouseDown(ctx, 1); err != nil {
		return err
	}
	return i.Inputter.MouseUp(ctx, 1)
}

func (i InputBundle) MouseMove(x, y int) error {
	return i.mouseMoveContext(context.Background(), x, y)
}

func (i InputBundle) mouseMoveContext(ctx context.Context, x, y int) error {
	i.traceAction(fmt.Sprintf("mouse-move x=%d y=%d", x, y))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.MouseMove(ctx, x, y)
}

func (i InputBundle) MouseDown(button int) error {
	return i.mouseDownContext(context.Background(), button)
}

func (i InputBundle) mouseDownContext(ctx context.Context, button int) error {
	i.traceAction(fmt.Sprintf("mouse-down button=%d", button))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.MouseDown(ctx, button)
}

func (i InputBundle) MouseUp(button int) error {
	return i.mouseUpContext(context.Background(), button)
}

func (i InputBundle) mouseUpContext(ctx context.Context, button int) error {
	i.traceAction(fmt.Sprintf("mouse-up button=%d", button))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.MouseUp(ctx, button)
}

func (i InputBundle) ScrollUp(clicks int) error {
	return i.scrollUpContext(context.Background(), clicks)
}

func (i InputBundle) scrollUpContext(ctx context.Context, clicks int) error {
	i.traceAction(fmt.Sprintf("scroll-up clicks=%d", clicks))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.ScrollUp(ctx, clicks)
}

func (i InputBundle) ScrollDown(clicks int) error {
	return i.scrollDownContext(context.Background(), clicks)
}

func (i InputBundle) scrollDownContext(ctx context.Context, clicks int) error {
	i.traceAction(fmt.Sprintf("scroll-down clicks=%d", clicks))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.ScrollDown(ctx, clicks)
}

func (i InputBundle) ScrollLeft(clicks int) error {
	return i.scrollLeftContext(context.Background(), clicks)
}

func (i InputBundle) scrollLeftContext(ctx context.Context, clicks int) error {
	i.traceAction(fmt.Sprintf("scroll-left clicks=%d", clicks))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.ScrollLeft(ctx, clicks)
}

func (i InputBundle) ScrollRight(clicks int) error {
	return i.scrollRightContext(context.Background(), clicks)
}

func (i InputBundle) scrollRightContext(ctx context.Context, clicks int) error {
	i.traceAction(fmt.Sprintf("scroll-right clicks=%d", clicks))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.ScrollRight(ctx, clicks)
}

func (i InputBundle) DragAndDrop(x1, y1, x2, y2 int) error {
	return i.dragAndDropContext(context.Background(), x1, y1, x2, y2)
}

func (i InputBundle) dragAndDropContext(ctx context.Context, x1, y1, x2, y2 int) error {
	i.traceAction(fmt.Sprintf("drag-and-drop x1=%d y1=%d x2=%d y2=%d", x1, y1, x2, y2))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	if err := i.Inputter.MouseMove(ctx, x1, y1); err != nil {
		return err
	}
	if err := i.Inputter.MouseDown(ctx, 1); err != nil {
		return err
	}
	released := false
	defer func() {
		if !released {
			_ = i.Inputter.MouseUp(context.Background(), 1)
		}
	}()
	if err := i.Inputter.MouseMove(ctx, x2, y2); err != nil {
		return err
	}
	released = true
	return i.Inputter.MouseUp(ctx, 1)
}
