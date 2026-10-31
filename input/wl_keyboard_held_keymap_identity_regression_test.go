//go:build linux

package input

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

type keyboardKeymapWire struct {
	recordingCtx
	maps []string
}

func (r *keyboardKeymapWire) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if len(data) >= 16 && wl.Uint32(data[4:8])&0xffff == 0 {
		cms, err := syscall.ParseSocketControlMessage(oob)
		if err != nil {
			return err
		}
		if len(cms) != 1 {
			return fmt.Errorf("expected keymap fd, got %d control messages", len(cms))
		}
		fds, err := syscall.ParseUnixRights(&cms[0])
		if err != nil {
			return err
		}
		if len(fds) != 1 {
			return fmt.Errorf("expected 1 keymap fd, got %d", len(fds))
		}
		dup, err := syscall.Dup(fds[0])
		if err != nil {
			return err
		}
		f := os.NewFile(uintptr(dup), "keymap test duplicate")
		// Ancillary FDs refer to a shared memory file whose writer is at EOF;
		// the compositor maps it from zero rather than reading that offset.
		if _, lintShadow45 := f.Seek(0, io.SeekStart); lintShadow45 != nil {
			_ = f.Close()
			return lintShadow45
		}
		lintShadow49, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil {
			return err
		}
		r.maps = append(r.maps, string(lintShadow49))
	}
	return r.recordingCtx.WriteMsgContext(ctx, data, oob)
}

// Remapping an XKB code while that key is held can make releasing it target
// a different key, so a different batch of Unicode input must preserve the
// original held key's wire mapping across keymap uploads.
func TestVirtualKeyboardRetainsHeldKeymapIdentityAcrossInterleavedUnicode(t *testing.T) {
	for _, tc := range []struct{ name, held, text string }{
		{"named enter", "enter", "é"},
		{"named escape", "escape", "é"},
		{"literal unicode", "é", "Ω"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, _ := newTestKeyboard()
			wire := &keyboardKeymapWire{}
			k.ctx = wire
			if err := k.sendkeys(context.Background(), []keySend{{key: tc.held, down: true}}); err != nil {
				t.Fatal(err)
			}
			heldSlot := k.held[tc.held]
			if heldSlot == 0 {
				t.Fatal("no initial held slot")
			}
			first := len(wire.msgs)
			if err := k.sendkeys(context.Background(), []keySend{{text: tc.text}, {key: tc.held, up: true}}); err != nil {
				t.Fatal(err)
			}
			events := keyboardEventsFromMessages(t, wire.msgs[first:])
			if len(events) != 3 || events[0].keycode == heldSlot || events[2].keycode != heldSlot || events[2].state != 0 {
				t.Fatalf("wrong held-key wire handling: slot=%d events=%+v", heldSlot, events)
			}
			if len(wire.maps) < 2 {
				t.Fatal("missing compositor keymap upload")
			}
			last := wire.maps[len(wire.maps)-1]
			slotTag := fmt.Sprintf("key <K%03d>", heldSlot)
			expectedSymbol := "Return"
			if tc.held == "escape" {
				expectedSymbol = "Escape"
			}
			if tc.held == "é" {
				expectedSymbol = "U00E9"
			}
			if !strings.Contains(last, slotTag+" { [ "+expectedSymbol+" ] }") {
				t.Fatalf("held %q was remapped before release: original slot=%d, keymap entry missing: %s", tc.held, heldSlot, last)
			}
		})
	}
}

func TestVirtualKeyboardRetainsTwoHeldKeysAcrossRepeatedKeymapUploads(t *testing.T) {
	k, _ := newTestKeyboard()
	wire := &keyboardKeymapWire{}
	k.ctx = wire
	if err := k.sendkeys(context.Background(), []keySend{{key: "enter", down: true}, {key: "escape", down: true}, {key: "é", down: true}}); err != nil {
		t.Fatal(err)
	}
	slots := map[string]uint32{}
	for key, slot := range k.held {
		slots[key] = slot
	}
	if len(slots) != 3 {
		t.Fatalf("expected three independently held keys, got %v", slots)
	}
	initial := len(wire.msgs)
	if err := k.sendkeys(context.Background(), []keySend{{text: "Ω"}, {key: "enter", up: true}}); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs[initial:])
	if len(events) != 3 || events[0].keycode == slots["enter"] || events[0].keycode == slots["escape"] || events[0].keycode == slots["é"] || events[2].keycode != slots["enter"] || events[2].state != 0 {
		t.Fatalf("interleaved Unicode reassigned held key slot: %+v held=%v", events, slots)
	}
	if _, ok := k.held["enter"]; ok {
		t.Fatal("released Enter still marked held")
	}
	if k.held["escape"] != slots["escape"] || k.held["é"] != slots["é"] {
		t.Fatalf("remaining held keys remapped: %v", k.held)
	}
	second := len(wire.msgs)
	if err := k.sendkeys(context.Background(), []keySend{{key: "escape", up: true}, {key: "é", up: true}}); err != nil {
		t.Fatal(err)
	}
	events = keyboardEventsFromMessages(t, wire.msgs[second:])
	if len(events) != 2 || events[0].keycode != slots["escape"] || events[1].keycode != slots["é"] || events[0].state != 0 || events[1].state != 0 || len(k.held) != 0 {
		t.Fatalf("remaining held keys not released at original slots: events=%+v held=%v", events, k.held)
	}
}

