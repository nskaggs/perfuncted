//go:build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"math"
	"slices"
	"strings"
	"testing"
)

// A one-shot D-Bus failure while releasing a tapped key must not leave the
// key logically pressed. A bounded best-effort release should run even when
// the operation failed, while preserving the original provider failure.
type failFirstGnomeKeyRelease struct {
	recordingGnomeBridge
	releaseFailed bool
	calls         []string
}

func (b *failFirstGnomeKeyRelease) Key(ctx context.Context, keyval uint32, down bool) error {
	b.calls = append(b.calls, fmt.Sprintf("%d:%t", keyval, down))
	if !down && !b.releaseFailed {
		b.releaseFailed = true
		return errors.New("synthetic GNOME key release failure")
	}
	return nil
}
func TestGnomeNativeTapRetriesTransientFailedReleaseWithoutHidingError(t *testing.T) {
	for _, seq := range []string{"{enter}", "{ctrl+a}"} {
		t.Run(seq, func(t *testing.T) {
			bridge := &failFirstGnomeKeyRelease{}
			b := &GnomeNativeBackend{bridge: bridge}
			err := b.Type(context.Background(), seq)
			if err == nil {
				t.Fatalf("failed key release acknowledged: events=%v", bridge.calls)
			}
			keyval := uint32(0xff0d)
			if seq == "{ctrl+a}" {
				keyval = uint32('a')
			}
			release := fmt.Sprintf("%d:false", keyval)
			count := 0
			for _, call := range bridge.calls {
				if call == release {
					count++
				}
			}
			if count != 2 {
				t.Fatalf("failed release not retried: release calls=%d wire=%v error=%v", count, bridge.calls, err)
			}
			if b.held.any() {
				t.Fatalf("transient failure stranded modifiers: %+v", b.held)
			}
		})
	}
}

// GNOME's D-Bus adapter may acknowledge a command just as the caller cancels.
// A successful late acknowledgment is not a successful caller operation.
type cancelingGnomeBridge struct {
	recordingGnomeBridge
	cancel context.CancelFunc
}

func (b *cancelingGnomeBridge) Key(ctx context.Context, keyval uint32, pressed bool) error {
	err := b.recordingGnomeBridge.Key(ctx, keyval, pressed)
	if b.cancel != nil {
		b.cancel()
	}
	return err
}
func (b *cancelingGnomeBridge) Text(context.Context, string) error {
	if b.cancel != nil {
		b.cancel()
	}
	return nil
}
func (b *cancelingGnomeBridge) PointerLocation(context.Context) (int, int, error) {
	if b.cancel != nil {
		b.cancel()
	}
	return 72, 83, nil
}
func TestGnomeNativeLateProviderSuccessCannotOutrunCallerCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(context.Context, *GnomeNativeBackend) error
	}{
		{"key-down", func(ctx context.Context, b *GnomeNativeBackend) error { return b.KeyDown(ctx, "a") }},
		{"literal-text", func(ctx context.Context, b *GnomeNativeBackend) error { return b.TypeLiteral(ctx, "abc") }},
		{"pointer-location", func(ctx context.Context, b *GnomeNativeBackend) error {
			_, _, err := b.PointerLocation(ctx)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bridge := &cancelingGnomeBridge{cancel: cancel}
			b := &GnomeNativeBackend{bridge: bridge}
			if err := tc.run(ctx, b); !errors.Is(err, context.Canceled) {
				t.Fatalf("late provider success after cancellation accepted: %v", err)
			}
		})
	}
}

type lifecycleGnomeBridge struct {
	recordingGnomeBridge
	log    []string
	failed error
	closed int
	x, y   int
}

func (b *lifecycleGnomeBridge) Text(_ context.Context, s string) error {
	b.log = append(b.log, "text:"+s)
	return b.failed
}
func (b *lifecycleGnomeBridge) Paste(_ context.Context, s string) error {
	b.log = append(b.log, "paste:"+s)
	return b.failed
}
func (b *lifecycleGnomeBridge) PointerMove(_ context.Context, x, y int32) error {
	b.log = append(b.log, fmt.Sprintf("move:%d,%d", x, y))
	return b.failed
}
func (b *lifecycleGnomeBridge) PointerButton(_ context.Context, button uint32, down bool) error {
	b.log = append(b.log, fmt.Sprintf("button:%d:%t", button, down))
	return b.failed
}
func (b *lifecycleGnomeBridge) PointerLocation(context.Context) (int, int, error) {
	return b.x, b.y, b.failed
}
func (b *lifecycleGnomeBridge) Close() error { b.closed++; return b.failed }

