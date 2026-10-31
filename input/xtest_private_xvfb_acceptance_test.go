//go:build linux
// +build linux

package input

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11test"
)

type privateXvfbEventResult struct {
	event xgb.Event
	err   error
}

func TestXTestBackendAgainstPrivateXvfb(t *testing.T) {
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb is unavailable")
	}
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		t.Fatalf("start private Xvfb: %v", err)
	}
	t.Cleanup(stop)
	observer, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatalf("connect event observer to private Xvfb: %v", err)
	}
	readerDone := make(chan struct{})
	readerStarted := false
	t.Cleanup(func() {
		observer.Close()
		if readerStarted {
			<-readerDone
		}
	})
	screen := xproto.Setup(observer).DefaultScreen(observer)
	window, err := xproto.NewWindowId(observer)
	if err != nil {
		t.Fatalf("allocate observer window: %v", err)
	}
	eventMask := uint32(xproto.EventMaskKeyPress | xproto.EventMaskKeyRelease | xproto.EventMaskButtonPress | xproto.EventMaskButtonRelease | xproto.EventMaskPointerMotion)
	if err := xproto.CreateWindowChecked(observer, screen.RootDepth, window, screen.Root, 0, 0, 200, 200, 0, xproto.WindowClassInputOutput, screen.RootVisual, xproto.CwEventMask, []uint32{eventMask}).Check(); err != nil {
		t.Fatalf("create observer window: %v", err)
	}
	if err := xproto.MapWindowChecked(observer, window).Check(); err != nil {
		t.Fatalf("map observer window: %v", err)
	}
	if err := xproto.SetInputFocusChecked(observer, xproto.InputFocusNone, window, xproto.TimeCurrentTime).Check(); err != nil {
		t.Fatalf("focus observer window: %v", err)
	}
	observer.Sync()
	events := make(chan privateXvfbEventResult, 64)
	readerStarted = true
	go func() {
		defer close(readerDone)
		for {
			event, err := observer.WaitForEvent()
			events <- privateXvfbEventResult{event: event, err: err}
			if event == nil || err != nil {
				return
			}
		}
	}()

	backend, err := NewXTestBackend(display)
	if err != nil {
		t.Fatalf("connect XTEST backend to private Xvfb: %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := backend.MouseMove(ctx, 113, 57); err != nil {
		t.Fatalf("move pointer on private Xvfb: %v", err)
	}
	if x, y, err := backend.PointerLocation(ctx); err != nil || x != 113 || y != 57 {
		t.Fatalf("pointer after XTEST motion = (%d,%d), %v; want (113,57)", x, y, err)
	}
	awaitPrivateXvfbEvent(t, ctx, events, window, xproto.MotionNotify, 0)
	keycode, err := backend.keycodeFor("a")
	if err != nil {
		t.Fatalf("resolve XTEST keycode: %v", err)
	}
	if err := backend.KeyDown(ctx, "a"); err != nil {
		t.Fatalf("press key on private Xvfb: %v", err)
	}
	awaitPrivateXvfbEvent(t, ctx, events, window, xproto.KeyPress, byte(keycode))
	if err := backend.KeyUp(ctx, "a"); err != nil {
		t.Fatalf("release key on private Xvfb: %v", err)
	}
	awaitPrivateXvfbEvent(t, ctx, events, window, xproto.KeyRelease, byte(keycode))
	if err := backend.MouseClick(ctx, 113, 57, 1); err != nil {
		t.Fatalf("click on private Xvfb: %v", err)
	}
	awaitPrivateXvfbEvent(t, ctx, events, window, xproto.ButtonPress, 1)
	awaitPrivateXvfbEvent(t, ctx, events, window, xproto.ButtonRelease, 1)
	if err := backend.MouseDown(ctx, 1); err != nil {
		t.Fatalf("press button on private Xvfb: %v", err)
	}
	awaitPrivateXvfbEvent(t, ctx, events, window, xproto.ButtonPress, 1)
	if err := backend.MouseUp(ctx, 1); err != nil {
		t.Fatalf("release button on private Xvfb: %v", err)
	}
	awaitPrivateXvfbEvent(t, ctx, events, window, xproto.ButtonRelease, 1)
	for _, scroll := range []struct {
		name string
		call func(context.Context, int) error
		btn  byte
	}{
		{name: "up", call: backend.ScrollUp, btn: 4},
		{name: "down", call: backend.ScrollDown, btn: 5},
		{name: "left", call: backend.ScrollLeft, btn: 6},
		{name: "right", call: backend.ScrollRight, btn: 7},
	} {
		if err := scroll.call(ctx, 1); err != nil {
			t.Fatalf("scroll %s on private Xvfb: %v", scroll.name, err)
		}
		awaitPrivateXvfbEvent(t, ctx, events, window, xproto.ButtonPress, scroll.btn)
		awaitPrivateXvfbEvent(t, ctx, events, window, xproto.ButtonRelease, scroll.btn)
	}
	if err := backend.TypeLiteral(ctx, "a"); err != nil {
		t.Fatalf("type literal on private Xvfb: %v", err)
	}
	awaitPrivateXvfbEvent(t, ctx, events, window, xproto.KeyPress, byte(keycode))
	awaitPrivateXvfbEvent(t, ctx, events, window, xproto.KeyRelease, byte(keycode))
	if err := backend.Sync(ctx); err != nil {
		t.Fatalf("synchronize private Xvfb input: %v", err)
	}
}

func awaitPrivateXvfbEvent(t *testing.T, ctx context.Context, events <-chan privateXvfbEventResult, window xproto.Window, eventType, detail byte) {
	t.Helper()
	for {
		select {
		case result := <-events:
			if result.err != nil {
				t.Fatalf("read event from private Xvfb: %v", result.err)
			}
			if result.event == nil {
				t.Fatal("private Xvfb event stream closed before expected input")
			}
			var gotType, gotDetail byte
			var gotWindow xproto.Window
			switch event := result.event.(type) {
			case xproto.KeyPressEvent:
				gotType, gotDetail, gotWindow = xproto.KeyPress, byte(event.Detail), event.Event
			case xproto.KeyReleaseEvent:
				gotType, gotDetail, gotWindow = xproto.KeyRelease, byte(event.Detail), event.Event
			case xproto.ButtonPressEvent:
				gotType, gotDetail, gotWindow = xproto.ButtonPress, byte(event.Detail), event.Event
			case xproto.ButtonReleaseEvent:
				gotType, gotDetail, gotWindow = xproto.ButtonRelease, byte(event.Detail), event.Event
			case xproto.MotionNotifyEvent:
				gotType, gotWindow = xproto.MotionNotify, event.Event
			}
			if gotType == eventType && gotDetail == detail && gotWindow == window {
				return
			}
		case <-ctx.Done():
			t.Fatalf("no private Xvfb event type=%d detail=%d window=%d: %v", eventType, detail, window, ctx.Err())
		}
	}
}
