package input

import (
	"context"
	"errors"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
	"os"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/x11test"
)

// Controlled test only: injected events establish XI2 transport delivery,
// not the provenance of real physical keyboard or mouse hardware.
// Enable with PF_X11_OBSERVATION_INTEGRATION=1; each test owns a private Xvfb.
// Each controlled integration owns a fresh Xvfb server with a confirmed
// display identity. Reusing a server after an unrelated test can reset
// connections on CI. Its process and display are cleaned up after the test.
func requireObservationX11TestServer(t *testing.T) string {
	t.Helper()
	if os.Getenv("PF_X11_OBSERVATION_INTEGRATION") != "1" {
		t.Skip("requires controlled native X11 integration")
	}
	display, stop, err := x11test.StartXvfbNoReset()
	if err != nil {
		t.Fatalf("start owned Xvfb: %v", err)
	}
	t.Cleanup(stop)
	// Xvfb's displayfd announces the socket before all extensions have
	// finished initializing. Give the isolated server a bounded settling
	// interval before the first XI2 version or keyboard-mapping request.
	time.Sleep(350 * time.Millisecond)
	t.Setenv("DISPLAY", display)
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("WAYLAND_SOCKET", "")
	return display
}

func TestObservationX11ControlledIntegration(t *testing.T) {
	display := requireObservationX11TestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	backend, err := OpenObserver(ctx, env.Current())
	if err != nil {
		t.Fatalf("open native observer: %v", err)
	}
	defer backend.Close()
	// Use a preinitialized XTEST connection on this isolated Xvfb server.
	// No production input-observation path depends on XTEST.
	injector, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatal(err)
	}
	defer injector.Close()
	if err := xtest.Init(injector); err != nil {
		t.Fatal(err)
	}
	keymap, err := loadXI2ChordMap(injector, "F8")
	if err != nil {
		t.Fatal(err)
	}
	var f8 byte
	for code := range keymap.target {
		f8 = code
		break
	}
	root := xproto.Setup(injector).DefaultScreen(injector).Root
	inject := func(kind byte, key byte) {
		t.Helper()
		if err := xtest.FakeInputChecked(injector, kind, key, xproto.TimeCurrentTime, root, 0, 0, 0).Check(); err != nil {
			t.Fatalf("inject controlled event: %v", err)
		}
	}
	for _, tt := range []struct {
		name    string
		filter  ObservationFilter
		press   byte
		release byte
		key     byte
	}{
		{"key", ObservationFilter{Combo: "F8", Transition: "both"}, xproto.KeyPress, xproto.KeyRelease, f8},
		{"button", ObservationFilter{Button: 1, Transition: "both"}, xproto.ButtonPress, xproto.ButtonRelease, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			listening, err := backend.Listen(ctx, tt.filter)
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer listening.Close()
			inject(tt.press, tt.key)
			inject(tt.release, tt.key)
			var presses, releases int
			timer := time.NewTimer(time.Second * 2)
			defer timer.Stop()
			for presses == 0 || releases == 0 {
				select {
				case <-timer.C:
					t.Fatalf("missing synthetic events press=%d release=%d err=%v", presses, releases, listening.Err())
				case ev, ok := <-listening.Events():
					if !ok {
						t.Fatalf("listener closed prematurely: %v", listening.Err())
					}
					if ev.Source != "x11-xi2" {
						t.Fatalf("unexpected source: %+v", ev)
					}
					if ev.Transition == "press" {
						presses++
					}
					if ev.Transition == "release" {
						releases++
					}
				}
			}
			if err := listening.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case _, ok := <-listening.Events():
				if ok {
					t.Fatal("unexpected event after closure")
				}
			case <-time.After(time.Second):
				t.Fatal("listener did not terminate")
			}
		})
	}
	listening, err := backend.Listen(ctx, ObservationFilter{Button: 1})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-listening.Events():
			if !ok {
				if !errors.Is(listening.Err(), context.Canceled) {
					t.Fatalf("cancellation error: %v", listening.Err())
				}
				return
			}
		case <-deadline:
			t.Fatal("cancellation failed to close listener")
		}
	}
}

