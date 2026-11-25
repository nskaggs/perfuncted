package window

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"strconv"
	"strings"
	"testing"
)

func TestGnomeManagerNilReceiverReturnsError(t *testing.T) {
	var manager *GnomeManager
	if _, err := manager.eval(context.Background(), `"ok"`); err == nil || !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("eval error = %v, want initialization error", err)
	}
}

func TestGnomeManagerNilReceiverCloseIsSafe(t *testing.T) {
	var manager *GnomeManager
	if err := manager.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
}

func TestGnomeManagerRejectsIDsThatLoseJavaScriptPrecision(t *testing.T) {
	manager := &GnomeManager{}
	id := strconv.FormatUint(maxGnomeJSSafeInteger+1, 10)

	err := manager.actOnWindowByID(context.Background(), id, `w.activate()`)
	if err == nil || !strings.Contains(err.Error(), "JavaScript safe integer range") {
		t.Fatalf("actOnWindowByID(%q) error = %v, want safe-integer error", id, err)
	}
}

// A handle that no longer names a window must be reported as ErrWindowNotFound,
// the same sentinel InfoByID and every other backend use. Callers decide whether
// to retry on it, so a generic script failure is not enough.
func TestGnomeActOnWindowByIDReportsMissingWindow(t *testing.T) {
	var script string
	manager := &GnomeManager{evalJS: func(_ context.Context, js string) (string, error) {
		script = js
		return gnomeWindowMissing, nil
	}}

	for _, action := range []string{
		`w.activate(global.get_current_time())`,
		`w.move_frame(true, 1, 2)`,
		`w.delete(global.get_current_time())`,
	} {
		script = ""
		err := manager.actOnWindowByID(context.Background(), "17", action)
		if !errors.Is(err, ErrWindowNotFound) {
			t.Fatalf("actOnWindowByID(%q) error = %v, want ErrWindowNotFound", action, err)
		}

		// The snippet must return the marker before running the action, so a
		// window that exists still has its action applied.
		if !strings.Contains(script, strconv.Quote(gnomeWindowMissing)) {
			t.Fatalf("script does not return the missing marker:\n%s", script)
		}
		if strings.Contains(script, "throw") {
			t.Fatalf("script still signals a missing window by throwing text:\n%s", script)
		}
		marker := strings.Index(script, "return "+strconv.Quote(gnomeWindowMissing))
		at := strings.Index(script, action)
		if marker < 0 || at < 0 || marker > at {
			t.Fatalf("script must check for a missing window before the action %q:\n%s", action, script)
		}
	}
}

// A window that exists must succeed, and a failure of the action itself must
// stay distinguishable from a missing window.
func TestGnomeActOnWindowByIDSeparatesActionFailureFromMissingWindow(t *testing.T) {
	manager := &GnomeManager{evalJS: func(context.Context, string) (string, error) {
		return "ok", nil
	}}
	if err := manager.actOnWindowByID(context.Background(), "17", `w.activate()`); err != nil {
		t.Fatalf("actOnWindowByID on an existing window = %v, want nil", err)
	}

	failing := &GnomeManager{evalJS: func(context.Context, string) (string, error) {
		return "", errors.New("gnome: eval failed: w is not a function")
	}}
	err := failing.actOnWindowByID(context.Background(), "17", `w.nope()`)
	if err == nil {
		t.Fatal("a failing action reported success")
	}
	if errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("an action failure was reported as a missing window: %v", err)
	}
}

// The marker must not be reachable as a normal result, so an action that
// legitimately returns it cannot be confused with a missing window.
func TestGnomeWindowMissingMarkerIsDistinctFromTheSuccessResult(t *testing.T) {
	if gnomeWindowMissing == "ok" {
		t.Fatal("the missing-window marker collides with the success result")
	}
}

