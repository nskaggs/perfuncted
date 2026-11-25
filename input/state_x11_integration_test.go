package input

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/x11test"
)

func TestInputStateX11ControlledIntegration(t *testing.T) {
	if os.Getenv("PF_X11_OBSERVATION_INTEGRATION") != "1" {
		t.Skip("requires controlled X11 integration")
	}
	display, stop, err := x11test.StartXvfbNoReset()
	if err != nil {
		t.Fatalf("start owned Xvfb: %v", err)
	}
	t.Cleanup(stop)
	time.Sleep(350 * time.Millisecond)
	t.Setenv("DISPLAY", display)
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("WAYLAND_SOCKET", "")

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	backend, err := OpenObserver(ctx, env.Current())
	if err != nil {
		t.Fatalf("open native observer: %v", err)
	}
	defer backend.Close()
	reader, ok := backend.(StateReader)
	if !ok {
		t.Fatal("X11 observer does not implement on-demand state queries")
	}

	injector, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatal(err)
	}
	defer injector.Close()
	if err := xtest.Init(injector); err != nil {
		t.Fatal(err)
	}
	root := xproto.Setup(injector).DefaultScreen(injector).Root
	inject := func(kind, code byte) {
		t.Helper()
		if err := xtest.FakeInputChecked(injector, kind, code, xproto.TimeCurrentTime, root, 0, 0, 0).Check(); err != nil {
			t.Fatalf("inject controlled XTest event: %v", err)
		}
	}
	keycode := func(symbol xproto.Keysym) byte {
		t.Helper()
		codes, err := x11KeycodesForSymbols(injector, []xproto.Keysym{symbol})
		if err != nil || len(codes) == 0 {
			t.Fatalf("find keycode for keysym %#x: %v", symbol, err)
		}
		return byte(codes[0])
	}

	f8 := keycode(keysymForName["f8"])
	if got, err := reader.KeyState(ctx, "F8"); err != nil || got.State != StateReleased {
		t.Fatalf("initial F8 state = %+v, %v", got, err)
	}
	inject(xproto.KeyPress, f8)
	if got, err := reader.KeyState(ctx, "f8"); err != nil || got.State != StatePressed || got.Provider != "x11-core" || got.Provenance != "unknown" {
		t.Fatalf("pressed F8 state = %+v, %v", got, err)
	}
	inject(xproto.KeyRelease, f8)
	if got, err := reader.KeyState(ctx, "f8"); err != nil || got.State != StateReleased {
		t.Fatalf("released F8 state = %+v, %v", got, err)
	}

	shift := keycode(stateKeysyms["shift"][0])
	inject(xproto.KeyPress, shift)
	if got, err := reader.KeyState(ctx, "shift"); err != nil || got.State != StatePressed {
		t.Fatalf("pressed Shift modifier = %+v, %v", got, err)
	}
	inject(xproto.KeyRelease, shift)
	if got, err := reader.KeyState(ctx, "shift"); err != nil || got.State != StateReleased {
		t.Fatalf("released Shift modifier = %+v, %v", got, err)
	}

	for _, name := range []string{"capslock", "numlock"} {
		t.Run(name, func(t *testing.T) {
			lockKey := keycode(stateLockKeysyms[name])
			initialLock, err := reader.LockState(ctx, name)
			if err != nil {
				t.Fatalf("initial %s mode: %v", name, err)
			}
			inject(xproto.KeyPress, lockKey)
			if got, err := reader.KeyState(ctx, name); err != nil || got.State != StatePressed {
				t.Fatalf("held %s key = %+v, %v", name, got, err)
			}
			lockWhileHeld, err := reader.LockState(ctx, name)
			if err != nil || lockWhileHeld.State == initialLock.State || (lockWhileHeld.State != StateEnabled && lockWhileHeld.State != StateDisabled) {
				t.Fatalf("%s mode while key held = %+v, %v", name, lockWhileHeld, err)
			}
			inject(xproto.KeyRelease, lockKey)
			if got, err := reader.KeyState(ctx, name); err != nil || got.State != StateReleased {
				t.Fatalf("released %s key = %+v, %v", name, got, err)
			}
			lockAfterRelease, err := reader.LockState(ctx, name)
			if err != nil || lockAfterRelease.State != lockWhileHeld.State {
				t.Fatalf("%s mode changed when key released: held=%+v released=%+v err=%v", name, lockWhileHeld, lockAfterRelease, err)
			}
		})
	}

	inject(xproto.ButtonPress, 1)
	if got, err := reader.MouseButtonState(ctx, 1); err != nil || got.State != StatePressed {
		t.Fatalf("pressed left button = %+v, %v", got, err)
	}
	inject(xproto.ButtonRelease, 1)
	if got, err := reader.MouseButtonState(ctx, 1); err != nil || got.State != StateReleased {
		t.Fatalf("released left button = %+v, %v", got, err)
	}
}