// TestObservationX11CrossFocusIntegration proves that a global XI2 listener
// still sees the named hotkey while focus moves between different X windows.
// XTEST is used strictly for controlled CI input; it does not attest to
// physical keyboard provenance or grant broader backend permissions.
func TestObservationX11CrossFocusIntegration(t *testing.T) {
	display := requireObservationX11TestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	backend, err := OpenObserver(ctx, env.Current())
	if err != nil {
		t.Fatalf("open native X11 observer: %v", err)
	}
	defer backend.Close()
	listening, err := backend.Listen(ctx, ObservationFilter{Combo: "F8", Transition: "both"})
	if err != nil {
		t.Fatalf("register global hotkey: %v", err)
	}
	defer listening.Close()

	client, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := xtest.Init(client); err != nil {
		t.Fatal(err)
	}
	keymap, err := loadXI2ChordMap(client, "F8")
	if err != nil {
		t.Fatal(err)
	}
	var keycode byte
	for code := range keymap.target {
		keycode = code
		break
	}
	if keycode == 0 {
		t.Fatal("native X11 keymap has no F8 key")
	}
	screen := xproto.Setup(client).DefaultScreen(client)
	var windows []xproto.Window
	defer func() {
		for _, window := range windows {
			_ = xproto.DestroyWindowChecked(client, window).Check()
		}
	}()
	for i := 0; i < 2; i++ {
		id, err := xproto.NewWindowId(client)
		if err != nil {
			t.Fatal(err)
		}
		if err := xproto.CreateWindowChecked(client, screen.RootDepth, id, screen.Root,
			int16(20+i*250), 20, 160, 120, 0, xproto.WindowClassInputOutput,
			screen.RootVisual, 0, nil).Check(); err != nil {
			t.Fatal(err)
		}
		windows = append(windows, id)
		if err := xproto.MapWindowChecked(client, id).Check(); err != nil {
			t.Fatal(err)
		}
	}
	for i, window := range windows {
		if err := xproto.SetInputFocusChecked(client, xproto.InputFocusNone, window, xproto.TimeCurrentTime).Check(); err != nil {
			t.Fatalf("focus window %d: %v", i, err)
		}
		actual, err := xproto.GetInputFocus(client).Reply()
		if err != nil || actual.Focus != window {
			t.Fatalf("focus window %d not acknowledged: focus=%v err=%v", i, actual, err)
		}
		for _, kind := range []byte{xproto.KeyPress, xproto.KeyRelease} {
			if err := xtest.FakeInputChecked(client, kind, keycode, xproto.TimeCurrentTime, screen.Root, 0, 0, 0).Check(); err != nil {
				t.Fatalf("XTEST key event while window %d focused: %v", i, err)
			}
			want := "press"
			if kind == xproto.KeyRelease {
				want = "release"
			}
			select {
			case ev, ok := <-listening.Events():
				if !ok {
					t.Fatalf("global listener ended: %v", listening.Err())
				}
				if ev.Source != "x11-xi2" || ev.Transition != want {
					t.Fatalf("focus %d: wrong global event: %+v, want %s", i, ev, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("global F8 %s missing while window %d focused", want, i)
			}
		}
	}
}

func TestObservationX11SessionClose(t *testing.T) {
	requireObservationX11TestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	backend, err := OpenObserver(ctx, env.Current())
	if err != nil {
		t.Fatal(err)
	}
	listening, err := backend.Listen(ctx, ObservationFilter{Combo: "F8"})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-listening.Events():
		if ok {
			t.Fatal("event after close")
		}
		if !errors.Is(listening.Err(), ErrObservationRevoked) {
			t.Fatal(listening.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("close blocked")
	}
}

// OpenObserver's native-X11 gate must honor caller cancellation after a
// positively identified local X server, not only on unsupported Wayland.
func TestObservationX11CanceledAfterNativePeerVerification(t *testing.T) {
	display := requireObservationX11TestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if backend, err := openX11Observer(ctx, display); backend != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled native backend: %T %v", backend, err)
	}
}