func TestGnomeNativeActionConvertsOnlyMissingWindowFailures(t *testing.T) {
	manager := &GnomeNativeManager{bridge: &gnomebridge.Client{}}
	arbitrary := errors.New("synthetic bridge transport error")
	cases := []struct {
		name          string
		callbackError error
		want          error
	}{
		{"success", nil, nil},
		{"missing window", gnomebridge.ErrObjectNotFound, ErrWindowNotFound},
		{"wrapped missing window", fmt.Errorf("bridge: %w", gnomebridge.ErrObjectNotFound), ErrWindowNotFound},
		{"transport failure", arbitrary, arbitrary},
		{"cancellation", context.Canceled, context.Canceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := 0
			err := manager.action(nil, func(ctx context.Context) error { //nolint:staticcheck // Exercise default-context behavior explicitly.
				called++
				if ctx == nil {
					t.Fatal("action did not default nil context")
				}
				return tc.callbackError
			})
			if called != 1 {
				t.Fatalf("callback called %d times, want 1", called)
			}
			if tc.want == nil {
				if err != nil {
					t.Fatalf("successful action returned %v", err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("action error %v does not preserve %v", err, tc.want)
			}
			if errors.Is(tc.want, ErrWindowNotFound) && errors.Is(err, gnomebridge.ErrObjectNotFound) {
				t.Fatalf("internal bridge error leaked to window API: %v", err)
			}
		})
	}
}

func TestGnomeNativeActionDoesNotRunAgainstUninitializedManager(t *testing.T) {
	for _, manager := range []*GnomeNativeManager{nil, {}} {
		called := false
		err := manager.action(context.Background(), func(context.Context) error {
			called = true
			return nil
		})
		if called || err == nil {
			t.Fatalf("uninitialized manager invoked callback=%t, error=%v", called, err)
		}
	}
}

func TestGnomeNativeManagerFailsClosedOnUninitializedAndDisconnectedBridge(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		manager *GnomeNativeManager
	}{
		{"missing bridge", &GnomeNativeManager{}},
		{"unreachable bridge", &GnomeNativeManager{bridge: &gnomebridge.Client{}}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			m := fixture.manager
			methods := []struct {
				name   string
				invoke func() error
			}{
				{"activate", func() error { return m.ActivateByID(context.Background(), "17") }},
				{"move", func() error { return m.MoveByID(context.Background(), "17", 30, 40) }},
				{"resize", func() error { return m.ResizeByID(context.Background(), "17", 300, 200) }},
				{"close", func() error { return m.CloseWindowByID(context.Background(), "17") }},
				{"minimize", func() error { return m.MinimizeByID(context.Background(), "17") }},
				{"maximize", func() error { return m.MaximizeByID(context.Background(), "17") }},
				{"fullscreen", func() error { return m.FullscreenByID(context.Background(), "17") }},
				{"unfullscreen", func() error { return m.UnfullscreenByID(context.Background(), "17") }},
				{"restore", func() error { return m.RestoreByID(context.Background(), "17") }},
				{"sync", func() error { return m.Sync(context.Background()) }},
			}
			for _, tc := range methods {
				t.Run(tc.name, func(t *testing.T) {
					if err := tc.invoke(); err == nil {
						t.Fatal("unavailable GNOME service accepted a mutation")
					}
				})
			}
			if windows, err := m.List(context.Background()); err == nil || len(windows) != 0 {
				t.Fatalf("unavailable window listing: %+v %v", windows, err)
			}
			if title, err := m.ActiveTitle(context.Background()); err == nil || title != "" {
				t.Fatalf("unavailable active title: %q %v", title, err)
			}
			if info, err := m.InfoByID(context.Background(), "17"); err == nil || info.NativeID != "" {
				t.Fatalf("unavailable node info: %+v %v", info, err)
			}
			var count int
			for _, err := range m.IterateWindows(context.Background()) {
				count++
				if err == nil {
					t.Fatal("iterator acknowledged unavailable bus")
				}
			}
			if count != 1 {
				t.Fatalf("iterator did not surface a single transport error: %d", count)
			}
			if err := m.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
		})
	}
}