func TestGnomeNativeInputNormalLiteralPointerAndLifecyclePaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*GnomeNativeBackend) error
		want string
	}{
		{"ASCII text", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "ASCII 123") }, "text:ASCII 123"},
		{"Unicode clipboard fallback", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "café") }, "paste:café"},
		{"multiline control-safe text", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "a\nb") }, "text:a\nb"},
		{"key syntax literal braces", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "{ctrl+a}") }, "text:{ctrl+a}"},
		{"move", func(b *GnomeNativeBackend) error { return b.MouseMove(context.Background(), 12, 24) }, "move:12,24"},
		{"mouse down", func(b *GnomeNativeBackend) error { return b.MouseDown(context.Background(), 2) }, "button:2:true"},
		{"mouse up", func(b *GnomeNativeBackend) error { return b.MouseUp(context.Background(), 3) }, "button:3:false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &lifecycleGnomeBridge{}
			b := &GnomeNativeBackend{bridge: bridge}
			if err := tc.call(b); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(bridge.log, "|"); got != tc.want {
				t.Fatalf("incorrect GNOME bridge command %q want %q", got, tc.want)
			}
		})
	}
	bridge := &lifecycleGnomeBridge{x: 45, y: 89}
	b := &GnomeNativeBackend{bridge: bridge}
	x, y, err := b.PointerLocation(context.Background())
	if err != nil || x != 45 || y != 89 {
		t.Fatalf("pointer=(%d,%d) %v", x, y, err)
	}
	space, err := b.PointerCoordinateSpace(context.Background())
	if err != nil || space.Kind != CoordinateSpaceLogical || space.ScaleX != 1 || space.ScaleY != 1 {
		t.Fatalf("GNOME coordinates %+v %v", space, err)
	}
	if err := b.Close(); err != nil || bridge.closed != 1 {
		t.Fatalf("bridge close count=%d err=%v", bridge.closed, err)
	}
}
func TestGnomeNativeInputUnavailableAndProviderFailuresFailClosed(t *testing.T) {
	for _, b := range []*GnomeNativeBackend{nil, {}} {
		if err := b.TypeLiteral(context.Background(), "text"); err == nil {
			t.Fatal("uninitialized GNOME accepted input")
		}
		if err := b.MouseUp(context.Background(), 1); err == nil {
			t.Fatal("uninitialized GNOME accepted button")
		}
		if _, _, err := b.PointerLocation(context.Background()); err == nil {
			t.Fatal("uninitialized GNOME disclosed coordinates")
		}
		if _, err := b.PointerCoordinateSpace(context.Background()); err == nil {
			t.Fatal("uninitialized GNOME asserted pointer coordinate trust")
		}
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
	}
	failure := errors.New("synthetic GNOME bridge disconnected")
	for _, tc := range []struct {
		name string
		call func(*GnomeNativeBackend) error
	}{
		{"text", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "abc") }},
		{"paste", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "🎯") }},
		{"move", func(b *GnomeNativeBackend) error { return b.MouseMove(context.Background(), 4, 5) }},
		{"button down", func(b *GnomeNativeBackend) error { return b.MouseDown(context.Background(), 1) }},
		{"button up", func(b *GnomeNativeBackend) error { return b.MouseUp(context.Background(), 2) }},
		{"pointer", func(b *GnomeNativeBackend) error { _, _, err := b.PointerLocation(context.Background()); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &GnomeNativeBackend{bridge: &lifecycleGnomeBridge{failed: failure}}
			if err := tc.call(b); !errors.Is(err, failure) {
				t.Fatalf("backend error lost: %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &GnomeNativeBackend{bridge: &lifecycleGnomeBridge{}}
	if _, err := b.PointerCoordinateSpace(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled coordinate query: %v", err)
	}
	if err := b.TypeLiteral(ctx, "abc"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled typing: %v", err)
	}
}

func TestGnomeKeyval(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want uint32
	}{
		{key: "a", want: 'a'},
		{key: "A", want: 'a'},
		{key: "space", want: 0x20},
		{key: "return", want: 0xff0d},
		{key: "ctrl", want: 0xffe3},
		{key: "f12", want: 0xffc9},
		{key: "é", want: 0x00e9},
	} {
		t.Run(tc.key, func(t *testing.T) {
			got, err := gnomeKeyval(tc.key)
			if err != nil {
				t.Fatalf("gnomeKeyval(%q): %v", tc.key, err)
			}
			if got != tc.want {
				t.Fatalf("gnomeKeyval(%q) = %#x, want %#x", tc.key, got, tc.want)
			}
		})
	}
	if _, err := gnomeKeyval("unknown-key"); err == nil {
		t.Fatal("gnomeKeyval(unknown-key) succeeded")
	}
}

func TestGnomeNativeBackendDoesNotAdvertiseSync(t *testing.T) {
	if slices.Contains((&GnomeNativeBackend{}).SupportedOperations(), "sync") {
		t.Fatal("GNOME native input must not advertise unsupported synchronization")
	}
}

func TestValidateGnomeCoordinatesRejectsValuesThatWouldTruncate(t *testing.T) {
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{name: "x above", x: int(int64(1 << 31)), y: 0},
		{name: "x below", x: int(-int64(1<<31) - 1), y: 0},
		{name: "y above", x: 0, y: int(int64(1 << 31))},
		{name: "y below", x: 0, y: int(-int64(1<<31) - 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateGnomeCoordinates(tc.x, tc.y); err == nil {
				t.Fatal("validateGnomeCoordinates accepted a value outside the int32 range")
			}
		})
	}
}

func TestGnomeDirectTextOnlyUsesLayoutSafeCharacters(t *testing.T) {
	for _, test := range []struct {
		text string
		want bool
	}{
		{text: "hello, world!\n", want: true},
		{text: "", want: true},
		{text: "é", want: false},
		{text: "\u0001", want: false},
	} {
		if got := gnomeDirectText(test.text); got != test.want {
			t.Fatalf("gnomeDirectText(%q) = %v, want %v", test.text, got, test.want)
		}
	}
}

func TestUpdateHeldModifierTracksExplicitModifierActions(t *testing.T) {
	var held modifiers
	for _, test := range []struct {
		key  string
		down bool
		want modifiers
	}{
		{key: "ctrl", down: true, want: modifiers{ctrl: true}},
		{key: "control", down: false, want: modifiers{}},
		{key: "meta", down: true, want: modifiers{super: true}},
		{key: "super", down: false, want: modifiers{}},
	} {
		updateHeldModifier(&held, test.key, test.down)
		if held != test.want {
			t.Fatalf("updateHeldModifier(%q, %v) = %#v, want %#v", test.key, test.down, held, test.want)
		}
	}
}

func TestReleaseMouseButtonUsesIndependentCleanupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var releaseCtxErr error
	err := releaseMouseButton(ctx, func(ctx context.Context) error {
		releaseCtxErr = ctx.Err()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("releaseMouseButton error = %v, want context.Canceled", err)
	}
	if releaseCtxErr != nil {
		t.Fatalf("release context error = %v, want nil during cleanup", releaseCtxErr)
	}
}

func TestReleaseMouseButtonJoinsReleaseError(t *testing.T) {
	releaseErr := errors.New("release failed")
	err := releaseMouseButton(context.Background(), func(context.Context) error {
		return releaseErr
	})
	if !errors.Is(err, releaseErr) {
		t.Fatalf("releaseMouseButton error = %v, want release error", err)
	}
}

// An unsuccessful GNOME constructor may already have acquired resources.
// Falling back, or requiring a session restart, must release those resources.
func TestOpenRuntimeClosesPartialGnomeBackendBeforeFailureOrFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cause    error
		fallback bool
	}{
		{"ordinary-bridge-error", errors.New("GNOME bridge failed"), true},
		{"session-restart-required", gnomebridge.ErrSessionRestartRequired, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetRuntimeSelectorConstructors(t)
			partial := &selectorTrackedInputter{}
			selected := &selectorTrackedInputter{}
			gnomeCalls, virtualCalls := 0, 0
			newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) { gnomeCalls++; return partial, tc.cause }
			newWlVirtualBackend = func(context.Context, string) (Inputter, error) { virtualCalls++; return selected, nil }
			got, err := OpenRuntimeContext(context.Background(), syntheticGNOMERuntime(t), 1024, 768)
			if partial.closed != 1 || gnomeCalls != 1 {
				t.Fatalf("failed GNOME backend leaked or closed twice: closes=%d calls=%d err=%v", partial.closed, gnomeCalls, err)
			}
			if tc.fallback {
				if err != nil || got != selected || virtualCalls != 1 {
					t.Fatalf("fallback missing after cleanup: backend=%T err=%v calls=%d", got, err, virtualCalls)
				}
			} else if got != nil || !errors.Is(err, tc.cause) || virtualCalls != 0 {
				t.Fatalf("terminal bridge failure ignored: backend=%T err=%v virtualCalls=%d", got, err, virtualCalls)
			}
		})
	}
}

