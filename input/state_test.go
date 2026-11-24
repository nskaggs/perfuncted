package input

import (
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
)

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
