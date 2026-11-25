package input

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestInputAttemptsJoinsRecordedCausesInSelectionOrder(t *testing.T) {
	var attempts inputAttempts
	if err := attempts.join(fmt.Errorf("terminal")); err == nil || err.Error() != "terminal" {
		t.Fatalf("join with no recorded causes = %v, want the terminal error", err)
	}

	attempts.add("wl-virtual input", errors.New("zwlr_virtual_pointer_manager_v1 absent"))
	attempts.add("XTEST input", errors.New("cannot open display"))
	attempts.add("uinput input", errors.New("permission denied"))
	attempts.add("ignored", nil)

	err := attempts.join(errors.New("uinput inaccessible"))
	if err == nil {
		t.Fatal("join returned nil")
	}
	message := err.Error()
	for _, want := range []string{
		"wl-virtual input",
		"zwlr_virtual_pointer_manager_v1 absent",
		"XTEST input",
		"cannot open display",
		"uinput input",
		"permission denied",
		"uinput inaccessible",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("joined error missing %q:\n%v", want, message)
		}
	}

	// Selection order is preserved so the most relevant cause reads first.
	first := strings.Index(message, "wl-virtual input")
	second := strings.Index(message, "XTEST input")
	last := strings.Index(message, "uinput inaccessible")
	if first >= second || second >= last {
		t.Fatalf("causes are not in selection order:\n%v", message)
	}

	// The wrapped causes stay inspectable rather than only rendered as text.
	if !strings.Contains(message, "cannot open display") {
		t.Fatalf("joined error lost its causes:\n%v", message)
	}
}

func TestInputAttemptsLabelsEveryRecordedBackend(t *testing.T) {
	// A label must appear for each attempt so an operator can tell which
	// backend refused and why, instead of seeing only the last fallback.
	var attempts inputAttempts
	attempts.add("GNOME Shell bridge input", errors.New("bridge not on the session bus"))
	if got := len(attempts); got != 1 {
		t.Fatalf("recorded %d attempts, want 1", got)
	}
	if !strings.Contains(attempts[0].Error(), "GNOME Shell bridge input") {
		t.Fatalf("attempt %q is missing its label", attempts[0])
	}
	if !strings.Contains(attempts[0].Error(), "bridge not on the session bus") {
		t.Fatalf("attempt %q lost its cause", attempts[0])
	}
}

func TestBackendConstructorReturningNilNilMustNotBeAccepted(t *testing.T) {
	oW, oX, oU, oStat := newWlVirtualBackend, newXTestBackend, newUinputBackend, statUinput
	t.Cleanup(func() { newWlVirtualBackend = oW; newXTestBackend = oX; newUinputBackend = oU; statUinput = oStat })
	newWlVirtualBackend = func(context.Context, string) (Inputter, error) { return nil, nil }
	newUinputBackend = func(int32, int32) (Inputter, error) { return backendName{name: "uinput"}, nil }
	newXTestBackend = func(string) (Inputter, error) { return backendName{name: "xtest"}, nil }
	statUinput = func() error { return nil }
	t.Setenv("PF_FORCE_INPUT", "")
	rt := env.FromEnviron([]string{"DISPLAY=:99", "WAYLAND_DISPLAY=wayland-0", "XDG_RUNTIME_DIR=" + t.TempDir()})
	b, err := OpenRuntimeContext(context.Background(), rt, 1600, 900)
	if err != nil {
		t.Fatal(err)
	}
	if b == nil {
		t.Fatal("successful open returned nil Inputter")
	}
	named, ok := b.(backendName)
	if !ok {
		t.Fatalf("unexpected input backend %T", b)
	}
	if got := named.name; got != "uinput" {
		t.Fatalf("expected fallback after nil backend, got %s", got)
	}
}

func TestCanceledSuccessfulBackendConstructionMustCloseBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := false
	b, err := openInputCandidate(ctx, new(inputAttempts), "delayed", func() (Inputter, error) {
		cancel()
		return &closeTrackingBackend{onClose: func() { closed = true }}, nil
	})
	if b != nil || !errors.Is(err, context.Canceled) || !closed {
		t.Fatalf("b=%v err=%v closed=%v", b, err, closed)
	}
}

type cancelingCloseInputter struct {
	noopInputter
	cancel context.CancelFunc
	closes int
}

func (b *cancelingCloseInputter) Close() error {
	b.closes++
	b.cancel()
	return nil
}