type pointerGnomeBridge struct {
	recordingGnomeBridge
	events                        []string
	moveErr, buttonErr, scrollErr error
	cancelOnPress                 context.CancelFunc
}

func (b *pointerGnomeBridge) PointerMove(_ context.Context, x, y int32) error {
	b.events = append(b.events, fmt.Sprintf("move:%d,%d", x, y))
	return b.moveErr
}
func (b *pointerGnomeBridge) PointerButton(_ context.Context, button uint32, pressed bool) error {
	state := "up"
	if pressed {
		state = "down"
	}
	b.events = append(b.events, fmt.Sprintf("%s:%d", state, button))
	if pressed && b.cancelOnPress != nil {
		b.cancelOnPress()
	}
	return b.buttonErr
}
func (b *pointerGnomeBridge) Scroll(_ context.Context, axis string, amount float64) error {
	b.events = append(b.events, fmt.Sprintf("scroll:%s:%g", axis, amount))
	return b.scrollErr
}

func TestGnomePointerNormalClickAndFourScrollDirections(t *testing.T) {
	bridge := &pointerGnomeBridge{}
	b := &GnomeNativeBackend{bridge: bridge}
	if err := b.MouseClick(context.Background(), 234, 117, 1); err != nil {
		t.Fatal(err)
	}
	want := "move:234,117,down:1,up:1"
	if got := strings.Join(bridge.events, ","); got != want {
		t.Fatalf("click events=%q want %q", got, want)
	}
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"up", func() error { return b.ScrollUp(context.Background(), 3) }, "scroll:vertical:-3"},
		{"down", func() error { return b.ScrollDown(context.Background(), 2) }, "scroll:vertical:2"},
		{"left", func() error { return b.ScrollLeft(context.Background(), 4) }, "scroll:horizontal:-4"},
		{"right", func() error { return b.ScrollRight(context.Background(), 1) }, "scroll:horizontal:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err != nil {
				t.Fatal(err)
			}
			if got := bridge.events[len(bridge.events)-1]; got != tc.want {
				t.Errorf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestGnomePointerFailuresDoNotLeaveButtonHeld(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setup      func(*pointerGnomeBridge, context.CancelFunc)
		wantEvents string
	}{
		{"move", func(b *pointerGnomeBridge, _ context.CancelFunc) { b.moveErr = errors.New("move failed") }, "move:12,13"},
		{"cancel-after-down", func(b *pointerGnomeBridge, c context.CancelFunc) { b.cancelOnPress = c }, "move:12,13,down:1,up:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bridge := &pointerGnomeBridge{}
			tc.setup(bridge, cancel)
			b := &GnomeNativeBackend{bridge: bridge}
			err := b.MouseClick(ctx, 12, 13, 1)
			if err == nil {
				t.Fatal("expected injected failure")
			}
			if got := strings.Join(bridge.events, ","); got != tc.wantEvents {
				t.Errorf("unexpected event sequence %s", got)
			}
		})
	}
}

