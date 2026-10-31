package input

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/wl"
	"strings"
	"testing"
)

func TestVirtualKeyboardNamedKeyProtocolMapping(t *testing.T) {
	cases := []struct {
		key    string
		code   uint32
		symbol string
	}{
		{"shift", kcShift, "Shift_L"}, {"ctrl", kcCtrl, "Control_L"}, {"alt", kcAlt, "Alt_L"}, {"super", kcSuper, "Super_L"},
		{"enter", kcReturn, "Return"}, {"escape", kcEscape, "Escape"}, {"tab", kcTab, "Tab"},
		{"backspace", kcBksp, "BackSpace"}, {"delete", kcDelete, "Delete"},
		{"home", kcHome, "Home"}, {"end", kcEnd, "End"}, {"pageup", kcPgUp, "Prior"}, {"pagedown", kcPgDown, "Next"},
		{"left", kcLeft, "Left"}, {"right", kcRight, "Right"}, {"up", kcUp, "Up"}, {"down", kcDown, "Down"},
		{"f1", kcF1, "F1"}, {"f2", kcF1 + 1, "F2"}, {"f3", kcF1 + 2, "F3"}, {"f4", kcF1 + 3, "F4"},
		{"f5", kcF1 + 4, "F5"}, {"f6", kcF1 + 5, "F6"}, {"f7", kcF1 + 6, "F7"},
		{"f8", kcF1 + 7, "F8"}, {"f9", kcF1 + 8, "F9"}, {"f10", kcF1 + 9, "F10"}, {"f11", kcF1 + 10, "F11"}, {"f12", kcF1 + 11, "F12"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			code, sym, ok := namedKey(tc.key)
			if !ok || code != tc.code || sym != tc.symbol {
				t.Fatalf("protocol mapping for %q: %d %q %t; want %d %q", tc.key, code, sym, ok, tc.code, tc.symbol)
			}
			if sym == "" {
				t.Fatal("named key missing keysym")
			}
		})
	}
	for _, key := range []string{"not-a-key", "F13", "pageupgarbage"} {
		if _, _, ok := namedKey(key); ok {
			t.Fatalf("unrecognized key %q mapped to protocol", key)
		}
	}
}

func TestVirtualKeyboardUnavailableTransportReleasesTemporaryModifiers(t *testing.T) {
	k, conn := newFailingKeyboard(2)
	seq, err := parseKeySend("{ctrl+a}")
	if err != nil {
		t.Fatal(err)
	}
	if err := k.sendkeys(context.Background(), seq); err == nil {
		t.Fatal("failed key injection was acknowledged")
	}
	if k.mods != 0 || len(k.held) != 0 {
		t.Fatalf("failed key injection retained pressed state: mods=%d held=%v", k.mods, k.held)
	}
	if len(conn.msgs) != 2 {
		t.Fatalf("expected attempted keymap + modifier before failing key: %d", len(conn.msgs))
	}
	if wl.Uint32(conn.msgs[1][4:8])&0xffff != 2 {
		t.Fatalf("second request was not temporary modifier state: %x", conn.msgs[1])
	}
}

func TestVirtualKeyboardRejectsCanceledOperationsAndUnbalancedKeys(t *testing.T) {
	k, conn := newTestKeyboard()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := k.sendkeys(ctx, []keySend{{key: "enter"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled operation accepted: %v", err)
	}
	if conn.writes != 0 {
		t.Fatalf("canceled operation emitted %d messages", conn.writes)
	}
	if err := k.releaseKey(context.Background(), "q"); err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("unheld key release accepted: %v", err)
	}
	if conn.writes != 0 {
		t.Fatalf("unheld release emitted messages: %d", conn.writes)
	}
	if err := k.sendkeys(context.Background(), []keySend{{key: "enter", up: true}}); err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("unheld named release accepted: %v", err)
	}
	if len(k.held) != 0 {
		t.Fatalf("phantom held state retained: %v", k.held)
	}
}