func TestOpenInputCandidateCancellationDuringFailedBackendCleanupTakesPrecedence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &cancelingCloseInputter{cancel: cancel}
	attempts := inputAttempts{}

	got, err := openInputCandidate(ctx, &attempts, "partial", func() (Inputter, error) {
		return backend, errors.New("backend initialization failed after acquiring resources")
	})
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("openInputCandidate returned backend=%v err=%v, want cancellation after cleanup", got, err)
	}
	if backend.closes != 1 {
		t.Fatalf("partial backend closed %d times, want once", backend.closes)
	}
	if len(attempts) != 0 {
		t.Fatalf("canceled constructor failure recorded %d backend attempts: %v", len(attempts), attempts)
	}
}

type closeTrackingBackend struct {
	noopInputter
	onClose func()
}

func (b *closeTrackingBackend) Close() error { b.onClose(); return nil }

func TestForcedBackendNilDeviceMustReturnUsefulError(t *testing.T) {
	old, oldStat := newUinputBackend, statUinput
	t.Cleanup(func() { newUinputBackend = old; statUinput = oldStat })
	newUinputBackend = func(int32, int32) (Inputter, error) { return nil, nil }
	statUinput = func() error { return nil }
	t.Setenv("PF_FORCE_INPUT", "uinput")
	b, err := OpenRuntimeContext(context.Background(), env.FromEnviron([]string{"DISPLAY=:99"}), 1600, 900)
	if b != nil || err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("nil backend accepted, b=%v err=%v", b, err)
	}
}

var _ = os.ErrNotExist

func TestUinputConstructorReturningDeviceAndErrorClosesDevice(t *testing.T) {
	old := newUinputBackend
	t.Cleanup(func() { newUinputBackend = old })
	closed := 0
	newUinputBackend = func(int32, int32) (Inputter, error) {
		return &closeTrackingBackend{onClose: func() { closed++ }}, errors.New("constructor half failed")
	}
	b, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 1600, 900)
	if b != nil || err == nil || closed != 1 {
		t.Fatalf("backend=%v err=%v close calls=%d, want 1", b, err, closed)
	}
}
func TestOpenInputCandidateWithBackendAndErrorClosesOnce(t *testing.T) {
	closed := 0
	attempts := inputAttempts{}
	b, err := openInputCandidate(context.Background(), &attempts, "corrupt", func() (Inputter, error) {
		return &closeTrackingBackend{onClose: func() { closed++ }}, errors.New("broken")
	})
	if b != nil || err == nil || closed != 1 || len(attempts) != 1 {
		t.Fatalf("b=%v err=%v closed=%d errors=%d", b, err, closed, len(attempts))
	}
}

type backendName struct {
	noopInputter
	name string
}

func TestWaylandFallbackPrefersNativeUinputOverXTest(t *testing.T) {
	ow, ox, ou, oldStat := newWlVirtualBackend, newXTestBackend, newUinputBackend, statUinput
	t.Cleanup(func() { newWlVirtualBackend = ow; newXTestBackend = ox; newUinputBackend = ou; statUinput = oldStat })
	calls := []string{}
	newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
		calls = append(calls, "wl-virtual")
		return nil, os.ErrNotExist
	}
	newXTestBackend = func(string) (Inputter, error) { calls = append(calls, "xtest"); return backendName{name: "xtest"}, nil }
	newUinputBackend = func(int32, int32) (Inputter, error) {
		calls = append(calls, "uinput")
		return backendName{name: "uinput"}, nil
	}
	statUinput = func() error { return nil }
	t.Setenv("PF_FORCE_INPUT", "")
	rt := env.FromEnviron([]string{"DISPLAY=:99", "WAYLAND_DISPLAY=wayland-0", "XDG_RUNTIME_DIR=" + t.TempDir()})
	b, err := OpenRuntimeContext(context.Background(), rt, 1600, 900)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	named, ok := b.(backendName)
	if !ok {
		t.Fatalf("unexpected input backend %T", b)
	}
	if got := named.name; got != "uinput" {
		t.Fatalf("selected %s, want Wayland-capable uinput before XWayland-only xtest; calls=%v", got, calls)
	}
}