func TestGnomePointerRejectsBadCoordinatesAndButtonsBeforeEmitting(t *testing.T) {
	bridge := &pointerGnomeBridge{}
	b := &GnomeNativeBackend{bridge: bridge}
	for _, xy := range [][2]int{{math.MaxInt, 0}, {0, math.MinInt}} {
		if err := b.MouseMove(context.Background(), xy[0], xy[1]); err == nil {
			t.Errorf("move %v unexpectedly accepted", xy)
		}
	}
	for _, button := range []int{-1, 0, 4, 255} {
		if err := b.MouseClick(context.Background(), 10, 20, button); err == nil {
			t.Errorf("click %d accepted", button)
		}
		if err := b.MouseDown(context.Background(), button); err == nil {
			t.Errorf("down %d accepted", button)
		}
	}
	if len(bridge.events) != 0 {
		t.Errorf("invalid operations emitted: %v", bridge.events)
	}
}

func TestGnomeScrollZeroAndInvalidDoNotEmit(t *testing.T) {
	bridge := &pointerGnomeBridge{}
	b := &GnomeNativeBackend{bridge: bridge}
	for _, n := range []int{-1, math.MaxInt} {
		if err := b.ScrollDown(context.Background(), n); err == nil {
			t.Errorf("scroll %d accepted", n)
		}
	}
	if err := b.ScrollDown(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if len(bridge.events) != 0 {
		t.Errorf("unexpected scroll events %v", bridge.events)
	}
}

// A late GNOME D-Bus reply after cancellation must not leak a point alongside
// its cancellation error. Callers can accidentally use nonzero output values
// even when they correctly check err in an upstream adapter.
func TestGnomeCanceledPointerLocationDoesNotPublishLateCoordinates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bridge := &cancelingGnomeBridge{cancel: cancel}
	b := &GnomeNativeBackend{bridge: bridge}
	x, y, err := b.PointerLocation(ctx)
	if !errors.Is(err, context.Canceled) || x != 0 || y != 0 {
		t.Fatalf("late canceled pointer result disclosed (%d,%d) error=%v", x, y, err)
	}
}