func TestGnomeNativeManagerRejectsMalformedGeometryBeforeTransport(t *testing.T) {
	m := &GnomeNativeManager{bridge: &gnomebridge.Client{}}
	for _, tc := range []struct {
		name   string
		invoke func() error
	}{
		{"negative width", func() error { return m.ResizeByID(context.Background(), "17", -1, 200) }},
		{"zero height", func() error { return m.ResizeByID(context.Background(), "17", 300, 0) }},
		{"overflow x", func() error { return m.MoveByID(context.Background(), "17", 1<<33, 20) }},
		{"overflow width", func() error { return m.ResizeByID(context.Background(), "17", 1<<33, 20) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.invoke()
			if err == nil || strings.Contains(err.Error(), "client is closed") {
				t.Fatalf("invalid geometry reached disconnected transport: %v", err)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.ActivateByID(canceled, "17"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled activation was not rejected: %v", err)
	}
}

func TestGNOMERuntimeUnavailableBridgeNeverSelectsUnsafeX11Fallback(t *testing.T) {
	for _, desktop := range []string{"GNOME", "ubuntu:GNOME", "GNOME-Classic"} {
		t.Run(desktop, func(t *testing.T) {
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=/nonexistent/perfuncted-gnome-synthetic",
				"XDG_CURRENT_DESKTOP=" + desktop,
				"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-gnome-bus",
				"DISPLAY=:450",
			})
			if kind := compositor.DetectRuntime(rt); kind != compositor.GNOME {
				t.Fatalf("unexpected compositor %v", kind)
			}
			manager, err := OpenRuntimeContext(context.Background(), rt)
			if manager != nil {
				defer manager.Close()
				t.Fatalf("unavailable GNOME backend selected %T", manager)
			}
			if err == nil || !strings.Contains(err.Error(), "GNOME Shell Eval unavailable") {
				t.Fatalf("GNOME fallback accepted unavailable bus or X11 desktop: %v", err)
			}
		})
	}
}

func TestGNOMERuntimeSetupAndProbeCancellationAndUnreachableBus(t *testing.T) {
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/nonexistent/perfuncted-gnome-synthetic",
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-gnome-bus",
	})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if manager, err := openGNOMERuntimeContext(canceled, rt); manager != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled GNOME opener=%T %v", manager, err)
	}
	for _, probe := range []struct {
		name      string
		available bool
		reason    string
	}{
		{"gnome-shell-eval", false, ""},
		{"kwin-scripting", false, "not a KDE Plasma session"},
	} {
		var gotName, gotReason string
		var available bool
		switch probe.name {
		case "gnome-shell-eval":
			got := checkGnomeShellEval(rt, compositor.GNOME)
			gotName, gotReason, available = got.Name, got.Reason, got.Available
		case "kwin-scripting":
			got := checkKWinScript(rt, compositor.GNOME)
			gotName, gotReason, available = got.Name, got.Reason, got.Available
		}
		if gotName != probe.name || available || gotReason == "" {
			t.Fatalf("%s probe result: %q %t %q", probe.name, gotName, available, gotReason)
		}
	}
	// A KDE-labelled but unreachable D-Bus service must not be considered
	// available just because its desktop label matches.
	if got := checkKWinScript(rt, compositor.KDE); got.Available || !strings.Contains(got.Reason, "D-Bus unavailable") {
		t.Fatalf("KWin probe incorrectly authorized: %+v", got)
	}
}

func TestGNOMENativeAndSwayAdvertisedWindowOperationsAreIndependent(t *testing.T) {
	native := (&GnomeNativeManager{}).SupportedOperations()
	if len(native) == 0 || native[len(native)-1] != "events" {
		t.Fatalf("GNOME operations missing event capability: %v", native)
	}
	native[0] = "MUTATED"
	again := (&GnomeNativeManager{}).SupportedOperations()
	if again[0] == "MUTATED" {
		t.Fatal("returned operations shared mutable state")
	}
	if err := (&SwayManager{}).RestoreByID(context.Background(), "not-a-real-ID"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("unsupported Sway restore result: %v", err)
	}
}