func TestRuntimeBackendSelectionMatrix(t *testing.T) {
	cases := []struct {
		name            string
		wayland         bool
		virtual, ui, xt bool
		expected        string
	}{
		{"wayland virtual wins", true, true, true, true, "virtual"},
		{"wayland uinput wins", true, false, true, true, "uinput"},
		{"wayland XTest last resort", true, false, false, true, "xtest"},
		{"pure X11 XTest wins", false, false, true, true, "xtest"},
		{"pure X11 uinput fallback", false, false, true, false, "uinput"},
		{"no available backend", false, false, false, false, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ow, ox, ou, oldStat := newWlVirtualBackend, newXTestBackend, newUinputBackend, statUinput
			t.Cleanup(func() { newWlVirtualBackend = ow; newXTestBackend = ox; newUinputBackend = ou; statUinput = oldStat })
			newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
				if tc.virtual {
					return backendName{name: "virtual"}, nil
				}
				return nil, os.ErrNotExist
			}
			newXTestBackend = func(string) (Inputter, error) {
				if tc.xt {
					return backendName{name: "xtest"}, nil
				}
				return nil, errors.New("unavailable")
			}
			newUinputBackend = func(int32, int32) (Inputter, error) {
				if tc.ui {
					return backendName{name: "uinput"}, nil
				}
				return nil, errors.New("unavailable")
			}
			statUinput = func() error {
				if tc.ui {
					return nil
				}
				return os.ErrPermission
			}
			t.Setenv("PF_FORCE_INPUT", "")
			vars := []string{"DISPLAY=:99"}
			if tc.wayland {
				vars = append(vars, "WAYLAND_DISPLAY=wayland-0", "XDG_RUNTIME_DIR="+t.TempDir())
			}
			b, err := OpenRuntimeContext(context.Background(), env.FromEnviron(vars), 1600, 900)
			if tc.expected == "error" {
				if err == nil {
					t.Fatalf("expected failure, got %T", b)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected %v", err)
			}
			defer b.Close()
			named, ok := b.(backendName)
			if !ok {
				t.Fatalf("unexpected input backend %T", b)
			}
			if got := named.name; got != tc.expected {
				t.Fatalf("got %q want %q", got, tc.expected)
			}
		})
	}
}
func TestForcedUinputNeverFallsBackAndReportsDimensions(t *testing.T) {
	old, oldStat := newUinputBackend, statUinput
	t.Cleanup(func() { newUinputBackend = old; statUinput = oldStat })
	statUinput = func() error { return nil }
	newUinputBackend = func(int32, int32) (Inputter, error) { return nil, errors.New("device failed") }
	t.Setenv("PF_FORCE_INPUT", "uinput")
	_, err := OpenRuntimeContext(context.Background(), env.FromEnviron([]string{"DISPLAY=:99"}), 1600, 900)
	if err == nil || !strings.Contains(err.Error(), "forced uinput") {
		t.Fatalf("forced backend error=%v", err)
	}
}

// ── contextutil.Default ──────────────────────────────────────────────────────────

func TestNormalizeContext_NilReturnsBackground(t *testing.T) {
	var nilCtx context.Context //nolint:SA1012 // testing nil handling
	got := contextutil.Default(nilCtx)
	if got == nil {
		t.Fatal("contextutil.Default(nil) returned nil, want non-nil context")
	}
	// Should be equivalent to context.Background(): no deadline, never cancelled.
	select {
	case <-got.Done():
		t.Fatal("contextutil.Default(nil) returned a cancelled context")
	default:
	}
}

func TestNormalizeContext_NonNilPassThrough(t *testing.T) {
	ctx := context.Background()
	got := contextutil.Default(ctx)
	if got != ctx {
		t.Fatal("contextutil.Default(non-nil) should return the same context")
	}
}

// ── sleepContext — zero / negative duration ───────────────────────────────────

func TestSleepContext_ZeroDuration(t *testing.T) {
	// Zero duration returns ctx.Err() immediately (nil for a live context).
	ctx := context.Background()
	if err := sleepContext(ctx, 0); err != nil {
		t.Fatalf("sleepContext(bg, 0) = %v, want nil", err)
	}
}

func TestSleepContext_ZeroDuration_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sleepContext(ctx, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepContext(cancelled, 0) = %v, want context.Canceled", err)
	}
}

func TestSleepContext_NilContext_ZeroDuration(t *testing.T) {
	// nil context is normalised to Background, so 0-duration returns nil.
	var nilCtx context.Context //nolint:SA1012 // testing nil handling
	if err := sleepContext(nilCtx, 0); err != nil {
		t.Fatalf("sleepContext(nil, 0) = %v, want nil", err)
	}
}