type gnomeErrorPointerBridge struct{ lifecycleGnomeBridge }

func (b *gnomeErrorPointerBridge) PointerLocation(context.Context) (int, int, error) {
	return 117, 302, errors.New("synthetic pointer provider failed")
}
func TestGnomeFailedPointerLocationDoesNotPublishUntrustedCoordinates(t *testing.T) {
	b := &GnomeNativeBackend{bridge: &gnomeErrorPointerBridge{}}
	x, y, err := b.PointerLocation(context.Background())
	if err == nil || x != 0 || y != 0 {
		t.Fatalf("failed provider result disclosed (%d,%d) error=%v", x, y, err)
	}
}

// Revoked input authority wins over malformed key, button, and scroll
// arguments. Validation must not bypass the operation lifetime boundary.
func TestGnomePreCanceledInputsRejectBeforeArgumentValidation(t *testing.T) {
	b := &GnomeNativeBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := []struct {
		name string
		run  func(context.Context) error
	}{
		{"key down", func(c context.Context) error { return b.KeyDown(c, "not_a_key_name") }},
		{"key up", func(c context.Context) error { return b.KeyUp(c, "not_a_key_name") }},
		{"mouse down", func(c context.Context) error { return b.MouseDown(c, 99) }},
		{"mouse up", func(c context.Context) error { return b.MouseUp(c, 99) }},
		{"mouse click", func(c context.Context) error { return b.MouseClick(c, 1, 2, 99) }},
		{"scroll up", func(c context.Context) error { return b.ScrollUp(c, -1) }},
		{"scroll down", func(c context.Context) error { return b.ScrollDown(c, -1) }},
		{"scroll left", func(c context.Context) error { return b.ScrollLeft(c, -1) }},
		{"scroll right", func(c context.Context) error { return b.ScrollRight(c, -1) }},
	}
	for _, tc := range operations {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled input returned %v", err)
			}
		})
	}
}

// A sequence containing a key which the backend cannot send must fail
// before changing desktop state, even if it follows a valid literal or held
// modifier. Invalid UTF-8 must never be silently converted into U+FFFD.
func TestGnomeNativeSequencePreflightRejectsUnknownKeysBeforeValidPrefix(t *testing.T) {
	for _, tc := range []struct{ name, sequence string }{
		{"literal then unknown named key", "private{not-a-real-key}"},
		{"modifier down then unknown named key", "{ctrl down}{not-a-real-key}"},
		{"valid named key then unknown", "{enter}{not-a-real-key}"},
		{"valid accelerator then unknown", "{ctrl+a}{not-a-real-key}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &lifecycleGnomeBridge{}
			backend := &GnomeNativeBackend{bridge: bridge}
			err := backend.Type(context.Background(), tc.sequence)
			if err == nil {
				t.Fatalf("unsupported sequence accepted: %q", tc.sequence)
			}
			if len(bridge.log) != 0 || len(bridge.events) != 0 || backend.held.any() {
				t.Fatalf("invalid sequence mutated desktop: events=%v log=%v held=%+v error=%v", bridge.events, bridge.log, backend.held, err)
			}
		})
	}
}

