package window

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/compositor"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"iter"
	"strings"
	"testing"
	"time"
)

func TestSyntheticWindowProbeForeignToplevelPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		globals   map[string]bool
		available bool
		want      string
	}{
		{"no Wayland", nil, false, "no Wayland session"},
		{"no protocols", map[string]bool{}, false, "no foreign-toplevel"},
		{"ext list", map[string]bool{"ext_foreign_toplevel_list_v1": true}, true, "ext_foreign_toplevel_list_v1"},
		{"wlroots control preferred", map[string]bool{"ext_foreign_toplevel_list_v1": true, "zwlr_foreign_toplevel_manager_v1": true}, true, "zwlr_foreign_toplevel_manager_v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := checkForeignToplevel(tc.globals)
			if r.Name != "foreign-toplevel" || r.Available != tc.available || !strings.Contains(r.Reason, tc.want) {
				t.Fatalf("incorrect foreign-toplevel capability %+v", r)
			}
		})
	}
}
func TestSyntheticWindowProbeUnavailableCompositorServices(t *testing.T) {
	absent := env.FromEnviron(nil)
	brokenBus := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-window-bus"})
	for _, tc := range []struct {
		name    string
		rt      env.Runtime
		session compositor.Session
		want    string
	}{
		{"not KDE", absent, compositor.X11, "not a KDE"},
		{"KDE without bus", absent, compositor.KDE, "D-Bus unavailable"},
		{"KDE invalid bus", brokenBus, compositor.KDE, "D-Bus unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := checkKWinScript(tc.rt, tc.session)
			if r.Available || !strings.Contains(r.Reason, tc.want) {
				t.Fatalf("unexpected KWin probe %+v", r)
			}
		})
	}
	if r := checkGnomeShellEval(absent, compositor.X11); r.Available || !strings.Contains(r.Reason, "not a GNOME") {
		t.Fatalf("X11 GNOME %+v", r)
	}
	if r := checkGnomeShellEval(brokenBus, compositor.GNOME); r.Available || r.Reason == "" {
		t.Fatalf("unavailable GNOME %+v", r)
	}
}

func TestOpenRuntime_NoSessionReturnsError(t *testing.T) {
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	if _, err := OpenRuntime(rt); err == nil {
		t.Fatal("OpenRuntime succeeded unexpectedly without a reachable backend")
	} else if !strings.Contains(err.Error(), "unsupported Wayland compositor") {
		t.Fatalf("OpenRuntime error = %v, want unsupported-session error", err)
	}
}

func TestOpenRuntimeFallsBackToX11WhenWaylandSocketUnresolvable(t *testing.T) {
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	mgr, err := OpenRuntime(rt)
	if err != nil {
		if !strings.Contains(err.Error(), `window/x11: connect to display ":99"`) {
			t.Fatalf("OpenRuntime error = %v, want X11 connection error", err)
		}
		return
	}
	if _, ok := mgr.(*X11Backend); !ok {
		t.Fatalf("OpenRuntime type = %T, want *X11Backend", mgr)
	}
}

func TestProbeRuntime_NoSessionReportsBackends(t *testing.T) {
	rt := env.FromEnviron([]string{})

	got := ProbeRuntime(rt)
	if len(got) != 4 {
		t.Fatalf("ProbeRuntime len = %d, want 4", len(got))
	}
	wantNames := []string{"kwin-scripting", "gnome-native", "gnome-shell-eval", "foreign-toplevel"}
	for i, name := range wantNames {
		if got[i].Name != name {
			t.Fatalf("ProbeRuntime[%d].Name = %q, want %q", i, got[i].Name, name)
		}
		if got[i].Available || got[i].Selected {
			t.Fatalf("ProbeRuntime[%d] available=%v selected=%v, want false/false", i, got[i].Available, got[i].Selected)
		}
		if got[i].Reason == "" {
			t.Fatalf("ProbeRuntime[%d] missing reason", i)
		}
	}
}

