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

type cancelAfterKeyboardRelease struct {
	recordingCtx
	cancel context.CancelFunc
}

func (c *cancelAfterKeyboardRelease) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.WriteMsg(data, oob); err != nil {
		return err
	}
	if len(data) == 20 && wl.Uint32(data[4:8])&0xffff == 1 && wl.Uint32(data[16:20]) == 0 {
		c.cancel()
	}
	return nil
}

func TestVirtualKeyboardCancellationAfterKeyReleaseStillReleasesTemporaryModifiers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	keyboard, _ := newTestKeyboard()
	wire := &cancelAfterKeyboardRelease{cancel: cancel}
	keyboard.ctx = wire

	err := keyboard.sendkeys(ctx, []keySend{{key: "enter", modifiers: modifiers{ctrl: true}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled key sequence returned %v, want context.Canceled", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	var modifierStates []uint32
	for _, event := range events {
		if event.opcode == 2 {
			modifierStates = append(modifierStates, event.state)
		}
	}
	if len(modifierStates) != 2 || modifierStates[0] != modControl || modifierStates[1] != 0 {
		t.Fatalf("temporary modifier remained pressed after cancellation: %+v", events)
	}
	if keyboard.mods != 0 || len(keyboard.held) != 0 {
		t.Fatalf("canceled sequence retained key state: mods=%d held=%v", keyboard.mods, keyboard.held)
	}
}

type failOnceKeyboardWrite struct {
	recordingCtx
	failAt int
	failed bool
}

func (c *failOnceKeyboardWrite) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.writes == c.failAt && !c.failed {
		c.failed = true
		return errors.New("injected one-time keyboard write failure")
	}
	return c.recordingCtx.WriteMsg(data, oob)
}

func TestVirtualKeyboardModifierPressFailureReleasesModifierKey(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		action keySend
		failAt int
	}{
		{name: "hold state publication", action: keySend{key: "ctrl", down: true}, failAt: 2},
		{name: "tap state publication", action: keySend{key: "ctrl"}, failAt: 2},
		{name: "tap key release", action: keySend{key: "ctrl"}, failAt: 3},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			keyboard, _ := newTestKeyboard()
			wire := &failOnceKeyboardWrite{failAt: testCase.failAt}
			keyboard.ctx = wire

			err := keyboard.sendkeys(context.Background(), []keySend{testCase.action})
			if err == nil || !strings.Contains(err.Error(), "injected one-time keyboard write failure") {
				t.Fatalf("modifier sequence error = %v, want injected modifier-state failure", err)
			}
			events := keyboardEventsFromMessages(t, wire.msgs)
			var keys []keyboardWireEvent
			var modifierStates []uint32
			for _, event := range events {
				if event.opcode == 1 {
					keys = append(keys, event)
				} else if event.opcode == 2 {
					modifierStates = append(modifierStates, event.state)
				}
			}
			if len(keys) != 2 || keys[0].keycode != kcCtrl || keys[0].state != 1 || keys[1].keycode != kcCtrl || keys[1].state != 0 {
				t.Fatalf("failed modifier operation left the key pressed: %+v", events)
			}
			if len(modifierStates) == 0 || modifierStates[len(modifierStates)-1] != 0 {
				t.Fatalf("modifier state not cleared after failed operation: %+v", events)
			}
			if keyboard.mods != 0 || len(keyboard.held) != 0 {
				t.Fatalf("failed modifier press retained logical state: mods=%d held=%v", keyboard.mods, keyboard.held)
			}
		})
	}
}