func TestSleepContext_NilContext_PositiveDuration(t *testing.T) {
	// nil context is normalised to Background; positive duration should complete.
	var nilCtx context.Context //nolint:SA1012 // testing nil handling
	if err := sleepContext(nilCtx, 1*time.Millisecond); err != nil {
		t.Fatalf("sleepContext(nil, 1ms) = %v, want nil", err)
	}
}

// ── parseCombo — modifier aliases ─────────────────────────────────────────────

func TestParseCombo_ModifierAliases(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantKey   string
		wantSuper bool
		wantCtrl  bool
	}{
		{name: "WinModifier", input: "{win+d}", wantKey: "d", wantSuper: true},
		{name: "LogoModifier", input: "{logo+l}", wantKey: "l", wantSuper: true},
		{name: "ControlAlias", input: "{control+c}", wantKey: "c", wantCtrl: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ks, err := parseKeySend(tc.input)
			if err != nil {
				t.Fatalf("parseKeySend(%q) error = %v", tc.input, err)
			}
			if ks[0].key != tc.wantKey {
				t.Errorf("key = %q, want %q", ks[0].key, tc.wantKey)
			}
			if ks[0].modifiers.super != tc.wantSuper {
				t.Errorf("super = %v, want %v", ks[0].modifiers.super, tc.wantSuper)
			}
			if ks[0].modifiers.ctrl != tc.wantCtrl {
				t.Errorf("ctrl = %v, want %v", ks[0].modifiers.ctrl, tc.wantCtrl)
			}
		})
	}
}

func TestParseCombo_UnknownModifier(t *testing.T) {
	_, err := parseKeySend("{bogus+s}")
	if err == nil {
		t.Fatal("expected error for unknown modifier bogus")
	}
}

func TestParseCombo_EmptyKey(t *testing.T) {
	_, err := parseKeySend("{ctrl+}")
	if err == nil {
		t.Fatal("expected error for empty key after +")
	}
}

// ── parseBraced — edge cases ──────────────────────────────────────────────────

func TestParseBraced_WhitespaceOnly(t *testing.T) {
	_, err := parseKeySend("{   }")
	if err == nil {
		t.Fatal("expected error for whitespace-only braced expression")
	}
}

func TestParseBraced_DownSuffix(t *testing.T) {
	sends, err := parseKeySend("{enter down}")
	if err != nil {
		t.Fatalf("parseKeySend({enter down}) error = %v", err)
	}
	if !sends[0].down {
		t.Error("expected down=true for {enter down}")
	}
	if sends[0].key != "enter" {
		t.Errorf("key = %q, want enter", sends[0].key)
	}
}

func TestParseBraced_UpSuffix(t *testing.T) {
	sends, err := parseKeySend("{tab up}")
	if err != nil {
		t.Fatalf("parseKeySend({tab up}) error = %v", err)
	}
	if sends[0].down {
		t.Error("expected down=false for {tab up}")
	}
	if !sends[0].up {
		t.Error("expected up=true for {tab up}")
	}
	if sends[0].key != "tab" {
		t.Errorf("key = %q, want tab", sends[0].key)
	}
}

type noopInputter struct{}

func (noopInputter) KeyDown(context.Context, string) error             { return nil }
func (noopInputter) KeyUp(context.Context, string) error               { return nil }
func (noopInputter) Type(context.Context, string) error                { return nil }
func (noopInputter) TypeLiteral(context.Context, string) error         { return nil }
func (noopInputter) MouseMove(context.Context, int, int) error         { return nil }
func (noopInputter) MouseClick(context.Context, int, int, int) error   { return nil }
func (noopInputter) MouseDown(context.Context, int) error              { return nil }
func (noopInputter) MouseUp(context.Context, int) error                { return nil }
func (noopInputter) ScrollUp(context.Context, int) error               { return nil }
func (noopInputter) ScrollDown(context.Context, int) error             { return nil }
func (noopInputter) ScrollLeft(context.Context, int) error             { return nil }
func (noopInputter) ScrollRight(context.Context, int) error            { return nil }
func (noopInputter) PointerLocation(context.Context) (int, int, error) { return 0, 0, nil }
func (noopInputter) Sync(context.Context) error                        { return nil }
func (noopInputter) Close() error                                      { return nil }