func TestGnomeNativeRejectsInvalidUTF8BeforeDesktopBridgeCalls(t *testing.T) {
	bad := string([]byte{0xff, 0xfe})
	for _, tc := range []struct {
		name string
		run  func(*GnomeNativeBackend) error
	}{
		{"literal", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "valid"+bad) }},
		{"type", func(b *GnomeNativeBackend) error { return b.Type(context.Background(), "valid"+bad) }},
		{"named key", func(b *GnomeNativeBackend) error { return b.KeyDown(context.Background(), bad) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &lifecycleGnomeBridge{}
			b := &GnomeNativeBackend{bridge: bridge}
			err := tc.run(b)
			if err == nil || !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("invalid UTF-8 accepted: %v", err)
			}
			if len(bridge.log) != 0 || len(bridge.events) != 0 {
				t.Fatalf("invalid UTF-8 sent to desktop: log=%v events=%v", bridge.log, bridge.events)
			}
		})
	}
}

// A stray explicit modifier release at the tail is invalid in XTEST/uinput
// and Wayland keyboard sequences; GNOME must not partially type the prefix
// before finding that the caller never held the modifier.
func TestGnomeNativeRejectsUnheldModifierReleaseBeforePrefix(t *testing.T) {
	for _, seq := range []string{"sensitive{ctrl up}", "{enter}{shift up}", "{alt up}", "{ctrl down}{shift up}"} {
		t.Run(seq, func(t *testing.T) {
			bridge := &lifecycleGnomeBridge{}
			b := &GnomeNativeBackend{bridge: bridge}
			err := b.Type(context.Background(), seq)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unheld modifier release not rejected: %v", err)
			}
			if len(bridge.log) != 0 || len(bridge.events) != 0 || b.held.any() {
				t.Fatalf("unheld modifier release sent partial keyboard events: log=%v keys=%v held=%+v", bridge.log, bridge.events, b.held)
			}
		})
	}
}

// Unicode needs a clipboard fallback on GNOME, which cannot run with a held
// modifier. Invalid late combinations must be rejected before any earlier
// text or key press reaches the window.
func TestGnomeNativePreflightsUnicodeAgainstHeldModifierSequence(t *testing.T) {
	for _, tc := range []struct{ name, seq string }{
		{"modifier before Unicode", "{ctrl down}é{ctrl up}"},
		{"literal prefix before modifier and Unicode", "safe{shift down}é"},
		{"accelerator before modifier and Unicode", "{ctrl+a}{alt down}é"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &lifecycleGnomeBridge{}
			b := &GnomeNativeBackend{bridge: bridge}
			err := b.Type(context.Background(), tc.seq)
			if err == nil || !strings.Contains(err.Error(), "held modifiers") {
				t.Fatalf("invalid clipboard gesture accepted: %v", err)
			}
			if len(bridge.log) != 0 || len(bridge.events) != 0 || b.held.any() {
				t.Fatalf("unsupported gesture partially mutated desktop: log=%v events=%v held=%+v", bridge.log, bridge.events, b.held)
			}
		})
	}
}

func TestGnomeNativeLiteralUnicodeDoesNotPasteWhileCallerHoldsModifier(t *testing.T) {
	bridge := &lifecycleGnomeBridge{}
	b := &GnomeNativeBackend{bridge: bridge}
	if err := b.KeyDown(context.Background(), "ctrl"); err != nil {
		t.Fatal(err)
	}
	bridge.log = nil
	bridge.events = nil
	err := b.TypeLiteral(context.Background(), "é")
	if err == nil || !strings.Contains(err.Error(), "held modifiers") {
		t.Fatalf("Unicode paste with caller-held Ctrl accepted: %v", err)
	}
	if len(bridge.log) != 0 || len(bridge.events) != 0 || !b.held.ctrl {
		t.Fatalf("unsupported paste changed caller-held modifier state: log=%v events=%v held=%+v", bridge.log, bridge.events, b.held)
	}
	if err := b.TypeLiteral(context.Background(), "ASCII"); err != nil {
		t.Fatalf("safe direct text with caller-held Ctrl rejected: %v", err)
	}
	if len(bridge.log) != 1 || bridge.log[0] != "text:ASCII" {
		t.Fatalf("safe text not typed directly: %v", bridge.log)
	}
}

