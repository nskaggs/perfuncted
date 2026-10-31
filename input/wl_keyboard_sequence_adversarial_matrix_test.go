//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

type keyboardWireEvent struct {
	opcode  uint32
	keycode uint32
	state   uint32
}

func keyboardEventsFromMessages(t *testing.T, msgs [][]byte) []keyboardWireEvent {
	t.Helper()
	var out []keyboardWireEvent
	for _, msg := range msgs {
		if len(msg) < 8 {
			t.Fatalf("truncated keyboard message %x", msg)
		}
		opcode := wl.Uint32(msg[4:8]) & 0xffff
		switch opcode {
		case 1:
			if len(msg) != 20 {
				t.Fatalf("malformed key event %x", msg)
			}
			out = append(out, keyboardWireEvent{opcode: 1, keycode: wl.Uint32(msg[12:16]) + 8, state: wl.Uint32(msg[16:20])})
		case 2:
			if len(msg) != 24 {
				t.Fatalf("malformed modifier event %x", msg)
			}
			out = append(out, keyboardWireEvent{opcode: 2, state: wl.Uint32(msg[8:12])})
		case 0:
			if len(msg) < 16 {
				t.Fatalf("malformed keymap event %x", msg)
			}
		default:
			t.Fatalf("unexpected keyboard wire opcode %d", opcode)
		}
	}
	return out
}
func TestVirtualKeyboardMixedTextNamedAndAcceleratorsPreserveProtocolOrder(t *testing.T) {
	k, ctx := newTestKeyboard()
	actions := []keySend{{text: "a"}, {key: "enter"}, {key: "b", modifiers: modifiers{ctrl: true}}, {text: "É"}}
	if err := k.sendkeys(context.Background(), actions); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, ctx.msgs)
	if len(events) != 10 {
		t.Fatalf("wrong number of key/modifier operations: %+v", events)
	}
	for _, pair := range [][2]int{{0, 1}, {2, 3}, {5, 6}, {8, 9}} {
		if events[pair[0]].opcode != 1 || events[pair[0]].state != 1 || events[pair[1]].opcode != 1 || events[pair[1]].state != 0 || events[pair[0]].keycode != events[pair[1]].keycode {
			t.Fatalf("unbalanced key pair at %d: %+v", pair[0], events)
		}
	}
	if events[4].opcode != 2 || events[4].state != modControl || events[7].opcode != 2 || events[7].state != 0 {
		t.Fatalf("Ctrl accelerator modifier missing: %+v", events)
	}
	if k.mods != 0 || len(k.held) != 0 {
		t.Fatalf("mixed typing left pressed keys: mods=%d held=%v", k.mods, k.held)
	}
}
func TestVirtualKeyboardExplicitHeldModifierSurvivesInterleavedTextUntilRelease(t *testing.T) {
	k, ctx := newTestKeyboard()
	actions := []keySend{{key: "ctrl", down: true}, {key: "a"}, {text: "b"}, {key: "ctrl", up: true}}
	if err := k.sendkeys(context.Background(), actions); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, ctx.msgs)
	var mods []uint32
	for _, ev := range events {
		if ev.opcode == 2 {
			mods = append(mods, ev.state)
		}
	}
	if len(mods) != 2 || mods[0] != modControl || mods[1] != 0 {
		t.Fatalf("held Ctrl unexpectedly released during input: events=%+v", events)
	}
	if k.mods != 0 || len(k.held) != 0 {
		t.Fatalf("modifier remained held: mods=%d held=%v", k.mods, k.held)
	}
}
func TestVirtualKeyboardExplicitHeldNamedAndLiteralKeysAreBalanced(t *testing.T) {
	for _, key := range []string{"enter", "é"} {
		t.Run(key, func(t *testing.T) {
			k, ctx := newTestKeyboard()
			if err := k.sendkeys(context.Background(), []keySend{{key: key, down: true}, {key: key, up: true}}); err != nil {
				t.Fatal(err)
			}
			events := keyboardEventsFromMessages(t, ctx.msgs)
			if len(events) != 2 || events[0].opcode != 1 || events[0].state != 1 || events[1].opcode != 1 || events[1].state != 0 || events[0].keycode != events[1].keycode {
				t.Fatalf("bad held-key protocol sequence for %q: %+v", key, events)
			}
			if len(k.held) != 0 {
				t.Fatalf("held %q survived its release: %v", key, k.held)
			}
		})
	}
}
func TestVirtualKeyboardBadHoldOrReleaseNeverInjectsKeyTransitions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action keySend
		want   string
	}{
		{"unknown named key up", keySend{key: "f3", up: true}, "not held"},
		{"literal not held", keySend{key: "x", up: true}, "not held"},
		{"multi rune hold", keySend{key: "abc", down: true}, "cannot hold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx := newTestKeyboard()
			err := k.sendkeys(context.Background(), []keySend{tc.action})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unsafe key action accepted: %v", err)
			}
			for _, ev := range keyboardEventsFromMessages(t, ctx.msgs) {
				if ev.opcode == 1 {
					t.Fatalf("invalid action injected key before rejection: %+v", ev)
				}
			}
			if len(k.held) != 0 || k.mods != 0 {
				t.Fatalf("invalid action retained state: %v %d", k.held, k.mods)
			}
		})
	}
}

type cancelOnKeyboardPress struct {
	recordingCtx
	cancel  context.CancelFunc
	pressed bool
}

func (c *cancelOnKeyboardPress) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := c.WriteMsg(data, oob)
	if err == nil && !c.pressed && len(data) == 20 && wl.Uint32(data[4:8])&0xffff == 1 && wl.Uint32(data[16:20]) == 1 {
		c.pressed = true
		c.cancel()
	}
	return err
}
func TestVirtualKeyboardCanceledAfterPressStillReleasesKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k, _ := newTestKeyboard()
	wire := &cancelOnKeyboardPress{cancel: cancel}
	k.ctx = wire
	err := k.sendkeys(ctx, []keySend{{key: "enter"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation during held key returned %v", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	if len(events) != 2 || events[0].state != 1 || events[1].state != 0 || events[0].keycode != events[1].keycode {
		t.Fatalf("canceled key was left pressed: %+v", events)
	}
}
