//go:build linux
// +build linux

package input

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// recordingCtx extends fakeCtx to capture all messages for inspection.
type recordingCtx struct {
	writes   int
	msgs     [][]byte
	lastData []byte
}

func (r *recordingCtx) Register(p wl.Proxy)            {}
func (r *recordingCtx) SetProxy(id uint32, p wl.Proxy) {}
func (r *recordingCtx) WriteMsg(data, oob []byte) error {
	r.writes++
	r.lastData = append([]byte{}, data...)
	r.msgs = append(r.msgs, append([]byte{}, data...))
	return nil
}
func (r *recordingCtx) Dispatch() error { return nil }
func (r *recordingCtx) Close() error    { return nil }

func newTestKeyboard() (*wlKeyboard, *recordingCtx) {
	k := &wlKeyboard{held: make(map[string]uint32)}
	rp := &wl.RawProxy{}
	rp.SetID(42)
	k.kbd = rp
	f := &recordingCtx{}
	k.ctx = f
	return k, f
}

func TestSendKey_MessageFormat(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.sendKey(10, 1); err != nil {
		t.Fatalf("sendKey: %v", err)
	}
	if rc.writes != 1 {
		t.Fatalf("expected 1 write, got %d", rc.writes)
	}
	msg := rc.lastData
	// Message: id(4) + size|opcode(4) + time(4) + keycode-8(4) + state(4) = 20 bytes
	if len(msg) != 20 {
		t.Fatalf("message length = %d, want 20", len(msg))
	}
	// Check object ID
	if wl.Uint32(msg[0:4]) != 42 {
		t.Errorf("object ID = %d, want 42", wl.Uint32(msg[0:4]))
	}
	// Check size|opcode: size=20, opcode=1
	sizeOpcode := wl.Uint32(msg[4:8])
	if sizeOpcode>>16 != 20 {
		t.Errorf("size = %d, want 20", sizeOpcode>>16)
	}
	if sizeOpcode&0xffff != 1 {
		t.Errorf("opcode = %d, want 1 (key)", sizeOpcode&0xffff)
	}
	// Check keycode: 10-8 = 2
	if wl.Uint32(msg[12:16]) != 2 {
		t.Errorf("keycode = %d, want 2", wl.Uint32(msg[12:16]))
	}
	// Check state: 1 (pressed)
	if wl.Uint32(msg[16:20]) != 1 {
		t.Errorf("state = %d, want 1", wl.Uint32(msg[16:20]))
	}
}

func TestSendKey_Release(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.sendKey(10, 0); err != nil {
		t.Fatalf("sendKey: %v", err)
	}
	msg := rc.lastData
	// state should be 0
	if wl.Uint32(msg[16:20]) != 0 {
		t.Errorf("state = %d, want 0 (release)", wl.Uint32(msg[16:20]))
	}
}

func TestSendModifiers_MessageFormat(t *testing.T) {
	k, rc := newTestKeyboard()
	k.mods = modShift | modControl
	if err := k.sendModifiers(); err != nil {
		t.Fatalf("sendModifiers: %v", err)
	}
	if rc.writes != 1 {
		t.Fatalf("expected 1 write, got %d", rc.writes)
	}
	msg := rc.lastData
	// Message: id(4) + size|opcode(4) + mods_depressed(4) + mods_latched(4) + mods_locked(4) + group(4) = 24 bytes
	if len(msg) != 24 {
		t.Fatalf("message length = %d, want 24", len(msg))
	}
	// Check size|opcode: size=24, opcode=2
	sizeOpcode := wl.Uint32(msg[4:8])
	if sizeOpcode>>16 != 24 {
		t.Errorf("size = %d, want 24", sizeOpcode>>16)
	}
	if sizeOpcode&0xffff != 2 {
		t.Errorf("opcode = %d, want 2 (modifiers)", sizeOpcode&0xffff)
	}
	// Check mods_depressed
	mods := wl.Uint32(msg[8:12])
	if mods != modShift|modControl {
		t.Errorf("mods = 0x%x, want 0x%x", mods, modShift|modControl)
	}
	// latched, locked, group should all be 0
	for i := 12; i < 24; i += 4 {
		if wl.Uint32(msg[i:i+4]) != 0 {
			t.Errorf("field at offset %d = %d, want 0", i, wl.Uint32(msg[i:i+4]))
		}
	}
}

func TestSendModifiers_ZeroMods(t *testing.T) {
	k, rc := newTestKeyboard()
	k.mods = 0
	if err := k.sendModifiers(); err != nil {
		t.Fatalf("sendModifiers: %v", err)
	}
	msg := rc.lastData
	mods := wl.Uint32(msg[8:12])
	if mods != 0 {
		t.Errorf("mods = 0x%x, want 0", mods)
	}
}

