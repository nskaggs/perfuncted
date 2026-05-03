package perfuncted

import (
	"context"
	"fmt"
	"image"
	"time"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/util"
)

// InputBundle wraps per-compositor input backends.
type InputBundle struct {
	input.Inputter
	tracer *actionTracer
}

// Close delegates to the underlying Inputter Close method.
func (i InputBundle) Close() error {
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
	return i.TypeContext(context.Background(), text)
}

func (i InputBundle) TypeContext(ctx context.Context, text string) error {
	i.traceAction(fmt.Sprintf("type text=%q", text))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.Type(ctx, text)
}

func (i InputBundle) TypeWithDelay(text string, delay time.Duration) error {
	return i.TypeWithDelayContext(context.Background(), text, delay)
}

func (i InputBundle) TypeWithDelayContext(ctx context.Context, text string, delay time.Duration) error {
	i.traceAction(fmt.Sprintf("type-with-delay text=%q delay=%s", text, delay))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	for _, r := range text {
		if err := i.KeyTapContext(ctx, keyNameForRune(r)); err != nil {
			return err
		}
		// Use a reusable timer to avoid leaking timers in tight loops.
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

func keyNameForRune(r rune) string {
	switch r {
	case ' ':
		return "space"
	case '\n', '\r':
		return "enter"
	case '\t':
		return "tab"
	default:
		return string(r)
	}
}

// TypeFast attempts to paste text via the system clipboard (wl-copy/xclip)
// and a Ctrl+V paste key. If the clipboard tool isn't available or paste
// fails, it falls back to character-by-character Type().
func (i InputBundle) TypeFast(text string) error {
	return i.TypeFastContext(context.Background(), text)
}

func (i InputBundle) TypeFastContext(ctx context.Context, text string) error {
	i.traceAction(fmt.Sprintf("type-fast text=%q", text))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	cb, err := clipboard.Open()
	if err == nil {
		defer cb.Close()
		if err := cb.Set(ctx, text); err == nil {
			// Press Ctrl+V to paste. If this fails, fall back to Type().
			if err := i.Inputter.PressCombo(ctx, "ctrl+v"); err == nil {
				return nil
			}
		}
	}
	// fallback to per-character typing
	return i.Inputter.Type(ctx, text)
}

func (i InputBundle) KeyTap(key string) error {
	return i.KeyTapContext(context.Background(), key)
}

func (i InputBundle) KeyTapContext(ctx context.Context, key string) error {
	i.traceAction(fmt.Sprintf("key-tap key=%q", key))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.KeyTap(ctx, key)
}

func (i InputBundle) KeyDown(key string) error {
	return i.KeyDownContext(context.Background(), key)
}

func (i InputBundle) KeyDownContext(ctx context.Context, key string) error {
	i.traceAction(fmt.Sprintf("key-down key=%q", key))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.KeyDown(ctx, key)
}

func (i InputBundle) KeyUp(key string) error {
	return i.KeyUpContext(context.Background(), key)
}

func (i InputBundle) KeyUpContext(ctx context.Context, key string) error {
	i.traceAction(fmt.Sprintf("key-up key=%q", key))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.KeyUp(ctx, key)
}

func (i InputBundle) PressCombo(combo string) error {
	return i.PressComboContext(context.Background(), combo)
}

func (i InputBundle) PressComboContext(ctx context.Context, combo string) error {
	i.traceAction(fmt.Sprintf("press-combo combo=%q", combo))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.PressCombo(ctx, combo)
}

func (i InputBundle) MouseClick(x, y, button int) error {
	return i.MouseClickContext(context.Background(), x, y, button)
}

func (i InputBundle) MouseClickContext(ctx context.Context, x, y, button int) error {
	i.traceAction(fmt.Sprintf("mouse-click x=%d y=%d button=%d", x, y, button))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.MouseClick(ctx, x, y, button)
}

func (i InputBundle) ClickCenter(rect image.Rectangle) error {
	return i.ClickCenterContext(context.Background(), rect)
}

func (i InputBundle) ClickCenterContext(ctx context.Context, rect image.Rectangle) error {
	i.traceAction(fmt.Sprintf("click-center rect=%s", rect))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	x, y := rect.Min.X+rect.Dx()/2, rect.Min.Y+rect.Dy()/2
	return i.Inputter.MouseClick(ctx, x, y, 1)
}

func (i InputBundle) DoubleClick(x, y int) error {
	return i.DoubleClickContext(context.Background(), x, y)
}

func (i InputBundle) DoubleClickContext(ctx context.Context, x, y int) error {
	i.traceAction(fmt.Sprintf("double-click x=%d y=%d", x, y))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	// Move once to target and emulate two quick down/up pairs to better match
	// a real double-click (avoids redundant extra move calls).
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
	return i.MouseMoveContext(context.Background(), x, y)
}

func (i InputBundle) MouseMoveContext(ctx context.Context, x, y int) error {
	i.traceAction(fmt.Sprintf("mouse-move x=%d y=%d", x, y))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.MouseMove(ctx, x, y)
}

func (i InputBundle) MouseDown(button int) error {
	return i.MouseDownContext(context.Background(), button)
}

func (i InputBundle) MouseDownContext(ctx context.Context, button int) error {
	i.traceAction(fmt.Sprintf("mouse-down button=%d", button))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.MouseDown(ctx, button)
}

func (i InputBundle) MouseUp(button int) error {
	return i.MouseUpContext(context.Background(), button)
}

func (i InputBundle) MouseUpContext(ctx context.Context, button int) error {
	i.traceAction(fmt.Sprintf("mouse-up button=%d", button))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.MouseUp(ctx, button)
}

func (i InputBundle) ScrollUp(clicks int) error {
	return i.ScrollUpContext(context.Background(), clicks)
}

func (i InputBundle) ScrollUpContext(ctx context.Context, clicks int) error {
	i.traceAction(fmt.Sprintf("scroll-up clicks=%d", clicks))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.ScrollUp(ctx, clicks)
}

func (i InputBundle) ScrollDown(clicks int) error {
	return i.ScrollDownContext(context.Background(), clicks)
}

func (i InputBundle) ScrollDownContext(ctx context.Context, clicks int) error {
	i.traceAction(fmt.Sprintf("scroll-down clicks=%d", clicks))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.ScrollDown(ctx, clicks)
}

func (i InputBundle) ScrollLeft(clicks int) error {
	return i.ScrollLeftContext(context.Background(), clicks)
}

func (i InputBundle) ScrollLeftContext(ctx context.Context, clicks int) error {
	i.traceAction(fmt.Sprintf("scroll-left clicks=%d", clicks))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.ScrollLeft(ctx, clicks)
}

func (i InputBundle) ScrollRight(clicks int) error {
	return i.ScrollRightContext(context.Background(), clicks)
}

func (i InputBundle) ScrollRightContext(ctx context.Context, clicks int) error {
	i.traceAction(fmt.Sprintf("scroll-right clicks=%d", clicks))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.ScrollRight(ctx, clicks)
}

func (i InputBundle) Scroll(dx, dy int) error {
	return i.ScrollContext(context.Background(), dx, dy)
}

func (i InputBundle) ScrollContext(ctx context.Context, dx, dy int) error {
	i.traceAction(fmt.Sprintf("scroll dx=%d dy=%d", dx, dy))
	if dx > 0 {
		return i.ScrollRightContext(ctx, dx)
	} else if dx < 0 {
		return i.ScrollLeftContext(ctx, -dx)
	}
	if dy > 0 {
		return i.ScrollUpContext(ctx, dy)
	} else if dy < 0 {
		return i.ScrollDownContext(ctx, -dy)
	}
	return nil
}

func (i InputBundle) DragAndDrop(x1, y1, x2, y2 int) error {
	return i.DragAndDropContext(context.Background(), x1, y1, x2, y2)
}

func (i InputBundle) DragAndDropContext(ctx context.Context, x1, y1, x2, y2 int) error {
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
	// Ensure the button is released even if subsequent operations fail.
	defer func() { _ = i.Inputter.MouseUp(context.Background(), 1) }()
	if err := i.Inputter.MouseMove(ctx, x2, y2); err != nil {
		return err
	}
	return i.Inputter.MouseUp(ctx, 1)
}

func (i InputBundle) ModifierDown(mod string) error {
	return i.ModifierDownContext(context.Background(), mod)
}

func (i InputBundle) ModifierDownContext(ctx context.Context, mod string) error {
	i.traceAction(fmt.Sprintf("modifier-down mod=%q", mod))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.KeyDown(ctx, mod)
}

func (i InputBundle) ModifierUp(mod string) error {
	return i.ModifierUpContext(context.Background(), mod)
}

func (i InputBundle) ModifierUpContext(ctx context.Context, mod string) error {
	i.traceAction(fmt.Sprintf("modifier-up mod=%q", mod))
	if err := i.checkAvailable(); err != nil {
		return err
	}
	return i.Inputter.KeyUp(ctx, mod)
}