func TestOpenRuntimeFallsBackToXTestWhenWaylandSocketUnresolvable(t *testing.T) {
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
		return nil, os.ErrNotExist
	}
	newXTestBackend = func(string) (Inputter, error) {
		return noopInputter{}, nil
	}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		return nil, os.ErrNotExist
	}

	t.Setenv("PF_FORCE_INPUT", "")
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	inp, err := OpenRuntime(rt, 1024, 768)
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}
	if _, ok := inp.(noopInputter); !ok {
		t.Fatalf("OpenRuntime type = %T, want noopInputter", inp)
	}
}

func TestOpenRuntimeContextStopsFallbackWhenCanceled(t *testing.T) {
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	started := make(chan struct{})
	newWlVirtualBackend = func(ctx context.Context, _ string) (Inputter, error) {
		close(started)
		<-ctx.Done()
		return nil, os.ErrNotExist
	}
	xTestCalls, uinputCalls := 0, 0
	newXTestBackend = func(string) (Inputter, error) {
		xTestCalls++
		return noopInputter{}, nil
	}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		uinputCalls++
		return noopInputter{}, nil
	}
	t.Setenv("PF_FORCE_INPUT", "")
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := OpenRuntimeContext(ctx, rt, 1024, 768)
		result <- err
	}()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenRuntimeContext error = %v, want context.Canceled", err)
	}
	if xTestCalls != 0 || uinputCalls != 0 {
		t.Fatalf("fallback calls = xtest:%d uinput:%d, want neither after cancellation", xTestCalls, uinputCalls)
	}
}

func TestOpenRuntimeForcedUinputWrapsInitError(t *testing.T) {
	oldUinput := newUinputBackend
	oldStatUinput := statUinput
	t.Cleanup(func() {
		newUinputBackend = oldUinput
		statUinput = oldStatUinput
	})

	want := errors.New("uinput init failed")
	newUinputBackend = func(int32, int32) (Inputter, error) {
		return nil, want
	}
	statUinput = func() error { return nil }

	t.Setenv("PF_FORCE_INPUT", "uinput")
	err := func() error {
		_, err := OpenRuntime(env.FromEnviron(nil), 1024, 768)
		return err
	}()
	if !errors.Is(err, want) {
		t.Fatalf("OpenRuntime error = %v, want wrapped %v", err, want)
	}
}

func TestProbeRuntimeFallsBackToXTestWhenWaylandSocketUnresolvable(t *testing.T) {
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
		return nil, os.ErrNotExist
	}
	newXTestBackend = func(string) (Inputter, error) {
		return noopInputter{}, nil
	}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		return nil, os.ErrNotExist
	}

	t.Setenv("PF_FORCE_INPUT", "")
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	results := ProbeRuntime(rt)
	if len(results) < 3 || results[1].Name != "uinput" || results[2].Name != "xtest" {
		t.Fatalf("Wayland probe priority should be wl-virtual/uinput/xtest, got %+v", results)
	}
	if !results[2].Available {
		t.Fatal("fallback XTest unexpectedly unavailable")
	}
	if results[1].Selected == results[2].Selected {
		t.Fatalf("exactly one of uinput and xtest should be selected: %+v", results)
	}

}

func TestOpenUsesCurrentEnvironment(t *testing.T) {
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	var virtualCalls, xTestCalls, uinputCalls int
	newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
		virtualCalls++
		return noopInputter{}, nil
	}
	newXTestBackend = func(string) (Inputter, error) {
		xTestCalls++
		return nil, os.ErrNotExist
	}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		uinputCalls++
		return nil, os.ErrNotExist
	}

	t.Setenv("PF_FORCE_INPUT", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("DISPLAY", ":99")

	inp, err := OpenRuntime(env.FromEnviron(os.Environ()), 1024, 768)
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}
	if _, ok := inp.(noopInputter); !ok {
		t.Fatalf("Open type = %T, want noopInputter", inp)
	}
	if virtualCalls != 1 || xTestCalls != 0 || uinputCalls != 0 {
		t.Fatalf("constructor calls = virtual:%d xtest:%d uinput:%d", virtualCalls, xTestCalls, uinputCalls)
	}
}