func TestTap_PressAndRelease(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.tap(15); err != nil {
		t.Fatalf("tap: %v", err)
	}
	// tap sends key down then key up = 2 messages
	if rc.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", rc.writes)
	}
	// First message: state=1 (press)
	if wl.Uint32(rc.msgs[0][16:20]) != 1 {
		t.Errorf("first event state = %d, want 1 (press)", wl.Uint32(rc.msgs[0][16:20]))
	}
	// Second message: state=0 (release)
	if wl.Uint32(rc.msgs[1][16:20]) != 0 {
		t.Errorf("second event state = %d, want 0 (release)", wl.Uint32(rc.msgs[1][16:20]))
	}
	// Both should have keycode = 15-8 = 7
	for i, msg := range rc.msgs {
		kc := wl.Uint32(msg[12:16])
		if kc != 7 {
			t.Errorf("msg[%d] keycode = %d, want 7", i, kc)
		}
	}
}

func TestUploadKeymapAndRestoreMods_WithMods(t *testing.T) {
	k, rc := newTestKeyboard()
	k.mods = modShift
	if err := k.uploadKeymapAndRestoreMods(xkbModsOnly()); err != nil {
		t.Fatalf("uploadKeymapAndRestoreMods: %v", err)
	}
	// Should have: 1 keymap upload + 1 modifiers message
	if rc.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", rc.writes)
	}
	// Second message should be modifiers
	msg := rc.msgs[1]
	sizeOpcode := wl.Uint32(msg[4:8])
	if sizeOpcode&0xffff != 2 {
		t.Errorf("second msg opcode = %d, want 2 (modifiers)", sizeOpcode&0xffff)
	}
}

func TestUploadKeymapAndRestoreMods_NoMods(t *testing.T) {
	k, rc := newTestKeyboard()
	k.mods = 0
	if err := k.uploadKeymapAndRestoreMods(xkbModsOnly()); err != nil {
		t.Fatalf("uploadKeymapAndRestoreMods: %v", err)
	}
	// Should have only 1 write (keymap upload, no modifiers needed)
	if rc.writes != 1 {
		t.Fatalf("expected 1 write, got %d", rc.writes)
	}
}

func TestTypeString_Empty(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.typeString(""); err != nil {
		t.Fatalf("typeString: %v", err)
	}
	if rc.writes != 0 {
		t.Fatalf("expected 0 writes for empty string, got %d", rc.writes)
	}
}

func TestTypeString_SingleChar(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.typeString("a"); err != nil {
		t.Fatalf("typeString: %v", err)
	}
	// 1 keymap upload + 2 key events (press+release) = 3 writes
	if rc.writes != 3 {
		t.Fatalf("expected 3 writes, got %d", rc.writes)
	}
	// First message should be keymap upload (opcode 0)
	opcode0 := wl.Uint32(rc.msgs[0][4:8]) & 0xffff
	if opcode0 != 0 {
		t.Errorf("first msg opcode = %d, want 0 (keymap)", opcode0)
	}
	// Second and third should be key events (opcode 1)
	for i := 1; i <= 2; i++ {
		opcode := wl.Uint32(rc.msgs[i][4:8]) & 0xffff
		if opcode != 1 {
			t.Errorf("msg[%d] opcode = %d, want 1 (key)", i, opcode)
		}
	}
}

func TestTypeString_MultipleChars(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.typeString("ab"); err != nil {
		t.Fatalf("typeString: %v", err)
	}
	// 1 keymap upload + 4 key events (2 chars × press+release) = 5 writes
	if rc.writes != 5 {
		t.Fatalf("expected 5 writes, got %d", rc.writes)
	}
}

func TestTypeString_RepeatedChar(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.typeString("aa"); err != nil {
		t.Fatalf("typeString: %v", err)
	}
	// 1 keymap upload + 4 key events = 5 writes
	if rc.writes != 5 {
		t.Fatalf("expected 5 writes, got %d", rc.writes)
	}
}

func TestTapKey_NamedKey(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.tapKey("return"); err != nil {
		t.Fatalf("tapKey: %v", err)
	}
	// 1 keymap upload + 2 key events = 3 writes
	if rc.writes != 3 {
		t.Fatalf("expected 3 writes, got %d", rc.writes)
	}
	// Verify the keycode in the key events is kcReturn-8 = 4
	for i := 1; i <= 2; i++ {
		kc := wl.Uint32(rc.msgs[i][12:16])
		if kc != kcReturn-8 {
			t.Errorf("msg[%d] keycode = %d, want %d", i, kc, kcReturn-8)
		}
	}
}