func TestOpenRuntimeContextX11ConnectionFailureReturnsNilManager(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=invalid-display-name"})
	manager, err := OpenRuntimeContext(context.Background(), rt)
	if err == nil || !strings.Contains(err.Error(), "window/x11: connect") {
		t.Fatalf("missing X11 connection failure: %v", err)
	}
	if manager != nil {
		t.Fatalf("failed X11 constructor leaked typed-nil manager: %T", manager)
	}
}

func TestSupportedOperationsIncludeSessionOperations(t *testing.T) {
	operations := (&X11Backend{}).SupportedOperations()
	for _, want := range []string{"info", "active-title"} {
		found := false
		for _, operation := range operations {
			if operation == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("SupportedOperations() = %v, missing %s", operations, want)
		}
	}
}

func TestPublicContextlessConstructorsValidateMissingEndpoints(t *testing.T) {
	constructors := []struct {
		name string
		open func() error
	}{
		{
			name: "GNOME native",
			open: func() error {
				_, err := NewGnomeNativeManagerForRuntime(env.FromEnviron(nil))
				return err
			},
		},
		{
			name: "KWin scripting",
			open: func() error {
				_, err := NewKWinScriptManagerForBus("")
				return err
			},
		},
		{
			name: "Sway IPC",
			open: func() error {
				_, err := NewSwayManagerRuntime(env.FromEnviron(nil))
				return err
			},
		},
	}
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			if err := constructor.open(); err == nil {
				t.Fatal("constructor succeeded without its required endpoint")
			}
		})
	}
}

func TestWindowOpenRuntimeContextFailsClosedOnUnavailableSyntheticBackends(t *testing.T) {
	for _, tc := range []struct{ name, desktop, want string }{
		{"kde", "KDE", "KWin scripting unavailable"},
		{"wlroots", "sway", "no window manager"},
		{"unknown", "unrecognised", "unsupported Wayland compositor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars := []string{"WAYLAND_DISPLAY=missing-perfuncted-test", "XDG_RUNTIME_DIR=" + t.TempDir(), "XDG_CURRENT_DESKTOP=" + tc.desktop, "DBUS_SESSION_BUS_ADDRESS=unix:path=" + t.TempDir() + "/missing-bus"}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			manager, err := OpenRuntimeContext(ctx, env.FromEnviron(vars))
			if manager != nil {
				defer manager.Close()
				t.Fatalf("unreachable compositor accepted: %T", manager)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %s", err, tc.want)
			}
		})
	}
}
func TestWindowRuntimeConstructorRespectsCancellationAndAbsentDisplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	manager, err := OpenRuntimeContext(ctx, env.FromEnviron([]string{"WAYLAND_DISPLAY=missing"}))
	if manager != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled manager: %T %v", manager, err)
	}
	manager, err = OpenRuntime(env.FromEnviron(nil))
	if manager != nil || err == nil || !strings.Contains(err.Error(), "no display") {
		t.Fatalf("headless manager: %T %v", manager, err)
	}
}

func TestNoOpSyncHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		sync func(context.Context) error
	}{
		{name: "sway", sync: (&SwayManager{}).Sync},
		{name: "gnome", sync: (&GnomeManager{}).Sync},
		{name: "kwin", sync: (&KWinScriptManager{}).Sync},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.sync(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("Sync error = %v, want context.Canceled", err)
			}
		})
	}
}

// Cancellation can arrive exactly as the waiter subscribes after confirming
// that its target is still present. The wait must return immediately, without
// waiting for another polling tick or mistaking cancellation for closure.
type cancelAtWindowWaitContext struct {
	context.Context //nolint:containedctx // Test context triggers cancellation at the subscription boundary.
	cancel          context.CancelFunc
	subscriptions   int
}

func (c *cancelAtWindowWaitContext) Done() <-chan struct{} {
	c.subscriptions++
	c.cancel()
	return c.Context.Done()
}