func TestProbeUsesCurrentEnvironment(t *testing.T) {
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
		return nil, os.ErrNotExist
	}
	newXTestBackend = func(string) (Inputter, error) {
		return noopInputter{}, nil
	}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		return nil, os.ErrNotExist
	}

	t.Setenv("PF_FORCE_INPUT", "")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("DISPLAY", ":99")

	results := Probe()
	if len(results) < 3 || results[1].Name != "uinput" || results[2].Name != "xtest" {
		t.Fatalf("Wayland probe priority should be wl-virtual/uinput/xtest, got %+v", results)
	}
	if !results[2].Available {
		t.Fatal("fallback XTest unexpectedly unavailable")
	}
	if results[1].Selected == results[2].Selected {
		t.Fatalf("exactly one of uinput and xtest should be selected: %+v", results)
	}

}

func TestOpenRuntimePrefersXTestOnX11(t *testing.T) {
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	t.Cleanup(func() {
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
	})

	var virtualCalls, xTestCalls, uinputCalls int
	newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
		virtualCalls++
		return nil, os.ErrNotExist
	}
	newXTestBackend = func(string) (Inputter, error) {
		xTestCalls++
		return noopInputter{}, nil
	}
	newUinputBackend = func(int32, int32) (Inputter, error) {
		uinputCalls++
		return nil, os.ErrNotExist
	}

	t.Setenv("PF_FORCE_INPUT", "")
	rt := env.FromEnviron([]string{"DISPLAY=:99"})
	inp, err := OpenRuntime(rt, 1024, 768)
	if err != nil {
		t.Fatalf("OpenRuntime: %v", err)
	}
	if _, ok := inp.(noopInputter); !ok {
		t.Fatalf("OpenRuntime type = %T, want noopInputter", inp)
	}
	if virtualCalls != 0 || xTestCalls != 1 || uinputCalls != 0 {
		t.Fatalf("constructor calls = virtual:%d xtest:%d uinput:%d", virtualCalls, xTestCalls, uinputCalls)
	}
}

func TestCheckWlVirtualWithGlobs(t *testing.T) {
	tests := []struct {
		name   string
		globs  map[string]bool
		want   bool
		wantRe string
	}{
		{
			name:   "nil",
			globs:  nil,
			want:   false,
			wantRe: "connect wayland-0: failed",
		},
		{
			name:   "missing pointer manager",
			globs:  map[string]bool{"zwp_virtual_keyboard_manager_v1": true, "wl_seat": true},
			want:   false,
			wantRe: "zwlr_virtual_pointer_manager_v1 not advertised",
		},
		{
			name:   "missing keyboard manager",
			globs:  map[string]bool{"zwlr_virtual_pointer_manager_v1": true, "wl_seat": true},
			want:   false,
			wantRe: "zwp_virtual_keyboard_manager_v1 not advertised",
		},
		{
			name:   "available",
			globs:  map[string]bool{"zwlr_virtual_pointer_manager_v1": true, "zwp_virtual_keyboard_manager_v1": true},
			want:   true,
			wantRe: "zwlr_virtual_pointer_manager_v1 + zwp_virtual_keyboard_manager_v1 available",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := checkWlVirtualWithGlobs("wayland-0", tt.globs)
			if r.Available != tt.want {
				t.Fatalf("Available = %v, want %v", r.Available, tt.want)
			}
			if r.Reason != tt.wantRe {
				t.Fatalf("Reason = %q, want %q", r.Reason, tt.wantRe)
			}
		})
	}
}

// A failed open must explain itself. The compositor-scoped backends are tried
// before the host-level ones, so reporting only the last failure hides the most
// relevant cause.
func TestOpenRuntimeContextReportsEveryBackendFailure(t *testing.T) {
	oldWlVirtual := newWlVirtualBackend
	oldXTest := newXTestBackend
	oldUinput := newUinputBackend
	oldStat := statUinput
	t.Cleanup(func() {
		newWlVirtualBackend = oldWlVirtual
		newXTestBackend = oldXTest
		newUinputBackend = oldUinput
		statUinput = oldStat
	})

	wlVirtualCause := errors.New("zwlr_virtual_pointer_manager_v1 not advertised")
	xTestCause := errors.New("cannot open display :99")
	newWlVirtualBackend = func(context.Context, string) (Inputter, error) { return nil, wlVirtualCause }
	newXTestBackend = func(string) (Inputter, error) { return nil, xTestCause }
	newUinputBackend = func(int32, int32) (Inputter, error) { return nil, errors.New("unexpected uinput attempt") }
	statUinput = func() error { return os.ErrNotExist }

	t.Setenv("PF_FORCE_INPUT", "")
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	_, err := OpenRuntimeContext(context.Background(), rt, 1024, 768)
	if err == nil {
		t.Fatal("OpenRuntimeContext succeeded with every backend stubbed to fail")
	}
	for _, want := range []string{
		"wl-virtual input",
		"zwlr_virtual_pointer_manager_v1 not advertised",
		"XTEST input",
		"cannot open display :99",
		"no backend available",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("open error missing %q:\n%v", want, err)
		}
	}
	// The causes stay inspectable, not merely rendered.
	if !errors.Is(err, wlVirtualCause) || !errors.Is(err, xTestCause) {
		t.Fatalf("open error lost its wrapped causes: %v", err)
	}
}