func TestTapKey_SingleChar(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.tapKey("x"); err != nil {
		t.Fatalf("tapKey: %v", err)
	}
	// 1 keymap upload + 2 key events = 3 writes
	if rc.writes != 3 {
		t.Fatalf("expected 3 writes, got %d", rc.writes)
	}
}

func TestPressKey_Modifier(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.pressKey("shift"); err != nil {
		t.Fatalf("pressKey: %v", err)
	}
	// 1 keymap upload + 1 key press + 1 modifiers = 3 writes
	if rc.writes != 3 {
		t.Fatalf("expected 3 writes, got %d", rc.writes)
	}
	// Verify shift is now in mods
	if k.mods&modShift == 0 {
		t.Error("shift modifier not set")
	}
}

func TestPressKey_NamedNonModifier(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.pressKey("return"); err != nil {
		t.Fatalf("pressKey: %v", err)
	}
	// 1 keymap upload + 1 key press = 2 writes (no modifiers message)
	if rc.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", rc.writes)
	}
	// Key should be in held map
	if _, ok := k.held["return"]; !ok {
		t.Error("return key not in held map")
	}
}

func TestPressKey_SingleChar(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.pressKey("x"); err != nil {
		t.Fatalf("pressKey: %v", err)
	}
	// 1 keymap upload + 1 key press = 2 writes
	if rc.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", rc.writes)
	}
	if _, ok := k.held["x"]; !ok {
		t.Error("x key not in held map")
	}
}

func TestPressKey_MultiCharError(t *testing.T) {
	k, _ := newTestKeyboard()
	err := k.pressKey("abc")
	if err == nil {
		t.Fatal("expected error for multi-character key")
	}
	t.Logf("got expected error: %v", err)
}

func TestReleaseKey_Modifier(t *testing.T) {
	k, rc := newTestKeyboard()
	// Press then release shift
	if err := k.pressKey("shift"); err != nil {
		t.Fatalf("pressKey: %v", err)
	}
	rc.writes = 0 // reset
	rc.msgs = nil

	if err := k.releaseKey("shift"); err != nil {
		t.Fatalf("releaseKey: %v", err)
	}
	// 1 key release + 1 modifiers = 2 writes
	if rc.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", rc.writes)
	}
	// Verify shift is cleared from mods
	if k.mods&modShift != 0 {
		t.Error("shift modifier still set after release")
	}
}

func TestReleaseKey_NamedNonModifier(t *testing.T) {
	k, rc := newTestKeyboard()
	if err := k.pressKey("return"); err != nil {
		t.Fatalf("pressKey: %v", err)
	}
	rc.writes = 0
	rc.msgs = nil

	if err := k.releaseKey("return"); err != nil {
		t.Fatalf("releaseKey: %v", err)
	}
	// releaseKey calls uploadKeymap which may be cached (same keymap as pressKey),
	// so we expect 1 key release write (keymap upload is cached/skipped).
	if rc.writes != 1 {
		t.Fatalf("expected 1 write, got %d", rc.writes)
	}
	// Key should be removed from held map
	if _, ok := k.held["return"]; ok {
		t.Error("return key still in held map after release")
	}
}

func TestReleaseKey_NotHeld(t *testing.T) {
	k, rc := newTestKeyboard()
	// Release a key that was never pressed — should return an error
	err := k.releaseKey("x")
	if err == nil {
		t.Fatal("expected error for releasing non-held key")
	}
	t.Logf("got expected error: %v", err)
	if rc.writes != 0 {
		t.Fatalf("expected 0 writes for unreleased key, got %d", rc.writes)
	}
}

func TestReleaseKey_NamedNotHeld(t *testing.T) {
	k, rc := newTestKeyboard()
	// Release a named non-modifier key that was never pressed — should return an error
	err := k.releaseKey("return")
	if err == nil {
		t.Fatal("expected error for releasing non-held named key")
	}
	t.Logf("got expected error: %v", err)
	if rc.writes != 0 {
		t.Fatalf("expected 0 writes for unreleased key, got %d", rc.writes)
	}
}

func TestReleaseKey_ModifierNotHeld(t *testing.T) {
	k, rc := newTestKeyboard()
	// Release shift without pressing — should still work (sends key up + modifiers)
	if err := k.releaseKey("shift"); err != nil {
		t.Fatalf("releaseKey: %v", err)
	}
	// Modifier keycodes (8-11) don't need keymap re-upload, so:
	// 1 key release + 1 modifiers = 2 writes
	if rc.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", rc.writes)
	}
}

func TestTypeString_TooManyUniqueRunes(t *testing.T) {
	k, _ := newTestKeyboard()
	// Create a string with more unique runes than maxDynSlots (219)
	runes := make([]rune, maxDynSlots+1)
	for i := range runes {
		runes[i] = rune(0x10000 + i) // use high Unicode to avoid collisions
	}
	err := k.typeString(string(runes))
	if err == nil {
		t.Fatal("expected error for too many unique runes")
	}
	t.Logf("got expected error: %v", err)
}

