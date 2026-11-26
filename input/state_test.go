package input

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb/xtest"
	"github.com/nskaggs/perfuncted/v2/internal/x11test"
	"os"
	"testing"
	"time"
)

func TestReviewStateQueryAdmissionErrors(t *testing.T) {
	b := &x11Observer{display: "not-a-local-display"}
	queries := []struct {
		name string
		run  func(context.Context) (StateObservation, error)
	}{
		{"key", func(ctx context.Context) (StateObservation, error) { return b.KeyState(ctx, "f8") }},
		{"lock", func(ctx context.Context) (StateObservation, error) { return b.LockState(ctx, "capslock") }},
		{"button", func(ctx context.Context) (StateObservation, error) { return b.MouseButtonState(ctx, 1) }},
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, q := range queries {
		for _, tc := range []struct {
			name string
			ctx  context.Context
			want error
		}{
			{"nil", nil, ErrInvalidStateQuery}, {"canceled", canceled, context.Canceled}, {"untrusted display", context.Background(), ErrObservationUnsupported},
		} {
			t.Run(q.name+"/"+tc.name, func(t *testing.T) {
				got, err := q.run(tc.ctx)
				if !errors.Is(err, tc.want) || got != (StateObservation{}) {
					t.Fatalf("got=%+v err=%v want=%v", got, err, tc.want)
				}
			})
		}
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	for _, q := range queries {
		got, err := q.run(context.Background())
		if !errors.Is(err, ErrObservationRevoked) || got != (StateObservation{}) {
			t.Fatalf("%s after close: %+v %v", q.name, got, err)
		}
	}
}

func TestReviewStateQueryErrorClassification(t *testing.T) {
	b := &x11Observer{}
	generic := errors.New("transport failed")
	for _, tc := range []struct{ err, want error }{
		{nil, nil}, {generic, ErrObservationUnavailable}, {xproto.AccessError{}, ErrObservationDenied}, {ErrObservationUnsupported, ErrObservationUnsupported}, {ErrObservationDenied, ErrObservationDenied}, {ErrObservationUnavailable, ErrObservationUnavailable},
	} {
		got := b.stateQueryError(context.Background(), tc.err)
		if !errors.Is(got, tc.want) {
			t.Errorf("err=%v got=%v want=%v", tc.err, got, tc.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.stateQueryError(ctx, generic); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := b.finishStateQuery(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.stateQueryError(context.Background(), generic); !errors.Is(err, ErrObservationRevoked) {
		t.Fatal(err)
	}
	if err := b.finishStateQuery(context.Background()); !errors.Is(err, ErrObservationRevoked) {
		t.Fatal(err)
	}
}

func TestInputStateQueryValidationAndAliases(t *testing.T) {
	for _, name := range []string{"shift", "Shift_L", "CTRL", "leftalt", "capslock", "f8"} {
		if err := ValidateKeyStateQuery(name); err != nil {
			t.Errorf("ValidateKeyStateQuery(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", "not-a-key", "F8\x00"} {
		if err := ValidateKeyStateQuery(name); !errors.Is(err, ErrInvalidStateQuery) {
			t.Errorf("ValidateKeyStateQuery(%q) = %v, want invalid query", name, err)
		}
	}
	for _, name := range []string{"capslock", "CAPS_LOCK", "numlock", "Num_Lock", "scrolllock"} {
		if err := ValidateLockStateQuery(name); err != nil {
			t.Errorf("ValidateLockStateQuery(%q): %v", name, err)
		}
	}
	if err := ValidateLockStateQuery("compose"); !errors.Is(err, ErrInvalidStateQuery) {
		t.Fatalf("invalid lock state was accepted: %v", err)
	}
	for _, button := range []int{1, 2, 3, 4, 5} {
		if err := ValidateMouseButtonStateQuery(button); err != nil {
			t.Errorf("ValidateMouseButtonStateQuery(%d): %v", button, err)
		}
	}
	for _, button := range []int{0, -1, 6} {
		if err := ValidateMouseButtonStateQuery(button); !errors.Is(err, ErrInvalidStateQuery) {
			t.Errorf("ValidateMouseButtonStateQuery(%d) = %v, want invalid query", button, err)
		}
	}
}

func TestInputStateKeyNameNormalization(t *testing.T) {
	for _, tc := range []struct {
		name      string
		canonical string
		count     int
	}{
		{" Shift ", "shift", 2},
		{"Control_L", "control_l", 1},
		{"F8", "f8", 1},
	} {
		canonical, symbols, err := normalizeStateKey(tc.name)
		if err != nil || canonical != tc.canonical || len(symbols) != tc.count {
			t.Errorf("normalizeStateKey(%q) = %q, %d symbols, %v", tc.name, canonical, len(symbols), err)
		}
	}
}

func TestX11LockModifierMaskRequiresUnambiguousMapping(t *testing.T) {
	valid := &xproto.GetModifierMappingReply{
		KeycodesPerModifier: 1,
		Keycodes:            []xproto.Keycode{0, 10, 0, 0, 20, 0, 0, 0},
	}
	mask, err := resolveX11LockModifierMask(valid, map[xproto.Keycode]bool{10: true}, map[xproto.Keycode]bool{20: true})
	if err != nil || mask != xproto.ModMaskLock {
		t.Fatalf("unique Caps Lock mapping = %#x, %v", mask, err)
	}

	ambiguous := &xproto.GetModifierMappingReply{
		KeycodesPerModifier: 2,
		Keycodes:            []xproto.Keycode{0, 0, 10, 20, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}
	if _, err := resolveX11LockModifierMask(ambiguous, map[xproto.Keycode]bool{10: true}, map[xproto.Keycode]bool{20: true}); !errors.Is(err, ErrObservationUnsupported) {
		t.Fatalf("shared Caps/Num modifier was accepted: %v", err)
	}

	if _, err := resolveX11LockModifierMask(valid, map[xproto.Keycode]bool{10: true}, map[xproto.Keycode]bool{10: true}); !errors.Is(err, ErrObservationUnsupported) {
		t.Fatalf("same keycode mapped as two locks was accepted: %v", err)
	}
	if _, err := resolveX11LockModifierMask(valid, map[xproto.Keycode]bool{99: true}, nil); !errors.Is(err, ErrObservationUnsupported) {
		t.Fatalf("unmapped lock key was accepted: %v", err)
	}
	if _, err := resolveX11LockModifierMask(&xproto.GetModifierMappingReply{}, nil, nil); !errors.Is(err, ErrObservationUnavailable) {
		t.Fatalf("malformed modifier mapping: %v", err)
	}
}

func TestX11LockModifierMaskRejectsUnrelatedKeyInLockModifier(t *testing.T) {
	mapping := &xproto.GetModifierMappingReply{
		KeycodesPerModifier: 2,
		Keycodes:            []xproto.Keycode{0, 0, 10, 30, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}
	if _, err := resolveX11LockModifierMask(mapping, map[xproto.Keycode]bool{10: true}, nil); !errors.Is(err, ErrObservationUnsupported) {
		t.Fatalf("Caps Lock shared with an unrelated mapped key was accepted: %v", err)
	}
}

func TestX11LockModifierMaskRejectsKeyMappedToMultipleModifiers(t *testing.T) {
	mapping := &xproto.GetModifierMappingReply{
		KeycodesPerModifier: 2,
		Keycodes:            []xproto.Keycode{10, 0, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}
	if _, err := resolveX11LockModifierMask(mapping, map[xproto.Keycode]bool{10: true}, nil); !errors.Is(err, ErrObservationUnsupported) {
		t.Fatalf("lock key mapped to multiple modifiers was accepted: %v", err)
	}
}

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