func TestWaitForMatchCloseCancelsWhenWaitSubscriptionBegins(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAtWindowWaitContext{Context: base, cancel: cancel}
	manager := &fakeManager{wins: []Info{{ID: 17, Title: "still-active-editor"}}}
	start := time.Now()
	err := WaitForMatchClose(ctx, manager, Match{TitleContains: "active-editor"}, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting for still-present target did not report cancellation: %v", err)
	}
	if ctx.subscriptions != 1 {
		t.Fatalf("wait subscribed %d times, want one", ctx.subscriptions)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("cancellation waited for polling instead of returning immediately: %s", elapsed)
	}
}

type canceledErrorWindowIterator struct {
	*fakeManager
	cancel  context.CancelFunc
	emitted int
}

func (m *canceledErrorWindowIterator) IterateWindows(context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		m.cancel()
		m.emitted++
		yield(Info{}, errors.New("window backend disconnected after caller cancellation"))
	}
}

func TestWaitForMatchClosePrefersCancellationAfterIteratorError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &canceledErrorWindowIterator{fakeManager: &fakeManager{}, cancel: cancel}
	err := WaitForMatchClose(ctx, m, Match{TitleContains: "Firefox"}, time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("returned backend error instead of caller cancellation: %v", err)
	}
	if m.emitted != 1 {
		t.Fatalf("iterated %d times, expected one", m.emitted)
	}
}

func TestWaitForMatchClosePreservesBackendErrorWithoutCancellation(t *testing.T) {
	cause := errors.New("real iterator error")
	m := &windowErrorIterator{fakeManager: &fakeManager{}, cause: cause}
	err := WaitForMatchClose(context.Background(), m, Match{TitleContains: "Firefox"}, time.Millisecond)
	if !errors.Is(err, cause) {
		t.Fatalf("backend failure=%v, want %v", err, cause)
	}
}

type windowErrorIterator struct {
	*fakeManager
	cause error
}

func (m *windowErrorIterator) IterateWindows(context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) { yield(Info{}, m.cause) }
}

type sequencedCloseWindowManager struct {
	*fakeManager
	calls    int
	sequence [][]Info
}

func (m *sequencedCloseWindowManager) IterateWindows(context.Context) iter.Seq2[Info, error] {
	m.calls++
	i := m.calls - 1
	if i >= len(m.sequence) {
		i = len(m.sequence) - 1
	}
	return func(yield func(Info, error) bool) {
		for _, w := range m.sequence[i] {
			if !yield(w, nil) {
				return
			}
		}
	}
}

func TestWaitForMatchClosePollsUntilTargetActuallyDisappears(t *testing.T) {
	m := &sequencedCloseWindowManager{fakeManager: &fakeManager{}, sequence: [][]Info{
		{{ID: 51, Title: "target app"}, {ID: 52, Title: "other"}},
		{{ID: 52, Title: "other"}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := WaitForMatchClose(ctx, m, Match{TitleContains: "target"}, time.Millisecond); err != nil {
		t.Fatalf("wait returned before disappearance: %v", err)
	}
	if m.calls != 2 {
		t.Fatalf("expected one positive probe and one negative probe, got %d", m.calls)
	}
}

func TestWaitForMatchCloseReportsDeadlineWhileMatchingWindowPersists(t *testing.T) {
	m := &sequencedCloseWindowManager{fakeManager: &fakeManager{}, sequence: [][]Info{{{ID: 53, Title: "target app"}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := WaitForMatchClose(ctx, m, Match{TitleContains: "target"}, time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("persistent target acknowledged closed, err=%v", err)
	}
	if m.calls < 2 {
		t.Fatalf("persistent target was not rechecked: calls=%d", m.calls)
	}
}

func TestWaitForMatchCloseDoesNotConfuseAnotherWindowWithTarget(t *testing.T) {
	m := &sequencedCloseWindowManager{fakeManager: &fakeManager{}, sequence: [][]Info{{{ID: 72, Title: "unrelated app"}}}}
	if err := WaitForMatchClose(context.Background(), m, Match{TitleContains: "target"}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if m.calls != 1 {
		t.Fatalf("unrelated window kept polling after match absence: calls=%d", m.calls)
	}
}