// An unavailable GNOME bridge must never be advertised as authorized input.
func TestGnomeInputProbeFailsClosedWithoutBridge(t *testing.T) {
	rt := syntheticGNOMERuntime(t)
	result := checkGnomeNative(rt)
	if result.Available {
		t.Fatalf("missing GNOME input bridge advertised as available: %+v", result)
	}
	if !strings.Contains(strings.ToLower(result.Name), "gnome") {
		t.Fatalf("unexpected GNOME probe identity: %+v", result)
	}
	if result.Reason == "" {
		t.Fatalf("unavailable bridge produced no diagnostic: %+v", result)
	}
}

// A bridge may report a transport error after delivering the button-down
// notification. In that uncertain state, a compensating release is required.
type uncertainGnomePressBridge struct {
	pointerGnomeBridge
	pressErr   error
	releaseErr error
	cancel     context.CancelFunc
}

func (b *uncertainGnomePressBridge) PointerButton(ctx context.Context, button uint32, pressed bool) error {
	if pressed {
		b.events = append(b.events, fmt.Sprintf("down:%d", button))
		if b.cancel != nil {
			b.cancel()
		}
		return b.pressErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.events = append(b.events, fmt.Sprintf("up:%d", button))
	return b.releaseErr
}

func TestGnomeClickCleansUpUncertainPressAndPreservesFailures(t *testing.T) {
	for _, tc := range []struct {
		name         string
		button       int
		cancel       bool
		releaseFails bool
	}{
		{name: "left press fails", button: 1},
		{name: "middle press and release fail", button: 2, releaseFails: true},
		{name: "right press fails and cancels operation", button: 3, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pressErr := errors.New("bridge partially delivered press")
			releaseErr := errors.New("bridge failed to release")
			bridge := &uncertainGnomePressBridge{pressErr: pressErr}
			if tc.cancel {
				bridge.cancel = cancel
			}
			if tc.releaseFails {
				bridge.releaseErr = releaseErr
			}
			b := &GnomeNativeBackend{bridge: bridge}
			err := b.MouseClick(ctx, 12, 34, tc.button)
			if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("click failed to preserve press, release, cancellation errors: %v", err)
			}
			want := []string{"move:12,34", fmt.Sprintf("down:%d", tc.button), fmt.Sprintf("up:%d", tc.button)}
			if !slices.Equal(bridge.events, want) {
				t.Fatalf("bridge events=%v, want=%v", bridge.events, want)
			}
		})
	}
}

func TestGnomeNativeZeroScrollHonorsCancellationAndUnavailableBridge(t *testing.T) {
	for _, direction := range []struct {
		name string
		run  func(*GnomeNativeBackend, context.Context) error
	}{
		{"up", func(b *GnomeNativeBackend, ctx context.Context) error { return b.ScrollUp(ctx, 0) }},
		{"down", func(b *GnomeNativeBackend, ctx context.Context) error { return b.ScrollDown(ctx, 0) }},
		{"left", func(b *GnomeNativeBackend, ctx context.Context) error { return b.ScrollLeft(ctx, 0) }},
		{"right", func(b *GnomeNativeBackend, ctx context.Context) error { return b.ScrollRight(ctx, 0) }},
	} {
		t.Run(direction.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			bridge := &lifecycleGnomeBridge{}
			backend := &GnomeNativeBackend{bridge: bridge}
			if err := direction.run(backend, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled no-op scroll acknowledged: %v", err)
			}
			if err := direction.run(&GnomeNativeBackend{}, context.Background()); err == nil {
				t.Fatal("unavailable bridge acknowledged no-op scroll")
			}
			if err := direction.run(backend, context.Background()); err != nil {
				t.Fatalf("valid zero scroll rejected: %v", err)
			}
			if len(bridge.log) != 0 || len(bridge.events) != 0 {
				t.Fatalf("zero scroll injected events: log=%v keys=%v", bridge.log, bridge.events)
			}
		})
	}
}