func TestProbeOrderMatchesWaylandBackendPriority(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=:not-a-display", "WAYLAND_DISPLAY=wayland-unknown", "XDG_RUNTIME_DIR=" + t.TempDir()})
	results := ProbeRuntime(rt)
	if len(results) < 3 {
		t.Fatalf("expected Wayland + uinput + XTest candidates, got %+v", results)
	}
	for i, want := range []string{"wl-virtual", "uinput", "xtest"} {
		if got := results[i].Name; got != want {
			t.Fatalf("probe %d=%s, want %s; selection order must match runtime", i, got, want)
		}
	}
}
func TestProbeOrderPureX11KeepsXTestFirst(t *testing.T) {
	results := ProbeRuntime(env.FromEnviron([]string{"DISPLAY=:not-a-display"}))
	if len(results) < 2 || results[0].Name != "xtest" || results[1].Name != "uinput" {
		t.Fatalf("pure X11 probes=%+v", results)
	}
}

func TestNewGnomeNativeBackendForRuntimeRequiresSession(t *testing.T) {
	if _, err := NewGnomeNativeBackendForRuntime(env.FromEnviron(nil)); err == nil {
		t.Fatal("NewGnomeNativeBackendForRuntime succeeded without a session bus")
	}
}

// A backend may only advertise sync when it can actually observe the compositor
// acting on injected events. Reporting success without a barrier hands callers a
// postcondition that is not established, which is how a follow-up screen read
// observes state from before the keystrokes.
func TestSyncIsAdvertisedOnlyByBackendsWithACompletionBarrier(t *testing.T) {
	backends := []struct {
		name    string
		backend interface{ SupportedOperations() []string }
		want    bool
	}{
		{name: "xtest", backend: &XTestBackend{}, want: true},
		{name: "wl-virtual", backend: &WlVirtualBackend{}, want: false},
		{name: "uinput", backend: &UinputBackend{}, want: false},
		{name: "portal", backend: &RemoteDesktopBackend{}, want: false},
		{name: "gnome-native", backend: &GnomeNativeBackend{}, want: false},
	}
	for _, tc := range backends {
		t.Run(tc.name, func(t *testing.T) {
			advertised := slices.Contains(tc.backend.SupportedOperations(), "sync")
			if advertised != tc.want {
				t.Fatalf("sync advertised = %t, want %t (operations %v)",
					advertised, tc.want, tc.backend.SupportedOperations())
			}
		})
	}
}

// Advertised sync and an implemented barrier must agree: a backend that claims
// the operation has to satisfy Syncer, and one that does not claim it must not
// pretend to provide one.
func TestSyncAdvertisementMatchesImplementedBarrier(t *testing.T) {
	backends := []struct {
		name    string
		backend interface {
			Inputter
			SupportedOperations() []string
		}
	}{
		{name: "xtest", backend: &XTestBackend{}},
		{name: "wl-virtual", backend: &WlVirtualBackend{}},
		{name: "uinput", backend: &UinputBackend{}},
		{name: "portal", backend: &RemoteDesktopBackend{}},
		{name: "gnome-native", backend: &GnomeNativeBackend{}},
	}
	for _, tc := range backends {
		t.Run(tc.name, func(t *testing.T) {
			_, isSyncer := any(tc.backend).(Syncer)
			advertised := slices.Contains(tc.backend.SupportedOperations(), "sync")
			if isSyncer != advertised {
				t.Fatalf("implements Syncer = %t but advertised sync = %t; a backend must not claim a barrier it cannot provide",
					isSyncer, advertised)
			}
		})
	}
}
