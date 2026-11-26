package window

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/compositor"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"iter"
	"math"
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

// A non-positive edge is the request that reaches a compositor as a nonsense
// size, so it must never be accepted.
func TestValidateSizeRejectsNonPositiveDimensions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
	}{
		{name: "zero width", width: 0, height: 100},
		{name: "zero height", width: 100, height: 0},
		{name: "negative width", width: -1, height: 100},
		{name: "negative height", width: 100, height: -100},
		{name: "both negative", width: -1, height: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSize(tc.width, tc.height)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ValidateSize(%d, %d) = %v, want ErrInvalidArgument", tc.width, tc.height, err)
			}
		})
	}
}

// Values beyond the 16-bit geometry space are truncated by the protocol rather
// than rejected, so they must be refused before dispatch. -1 is the case that
// turns into a 65535-pixel edge.
func TestValidateSizeRejectsUnrepresentableDimensions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
	}{
		{name: "negative width", width: -1, height: 100},
		{name: "negative height", width: 100, height: -1},
		{name: "width above 16 bits", width: MaxDimension + 1, height: 100},
		{name: "height above 16 bits", width: 100, height: MaxDimension + 1},
		{name: "int overflow width", width: math.MaxInt, height: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateSize(tc.width, tc.height); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ValidateSize(%d, %d) = %v, want ErrInvalidArgument", tc.width, tc.height, err)
			}
		})
	}
}

func TestValidateSizeAcceptsRepresentableDimensions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
	}{
		{name: "minimum", width: 1, height: 1},
		{name: "typical", width: 1280, height: 720},
		{name: "maximum", width: MaxDimension, height: MaxDimension},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateSize(tc.width, tc.height); err != nil {
				t.Fatalf("ValidateSize(%d, %d) = %v, want nil", tc.width, tc.height, err)
			}
		})
	}
}

// A monitor can sit left of or above the primary one, so negative positions are
// ordinary and must be accepted.
func TestValidatePositionAcceptsNegativeCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{name: "origin", x: 0, y: 0},
		{name: "left and above", x: -1920, y: -1080},
		{name: "large but representable", x: math.MaxInt32, y: math.MinInt32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePosition(tc.x, tc.y); err != nil {
				t.Fatalf("ValidatePosition(%d, %d) = %v, want nil", tc.x, tc.y, err)
			}
		})
	}
}

func TestValidatePositionRejectsOutOfRangeCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{name: "x above", x: math.MaxInt32 + 1, y: 0},
		{name: "x below", x: math.MinInt32 - 1, y: 0},
		{name: "y above", x: 0, y: math.MaxInt32 + 1},
		{name: "y below", x: 0, y: math.MinInt32 - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePosition(tc.x, tc.y); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ValidatePosition(%d, %d) = %v, want ErrInvalidArgument", tc.x, tc.y, err)
			}
		})
	}
}

func TestSignedNumericIDRejectsOverflow(t *testing.T) {
	if _, err := signedNumericID("9223372036854775808"); err == nil {
		t.Fatal("signedNumericID accepted a value above MaxInt64")
	}
}

func TestX11WindowIDRejectsOverflow(t *testing.T) {
	_, err := x11WindowID("4294967296")
	if err == nil || !strings.Contains(err.Error(), "X11 window id range") {
		t.Fatalf("x11WindowID error = %v, want X11 range error", err)
	}
}

func TestSwayIterateWindowsSkipsNegativeIDs(t *testing.T) {
	manager := &SwayManager{conn: newSuccessSwayConn([]byte(`{"id":1,"type":"root","nodes":[{"id":-1,"type":"con","name":"bad"},{"id":2,"type":"con","name":"good"}]}`))}
	var got []Info
	for info, err := range manager.IterateWindows(context.Background()) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, info)
	}
	if len(got) != 1 || got[0].Title != "good" {
		t.Fatalf("IterateWindows = %#v, want only good window", got)
	}
}

// Native IDs are serialized as decimal text. Leading zeroes do not mean octal:
// accepting base-0 parser semantics would route a request for ID 10 to ID 8.
func TestNumericNativeIDsUseDecimalNotOctal(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want uint64
	}{
		{"0", 0}, {"08", 8}, {"010", 10}, {"00042", 42},
		{"0x2a", 42}, {"0X2A", 42},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := numericID(tc.raw)
			if err != nil || got != tc.want {
				t.Fatalf("numericID(%q) = %d, %v; want %d", tc.raw, got, err, tc.want)
			}
		})
	}
}

func TestX11NativeIDsDoNotRetargetLeadingZeroDecimal(t *testing.T) {
	got, err := x11WindowID("010")
	if err != nil || uint64(got) != 10 {
		t.Fatalf("X11 handle leading-zero decimal = %d, %v; want 10", got, err)
	}
}
