package input

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// Type must keep its explicit modifier ownership consistent with KeyDown/Up.
// In particular a caller-held Ctrl cannot be released as a temporary key,
// and a later command must see the same state as the previous command.
func TestXTestExplicitModifierHoldAndReleaseAcrossCommands(t *testing.T) {
	b, events := newModifierXTestBackend(t)
	ctrl := keycodeForCtrl(t, b)
	a, err := b.keycodeFor("a")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		command string
		want    []xtestEvent
		held    bool
	}{
		{"{ctrl down}", []xtestEvent{{xproto.KeyPress, ctrl}}, true},
		{"{ctrl+a}", []xtestEvent{{xproto.KeyPress, byte(a)}, {xproto.KeyRelease, byte(a)}}, true},
		{"{ctrl up}", []xtestEvent{{xproto.KeyRelease, ctrl}}, false},
		{"{ctrl+a}", []xtestEvent{{xproto.KeyPress, ctrl}, {xproto.KeyPress, byte(a)}, {xproto.KeyRelease, byte(a)}, {xproto.KeyRelease, ctrl}}, false},
	} {
		*events = nil
		if err := b.Type(context.Background(), tc.command); err != nil {
			t.Fatalf("type %q: %v", tc.command, err)
		}
		if !reflect.DeepEqual(*events, tc.want) {
			t.Fatalf("type %q sent %+v, want %+v", tc.command, *events, tc.want)
		}
		if held := b.modifierHeld(xproto.Keycode(ctrl)); held != tc.held {
			t.Fatalf("type %q owned ctrl=%t want=%t", tc.command, held, tc.held)
		}
	}
}

func TestXTestModifierTransactionFailsClosedAtEveryProtocolBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAt int
		want   []xtestEvent
	}{
		{"mod down fails", 1, []xtestEvent{{xproto.KeyPress, 8}}},
		{"key down fails", 2, []xtestEvent{{xproto.KeyPress, 8}, {xproto.KeyPress, 9}, {xproto.KeyRelease, 8}}},
		{"key up fails", 3, []xtestEvent{{xproto.KeyPress, 8}, {xproto.KeyPress, 9}, {xproto.KeyRelease, 9}, {xproto.KeyRelease, 8}}},
		{"mod up fails", 4, []xtestEvent{{xproto.KeyPress, 8}, {xproto.KeyPress, 9}, {xproto.KeyRelease, 9}, {xproto.KeyRelease, 8}, {xproto.KeyRelease, 8}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0xffe3, 0x61}, func(ev xtestEvent) error {
				calls++
				if calls == tc.failAt {
					return errors.New("synthetic XTEST wire write rejected")
				}
				return nil
			})
			// Ctrl is slot 8, A is slot 9.
			err := b.Type(context.Background(), "{ctrl+a}")
			if err == nil {
				t.Fatal("failed XTEST wire message was acknowledged")
			}
			if !reflect.DeepEqual(*events, tc.want) {
				t.Fatalf("failed wire request and cleanup events=%v, want=%v", *events, tc.want)
			}
			if b.modifierHeld(xproto.Keycode(8)) {
				t.Fatal("failed temporary modifier transaction retained caller-owned Ctrl")
			}
		})
	}
}