func TestVirtualKeyboardCombinedTemporaryModifierMaskIsBalanced(t *testing.T) {
	keyboard, wire := newTestKeyboard()
	allModifiers := modifiers{ctrl: true, alt: true, shift: true, super: true}
	if err := keyboard.sendkeys(context.Background(), []keySend{{key: "enter", modifiers: allModifiers}}); err != nil {
		t.Fatalf("send key with all temporary modifiers: %v", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	var modifierStates []uint32
	var keys []keyboardWireEvent
	for _, event := range events {
		if event.opcode == 2 {
			modifierStates = append(modifierStates, event.state)
		} else if event.opcode == 1 {
			keys = append(keys, event)
		}
	}
	wantMask := modControl | modMod1 | modShift | modMod4
	if len(modifierStates) != 2 || modifierStates[0] != wantMask || modifierStates[1] != 0 {
		t.Fatalf("combined temporary modifier states = %v, want [%d 0]", modifierStates, wantMask)
	}
	if len(keys) != 2 || keys[0].state != 1 || keys[1].state != 0 || keys[0].keycode != keys[1].keycode {
		t.Fatalf("modified key events are not balanced: %+v", keys)
	}
}

func TestVirtualKeyboardFailedTemporaryModifierSetupNeverDispatchesKey(t *testing.T) {
	keyboard, _ := newTestKeyboard()
	wire := &failOnceKeyboardWrite{failAt: 1}
	keyboard.ctx = wire
	err := keyboard.sendkeys(context.Background(), []keySend{{key: "enter", modifiers: modifiers{shift: true}}})
	if err == nil || !strings.Contains(err.Error(), "injected one-time keyboard write failure") {
		t.Fatalf("failed temporary modifier setup returned %v, want the injected failure", err)
	}
	for _, event := range keyboardEventsFromMessages(t, wire.msgs) {
		if event.opcode == 1 {
			t.Fatalf("key dispatched after temporary modifier setup failed: %+v", event)
		}
	}
	if keyboard.mods != 0 || len(keyboard.held) != 0 {
		t.Fatalf("failed temporary modifier setup retained key state: mods=%d held=%v", keyboard.mods, keyboard.held)
	}
}

func TestVirtualKeyboardRetainsModifierStateWhenCleanupTransportFails(t *testing.T) {
	keyboard, wire := newFailingKeyboard(3)
	err := keyboard.sendkeys(context.Background(), []keySend{{key: "ctrl"}})
	if err == nil || !strings.Contains(err.Error(), "injected write failure") {
		t.Fatalf("modifier cleanup failure returned %v, want the injected transport error", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	if len(events) != 2 || events[0].opcode != 1 || events[0].keycode != kcCtrl || events[0].state != 1 || events[1].opcode != 2 || events[1].state != modControl {
		t.Fatalf("unexpected wire state after cleanup transport failure: %+v", events)
	}
	if keyboard.mods != modControl {
		t.Fatalf("last known modifier state = %d, want %d", keyboard.mods, modControl)
	}
}

type failSelectedKeyboardWrites struct {
	recordingCtx
	failAt map[int]struct{}
}

func (c *failSelectedKeyboardWrites) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, fail := c.failAt[c.writes]; fail {
		delete(c.failAt, c.writes)
		return errors.New("injected selected keyboard write failure")
	}
	return c.recordingCtx.WriteMsg(data, oob)
}

func TestVirtualKeyboardRetainsModifierStateWhenModifierCleanupPublicationFails(t *testing.T) {
	keyboard, _ := newTestKeyboard()
	wire := &failSelectedKeyboardWrites{failAt: map[int]struct{}{4: {}, 5: {}}}
	keyboard.ctx = wire

	err := keyboard.sendkeys(context.Background(), []keySend{{key: "ctrl"}})
	if err == nil || !strings.Contains(err.Error(), "injected selected keyboard write failure") {
		t.Fatalf("modifier cleanup error = %v, want the injected state publication failure", err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs)
	if len(events) != 4 || events[0].opcode != 1 || events[0].keycode != kcCtrl || events[0].state != 1 || events[1].opcode != 2 || events[1].state != modControl || events[2].opcode != 1 || events[2].keycode != kcCtrl || events[2].state != 0 || events[3].opcode != 1 || events[3].keycode != kcCtrl || events[3].state != 0 {
		t.Fatalf("unexpected wire state after modifier cleanup publication failure: %+v", events)
	}
	if keyboard.mods != modControl {
		t.Fatalf("last published modifier state = %d, want %d", keyboard.mods, modControl)
	}
}