func TestTypeString_ExactMaxSlots(t *testing.T) {
	k, _ := newTestKeyboard()
	// Create a string with exactly maxDynSlots unique runes
	runes := make([]rune, maxDynSlots)
	for i := range runes {
		runes[i] = rune(0x10000 + i)
	}
	err := k.typeString(string(runes))
	if err != nil {
		t.Fatalf("typeString with max slots should succeed: %v", err)
	}
}

func TestUploadKeymap_MessageFormat(t *testing.T) {
	k, rc := newTestKeyboard()
	text := "xkb_keymap { }"
	if err := k.uploadKeymap(text); err != nil {
		t.Fatalf("uploadKeymap: %v", err)
	}
	msg := rc.lastData
	// Message: id(4) + size|opcode(4) + format(4) + size(4) = 16 bytes
	if len(msg) != 16 {
		t.Fatalf("message length = %d, want 16", len(msg))
	}
	// Check opcode 0 (keymap)
	sizeOpcode := wl.Uint32(msg[4:8])
	if sizeOpcode&0xffff != 0 {
		t.Errorf("opcode = %d, want 0 (keymap)", sizeOpcode&0xffff)
	}
	// Check format = 1 (xkb_format_text_v1)
	if wl.Uint32(msg[8:12]) != xkbFormatTextV1 {
		t.Errorf("format = %d, want %d", wl.Uint32(msg[8:12]), xkbFormatTextV1)
	}
}

func TestXkbModsOnly_ContainsExpectedKeys(t *testing.T) {
	km := xkbModsOnly()
	expected := []string{"Shift_L", "Control_L", "Alt_L", "Super_L"}
	for _, key := range expected {
		if !bytes.Contains([]byte(km), []byte(key)) {
			t.Errorf("xkbModsOnly missing %q", key)
		}
	}
}

func TestXkbWithRunes_Structure(t *testing.T) {
	km := xkbWithRunes([]rune{'A', 'b', '€'})
	if !bytes.Contains([]byte(km), []byte("[ A ]")) {
		t.Error("missing A keysym")
	}
	if !bytes.Contains([]byte(km), []byte("[ b ]")) {
		t.Error("missing b keysym")
	}
	if !bytes.Contains([]byte(km), []byte("U20AC")) {
		t.Error("missing € keysym")
	}
}

func TestXkbWithNamed_Structure(t *testing.T) {
	km := xkbWithNamed(kcReturn, "Return")
	if !bytes.Contains([]byte(km), []byte("Return")) {
		t.Error("missing Return keysym")
	}
	if !bytes.Contains([]byte(km), []byte("KNAM")) {
		t.Error("missing KNAM keycode")
	}
}

func TestXkbWithNamed_ModifierReturnsModsOnly(t *testing.T) {
	km := xkbWithNamed(kcShift, "Shift_L")
	if bytes.Contains([]byte(km), []byte("KNAM")) {
		t.Error("modifier keycode should not add KNAM")
	}
}

// ── Test-only helpers (moved from wl_keyboard.go to eliminate U1000 warnings) ──

const testMaxDynSlots = kcDynMax - kcDynBase + 1

func testUniqueRunes(s string) []rune {
	seen := make(map[rune]bool)
	var out []rune
	for _, r := range s {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

func (k *wlKeyboard) typeString(s string) error {
	if s == "" {
		return nil
	}
	runes := testUniqueRunes(s)
	if uint32(len(runes)) > testMaxDynSlots {
		return fmt.Errorf("keyboard: string has %d unique characters, max %d per call", len(runes), testMaxDynSlots)
	}
	if err := k.uploadKeymapAndRestoreMods(xkbWithRunes(runes)); err != nil {
		return err
	}
	slot := make(map[rune]uint32, len(runes))
	for i, r := range runes {
		slot[r] = kcDynBase + uint32(i)
	}
	for _, r := range s {
		if err := k.tap(slot[r]); err != nil {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (k *wlKeyboard) tapKey(key string) error {
	if kc, sym, ok := namedKey(key); ok {
		if err := k.uploadKeymapAndRestoreMods(xkbWithNamed(kc, sym)); err != nil {
			return err
		}
		return k.tap(kc)
	}
	return k.typeString(key)
}

// maxDynSlots is exported for tests that reference it by name.
const maxDynSlots = testMaxDynSlots

// uniqueRunes is exported for tests in wl_keyboard_test.go.
func uniqueRunes(s string) []rune {
	return testUniqueRunes(s)
}