func TestVirtualKeyboardCapacityFailureWithHeldSlotNeverWritesOrChangesState(t *testing.T) {
	k, _ := newTestKeyboard()
	wire := &keyboardKeymapWire{}
	k.ctx = wire
	if err := k.sendkeys(context.Background(), []keySend{{key: "enter", down: true}}); err != nil {
		t.Fatal(err)
	}
	slot := k.held["enter"]
	before := len(wire.msgs)
	var input strings.Builder
	for i := 0; i < int(maxDynamicKeySlots); i++ {
		input.WriteRune(rune(0x1000 + i))
	}
	err := k.sendkeys(context.Background(), []keySend{{text: input.String()}})
	if err == nil || !strings.Contains(err.Error(), "dynamic slots") {
		t.Fatalf("full dynamic slot allocation overwrote held key: %v", err)
	}
	if len(wire.msgs) != before || k.held["enter"] != slot {
		t.Fatalf("capacity rejection mutated compositor or held key: messages=%d before=%d held=%v", len(wire.msgs), before, k.held)
	}
}

func TestVirtualKeyboardStaticHeldNamedKeySurvivesDynamicSequence(t *testing.T) {
	k, _ := newTestKeyboard()
	wire := &keyboardKeymapWire{}
	k.ctx = wire
	if err := k.pressKey(context.Background(), "return"); err != nil {
		t.Fatal(err)
	}
	if k.held["return"] != kcReturn {
		t.Fatalf("static pressed key uses wrong slot: %v", k.held)
	}
	before := len(wire.msgs)
	if err := k.sendkeys(context.Background(), []keySend{{text: "é"}, {key: "return", up: true}}); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs[before:])
	if len(events) != 3 || events[2].state != 0 || events[2].keycode != kcReturn || len(k.held) != 0 {
		t.Fatalf("static key moved during keymap update: events=%+v held=%v", events, k.held)
	}
	last := wire.maps[len(wire.maps)-1]
	if !strings.Contains(last, "key <K012> { [ Return ] }") {
		t.Fatalf("static named key was omitted from later keymap: %s", last)
	}
}

func TestVirtualKeyboardDirectKeyDownDoesNotRemapAnotherHeldKey(t *testing.T) {
	k, _ := newTestKeyboard()
	wire := &keyboardKeymapWire{}
	k.ctx = wire
	if err := k.pressKey(context.Background(), "é"); err != nil {
		t.Fatal(err)
	}
	heldSlot := k.held["é"]
	before := len(wire.msgs)
	if err := k.pressKey(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs[before:])
	if len(events) != 1 || events[0].state != 1 || events[0].keycode == heldSlot {
		t.Fatalf("second KeyDown reused active physical keycode: held slot=%d events=%+v", heldSlot, events)
	}
	if k.held["é"] != heldSlot || k.held["x"] == heldSlot {
		t.Fatalf("held keys alias after independent KeyDown calls: %v", k.held)
	}
	if err := k.releaseKey(context.Background(), "é"); err != nil {
		t.Fatal(err)
	}
	if err := k.releaseKey(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if len(k.held) != 0 {
		t.Fatalf("releases left held keys: %v", k.held)
	}
}

func TestVirtualKeyboardDirectKeyUpUsesOriginalSendkeysHeldSlot(t *testing.T) {
	k, _ := newTestKeyboard()
	wire := &keyboardKeymapWire{}
	k.ctx = wire
	if err := k.sendkeys(context.Background(), []keySend{{key: "enter", down: true}}); err != nil {
		t.Fatal(err)
	}
	heldSlot := k.held["enter"]
	before := len(wire.msgs)
	if err := k.releaseKey(context.Background(), "enter"); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs[before:])
	if len(events) != 1 || events[0].keycode != heldSlot || events[0].state != 0 {
		t.Fatalf("KeyUp released a different XKB code: held=%d events=%+v", heldSlot, events)
	}
}

func TestVirtualKeyboardDirectKeyUpRestoresHeldModifierAndUnrelatedKey(t *testing.T) {
	k, _ := newTestKeyboard()
	wire := &keyboardKeymapWire{}
	k.ctx = wire
	if err := k.pressKey(context.Background(), "ctrl"); err != nil {
		t.Fatal(err)
	}
	if err := k.pressKey(context.Background(), "é"); err != nil {
		t.Fatal(err)
	}
	if err := k.pressKey(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	xSlot := k.held["x"]
	initial := len(wire.msgs)
	if err := k.releaseKey(context.Background(), "é"); err != nil {
		t.Fatal(err)
	}
	events := keyboardEventsFromMessages(t, wire.msgs[initial:])
	if len(events) < 2 || events[0].opcode != 2 || events[0].state != modControl {
		t.Fatalf("key release did not preserve caller-held control modifier: %+v", events)
	}
	if !strings.Contains(wire.maps[len(wire.maps)-1], fmt.Sprintf("key <K%03d>", xSlot)) {
		t.Fatalf("unrelated held key remapped during release: held=%d map=%s", xSlot, wire.maps[len(wire.maps)-1])
	}
	if err := k.releaseKey(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if err := k.releaseKey(context.Background(), "ctrl"); err != nil {
		t.Fatal(err)
	}
	if len(k.held) != 0 || k.mods != 0 {
		t.Fatalf("release leaked held state: %v mods=%d", k.held, k.mods)
	}
}
