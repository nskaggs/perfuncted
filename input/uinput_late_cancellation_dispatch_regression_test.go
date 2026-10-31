//go:build linux

package input

import (
	"context"
	"errors"
	"github.com/bendahl/uinput"
	"testing"
)

type uinputCancelOnKeyboardPress struct {
	*recordingKeyboard
	cancel context.CancelFunc
}

func (k *uinputCancelOnKeyboardPress) KeyPress(key int) error {
	err := k.recordingKeyboard.KeyPress(key)
	k.cancel()
	return err
}

var _ uinput.Keyboard = (*uinputCancelOnKeyboardPress)(nil)

type cancelOnTouchMove struct {
	*recordingTouchPad
	cancel context.CancelFunc
}

func (t *cancelOnTouchMove) MoveTo(x, y int32) error {
	err := t.recordingTouchPad.MoveTo(x, y)
	t.cancel()
	return err
}

var _ uinput.TouchPad = (*cancelOnTouchMove)(nil)

type cancelOnWheel struct {
	*recordingMouse
	cancel context.CancelFunc
}

func (m *cancelOnWheel) Wheel(horizontal bool, delta int32) error {
	err := m.recordingMouse.Wheel(horizontal, delta)
	m.cancel()
	return err
}

var _ uinput.Mouse = (*cancelOnWheel)(nil)

// A synthetic device can finish its wire write just as an operation is
// cancelled. Its successful return does not make the cancelled request succeed.
func TestUinputCompletedDeviceWriteCannotHideLateCallerCancellation(t *testing.T) {
	t.Run("literal keyboard", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		kb := &uinputCancelOnKeyboardPress{recordingKeyboard: &recordingKeyboard{}, cancel: cancel}
		b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
		if err := b.TypeLiteral(ctx, "a"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled keyboard write reported %v", err)
		}
		if len(kb.events) != 1 {
			t.Fatalf("device did not receive one real key write: %v", kb.events)
		}
	})
	t.Run("absolute move", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		tp := &cancelOnTouchMove{recordingTouchPad: &recordingTouchPad{}, cancel: cancel}
		b := &UinputBackend{touchpad: tp}
		if err := b.MouseMove(ctx, 45, 61); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled move reported %v", err)
		}
		if len(tp.events) != 1 {
			t.Fatalf("device did not receive one move: %v", tp.events)
		}
	})
	t.Run("scroll wheel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		mouse := &cancelOnWheel{recordingMouse: &recordingMouse{}, cancel: cancel}
		b := &UinputBackend{mouse: mouse}
		if err := b.ScrollDown(ctx, 2); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled scroll reported %v", err)
		}
		if events := mouse.eventsSnapshot(); len(events) != 1 || events[0] != "wheel:false:2" {
			t.Fatalf("device write not observed: %v", events)
		}
	})
}
