package input

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/internal/wl"
	"strings"
	"testing"
)

// Repeated direct KeyDown must not put a second physical press on the wire.
// In particular, modifiers live in k.mods rather than k.held and previously
// bypassed sendkeys' held-key ownership preflight entirely.
func TestVirtualKeyboardDirectDuplicateKeyDownDoesNotDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		mods      uint32
		held      map[string]uint32
	}{
		{name: "held ctrl", key: "control", mods: modControl},
		{name: "held shift", key: "shift_l", mods: modShift},
		{name: "held named", key: "return", held: map[string]uint32{"enter": kcReturn}},
		{name: "held literal", key: "q", held: map[string]uint32{"q": kcDynBase}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			k.mods = tc.mods
			for key, slot := range tc.held {
				k.held[key] = slot
			}
			err := k.pressKey(context.Background(), tc.key)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("duplicate KeyDown(%q) err=%v; want already held", tc.key, err)
			}
			if len(wire.msgs) != 0 || k.mods != tc.mods || len(k.held) != len(tc.held) {
				t.Fatalf("duplicate press affected state or wire: msgs=%d mods=%d held=%v", len(wire.msgs), k.mods, k.held)
			}
		})
	}
}

// An empty character set must not synthesize dynamic slots, whose names
// could conflict with modifiers or permit an empty batch to alter the keymap.
func TestVirtualKeyboardEmptyRuneMapUsesOnlyModifierKeymap(t *testing.T) {
	expected := xkbModsOnly()
	actual := xkbWithRunes(nil)
	if actual != expected {
		t.Fatalf("nil rune set introduced dynamic keys into keymap")
	}
	actual = xkbWithRunes([]rune{})
	if actual != expected {
		t.Fatal("empty rune set introduced dynamic keys into keymap")
	}
	if strings.Contains(actual, "<K0") || strings.Contains(actual, "U0000") {
		t.Fatal("empty keymap allocated a dynamic character slot")
	}
}

// An operation canceled before direct tap admission must not send a press or
// rely on cleanup to undo a key event that was never authorized.
func TestVirtualKeyboardDirectTapPreCanceledBeforeWire(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	k, wire := newTestKeyboard()
	if err := k.tap(ctx, kcDynBase); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled tap returned %v", err)
	}
	if wire.writes != 0 || len(wire.msgs) != 0 {
		t.Fatalf("canceled tap sent %d compositor messages", wire.writes)
	}
	if k.mods != 0 || len(k.held) != 0 {
		t.Fatalf("canceled tap mutated input ownership: mods=%d held=%v", k.mods, k.held)
	}
}

// Direct KeyDown must never translate malformed UTF-8 into a real replacement
// character press: the caller did not authorize that physical key event.
func TestVirtualKeyboardDirectKeyDownRejectsInvalidUTF8BeforeWire(t *testing.T) {
	for i, raw := range []string{string([]byte{0xff}), string([]byte{'a', 0xff})} {
		t.Run(fmt.Sprintf("invalid-utf8-%d", i), func(t *testing.T) {
			k, wire := newTestKeyboard()
			err := k.pressKey(context.Background(), raw)
			if err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
				t.Fatalf("invalid direct key accepted: %v", err)
			}
			if len(wire.msgs) != 0 || len(k.held) != 0 || k.mods != 0 {
				t.Fatalf("invalid key emitted compositor events: %d held=%v mods=%d", len(wire.msgs), k.held, k.mods)
			}
		})
	}
}

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

// Unknown multi-character key tokens execute as individual taps. A held key
// within that token must be rejected before any keymap or key wire message.
func TestVirtualKeyboardMultiCharacterTokenNeverTapsHeldRune(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []keySend
	}{
		{"held at end", []keySend{{key: "ba"}}},
		{"held at start", []keySend{{key: "ab"}}},
		{"held after valid prefix", []keySend{{text: "ok"}, {key: "ba"}}},
		{"held earlier in batch", []keySend{{key: "a", down: true}, {key: "ba"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			if tc.name != "held earlier in batch" {
				k.held["a"] = kcDynBase
			}
			err := k.sendkeys(context.Background(), tc.actions)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("held rune allowed within multi-character token: %v", err)
			}
			if len(wire.msgs) != 0 || len(k.held) != (map[bool]int{true: 0, false: 1}[tc.name == "held earlier in batch"]) {
				t.Fatalf("invalid batch crossed wire or altered holds: msgs=%d held=%v", len(wire.msgs), k.held)
			}
		})
	}
}

// Named aliases such as enter/return refer to the same physical key. A tap
// or second hold via an alias must not release or duplicate a caller's hold.
func TestVirtualKeyboardNamedAliasesCannotBypassHeldOwnership(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []keySend
	}{
		{"tap alias", []keySend{{key: "return"}}},
		{"hold alias", []keySend{{key: "return", down: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			k.held = map[string]uint32{"enter": kcReturn}
			err := k.sendkeys(context.Background(), tc.actions)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("alias crossed held-key ownership: err=%v", err)
			}
			if len(wire.msgs) != 0 {
				t.Fatalf("alias emitted %d compositor messages", len(wire.msgs))
			}
			if len(k.held) != 1 || k.held["enter"] != kcReturn {
				t.Fatalf("existing hold was changed: %v", k.held)
			}
		})
	}
}

// Releasing a held key through its alias is valid; the original hold must
// be removed rather than treated as an unrelated key.
func TestVirtualKeyboardAliasReleaseMatchesOriginalHold(t *testing.T) {
	k, wire := newTestKeyboard()
	k.held = map[string]uint32{"enter": kcReturn}
	if err := k.sendkeys(context.Background(), []keySend{{key: "return", up: true}}); err != nil {
		t.Fatalf("alias release failed: %v", err)
	}
	if len(k.held) != 0 {
		t.Fatalf("alias release left key depressed: %v", k.held)
	}
	if len(wire.msgs) == 0 {
		t.Fatal("alias release emitted no compositor events")
	}
}

func TestVirtualKeyboardInsertIsOneNamedKeyNotSixLetters(t *testing.T) {
	k, wire := newTestKeyboard()
	if err := k.sendkeys(context.Background(), []keySend{{key: "insert"}}); err != nil {
		t.Fatalf("insert tap: %v", err)
	}
	events := 0
	for _, msg := range wire.msgs {
		if len(msg) >= 20 && wl.Uint32(msg[4:8])&0xffff == 1 {
			events++
		}
	}
	if events != 2 {
		t.Fatalf("insert emitted %d key events, want one press and release", events)
	}
}

func TestVirtualKeyboardInsertCanBeHeldAndReleased(t *testing.T) {
	k, wire := newTestKeyboard()
	if err := k.sendkeys(context.Background(), []keySend{{key: "insert", down: true}}); err != nil {
		t.Fatalf("hold insert: %v", err)
	}
	if len(k.held) != 1 {
		t.Fatalf("insert hold missing: %v", k.held)
	}
	prior := len(wire.msgs)
	if err := k.sendkeys(context.Background(), []keySend{{key: "insert"}}); err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("held Insert accepted tap: %v", err)
	}
	if len(wire.msgs) != prior {
		t.Fatalf("invalid insert tap sent %d events", len(wire.msgs)-prior)
	}
	if err := k.sendkeys(context.Background(), []keySend{{key: "insert", up: true}}); err != nil {
		t.Fatalf("release insert: %v", err)
	}
	if len(k.held) != 0 {
		t.Fatalf("insert remained held: %v", k.held)
	}
}

// The public key syntax accepts {space}. It must represent one physical
// Space key, not type the five letters of the key's name.
func TestVirtualKeyboardNamedSpaceEmitsOneKeyTap(t *testing.T) {
	k, wire := newTestKeyboard()
	if err := k.sendkeys(context.Background(), []keySend{{key: "space"}}); err != nil {
		t.Fatalf("named space tap: %v", err)
	}
	keyMessages := 0
	for _, msg := range wire.msgs {
		if len(msg) >= 8 && uint16(msg[4]) == 1 {
			keyMessages++
		}
	}
	if keyMessages != 2 {
		t.Fatalf("named space emitted %d key transitions, want one down and one up", keyMessages)
	}
}

func TestVirtualKeyboardNamedSpaceHoldAndRelease(t *testing.T) {
	k, _ := newTestKeyboard()
	if err := k.sendkeys(context.Background(), []keySend{{key: "space", down: true}}); err != nil {
		t.Fatalf("hold space: %v", err)
	}
	if len(k.held) != 1 {
		t.Fatalf("space hold was not recorded: %v", k.held)
	}
	if err := k.sendkeys(context.Background(), []keySend{{key: "space", up: true}}); err != nil {
		t.Fatalf("release space: %v", err)
	}
	if len(k.held) != 0 {
		t.Fatalf("space key remains held: %v", k.held)
	}
}

func TestVirtualKeyboardNamedSpaceSharesLiteralHeldOwnership(t *testing.T) {
	for _, tc := range []struct {
		name    string
		held    string
		attempt string
	}{
		{"literal held named tap", " ", "space"},
		{"named held literal tap", "space", " "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			if err := k.pressKey(context.Background(), tc.held); err != nil {
				t.Fatalf("hold %q: %v", tc.held, err)
			}
			prior := len(wire.msgs)
			err := k.sendkeys(context.Background(), []keySend{{key: tc.attempt}})
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("tap %q while held %q: %v", tc.attempt, tc.held, err)
			}
			if len(wire.msgs) != prior {
				t.Fatalf("held space tap emitted %d wire messages", len(wire.msgs)-prior)
			}
			if err := k.releaseKey(context.Background(), "space"); err != nil {
				t.Fatalf("release named space: %v", err)
			}
			if len(k.held) != 0 {
				t.Fatalf("held state not cleared: %v", k.held)
			}
		})
	}
}

// A successful virtual-keyboard press must still be released when the caller
// cancels during the protocol's 10ms key-down pacing delay. The compensating
// release uses a bounded independent context; cleanup errors remain visible.
func TestWaylandTapCancelDuringPacingReleasesPressedKey(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failCleanup bool
	}{
		{name: "cleanup succeeds"},
		{name: "cleanup fails", failCleanup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cleanupErr := errors.New("synthetic key release transport failure")
			wire := &uncertainDirectKeyPressWire{cancel: cancel, cleanupErr: cleanupErr, failCleanup: tc.failCleanup}
			k, _ := newTestKeyboard()
			k.ctx = wire
			err := k.tap(ctx, kcDynBase)
			if !errors.Is(err, context.Canceled) || errors.Is(err, cleanupErr) != tc.failCleanup {
				t.Fatalf("canceled tap lost cancellation or cleanup failure: %v", err)
			}
			events := keyboardEventsFromMessages(t, wire.msgs)
			if len(events) != 2 || events[0].opcode != 1 || events[0].keycode != kcDynBase || events[0].state != 1 || events[1].opcode != 1 || events[1].keycode != kcDynBase || events[1].state != 0 {
				t.Fatalf("cancelled pacing left key held in compositor: %+v", events)
			}
			if k.mods != 0 || len(k.held) != 0 {
				t.Fatalf("cancelled pacing retained ownership: mods=%d held=%v", k.mods, k.held)
			}
		})
	}
}

func TestXkbKeysym(t *testing.T) {
	tests := []struct {
		r    rune
		want string
	}{
		{'A', "A"},
		{'z', "z"},
		{'0', "0"},
		{' ', "space"},
		{'€', "U20AC"},
	}
	for _, tc := range tests {
		got := xkbKeysym(tc.r)
		if got != tc.want {
			t.Errorf("xkbKeysym(%q) = %q, want %q", tc.r, got, tc.want)
		}
	}
}

func TestNamedKey(t *testing.T) {
	tests := []struct {
		name string
		kc   uint32
		sym  string
		ok   bool
	}{
		{"shift", kcShift, "Shift_L", true},
		{"Ctrl", kcCtrl, "Control_L", true},
		{"RETURN", kcReturn, "Return", true},
		{"enter", kcReturn, "Return", true},
		{"f1", kcF1, "F1", true},
		{"f12", kcF1 + 11, "F12", true},
		{"nonexistent", 0, "", false},
	}
	for _, tc := range tests {
		kc, sym, ok := namedKey(tc.name)
		if ok != tc.ok || kc != tc.kc || sym != tc.sym {
			t.Errorf("namedKey(%q) = (%d, %q, %v), want (%d, %q, %v)",
				tc.name, kc, sym, ok, tc.kc, tc.sym, tc.ok)
		}
	}
}

func TestModBit(t *testing.T) {
	tests := []struct {
		key  string
		want uint32
	}{
		{"shift", modShift},
		{"ctrl", modControl},
		{"alt", modMod1},
		{"super", modMod4},
		{"f1", 0},
	}
	for _, tc := range tests {
		got := modBit(tc.key)
		if got != tc.want {
			t.Errorf("modBit(%q) = %d, want %d", tc.key, got, tc.want)
		}
	}
}

func TestUniqueRunes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "helo"},
		{"aabbcc", "abc"},
		{"", ""},
		{"abcabc", "abc"},
		{"日本日", "日本"},
	}
	for _, tc := range tests {
		got := uniqueRunes(tc.input)
		gotStr := string(got)
		if gotStr != tc.want {
			t.Errorf("uniqueRunes(%q) = %q, want %q", tc.input, gotStr, tc.want)
		}
	}
}

func TestXkbBuildContainsModifiers(t *testing.T) {
	km := xkbModsOnly()
	if !strings.Contains(km, "Shift_L") {
		t.Error("keymap missing Shift_L")
	}
	if !strings.Contains(km, "Control_L") {
		t.Error("keymap missing Control_L")
	}
	if !strings.Contains(km, "Alt_L") {
		t.Error("keymap missing Alt_L")
	}
	if !strings.Contains(km, "Super_L") {
		t.Error("keymap missing Super_L")
	}
}

func TestXkbWithRunes(t *testing.T) {
	km := xkbWithRunes([]rune{'A', 'b'})
	if !strings.Contains(km, "[ A ]") {
		t.Error("keymap missing standard A keysym")
	}
	if !strings.Contains(km, "[ b ]") {
		t.Error("keymap missing standard b keysym")
	}
}

func TestXkbWithNamed(t *testing.T) {
	km := xkbWithNamed(kcReturn, "Return")
	if !strings.Contains(km, "Return") {
		t.Error("keymap missing Return")
	}
	if !strings.Contains(km, "KNAM") {
		t.Error("keymap missing KNAM keycode")
	}

	// Modifier keycodes should just return mods-only.
	km2 := xkbWithNamed(kcShift, "Shift_L")
	if strings.Contains(km2, "KNAM") {
		t.Error("modifier keycode should not add KNAM")
	}
}

// --- sendkeys tests use recordingCtx from wlkeyboard_keyevents_test.go ---

func newSendkeysTestKeyboard() (*wlKeyboard, *recordingCtx) {
	k := &wlKeyboard{held: make(map[string]uint32)}
	rp := &wl.RawProxy{}
	rp.SetID(42)
	k.kbd = rp
	f := &recordingCtx{}
	k.ctx = f
	return k, f
}

// --- new tests for keymap caching ---

// fakeCtx implements wl.Ctx for testing WriteMsg calls.
type fakeCtx struct {
	writes   int
	lastData []byte
}

func (f *fakeCtx) Register(p wl.Proxy)            {}
func (f *fakeCtx) SetProxy(id uint32, p wl.Proxy) {}
func (f *fakeCtx) Unregister(p wl.Proxy)          {}
func (f *fakeCtx) WriteMsg(data, oob []byte) error {
	f.writes++
	f.lastData = append([]byte{}, data...)
	return nil
}
func (f *fakeCtx) Dispatch() error { return nil }
func (f *fakeCtx) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return f.WriteMsg(data, oob)
}
func (f *fakeCtx) DispatchContext(ctx context.Context) error { return ctx.Err() }
func (f *fakeCtx) WithOperationContext(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}
func (f *fakeCtx) Close() error { return nil }

func TestUploadKeymap_Caching(t *testing.T) {
	k := &wlKeyboard{held: make(map[string]uint32)}
	// prepare a raw proxy with a fixed id
	rp := &wl.RawProxy{}
	rp.SetID(123)
	k.kbd = rp
	f := &fakeCtx{}
	k.ctx = f

	text := "xkb_keymap { }"
	if err := k.uploadKeymap(context.Background(), text); err != nil {
		t.Fatalf("upload1 error: %v", err)
	}
	if f.writes != 1 {
		t.Fatalf("expected 1 write, got %d", f.writes)
	}
	// second call should be a no-op
	if err := k.uploadKeymap(context.Background(), text); err != nil {
		t.Fatalf("upload2 error: %v", err)
	}
	if f.writes != 1 {
		t.Fatalf("expected still 1 write, got %d", f.writes)
	}

	// different text should cause another write
	if err := k.uploadKeymap(context.Background(), text+"x"); err != nil {
		t.Fatalf("upload3 error: %v", err)
	}
	if f.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", f.writes)
	}
}

func TestSendkeys_Empty(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	if err := k.sendkeys(context.Background(), nil); err != nil {
		t.Fatalf("sendkeys(nil): %v", err)
	}
	if rc.writes != 0 {
		t.Fatalf("expected 0 writes, got %d", rc.writes)
	}
	if err := k.sendkeys(context.Background(), []keySend{}); err != nil {
		t.Fatalf("sendkeys([]): %v", err)
	}
	if rc.writes != 0 {
		t.Fatalf("expected 0 writes, got %d", rc.writes)
	}
}

func TestSendkeys_PlainText(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	actions := []keySend{{text: "ab"}}
	if err := k.sendkeys(context.Background(), actions); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}
	// 1 keymap upload + 4 key events (2 chars × press+release) = 5 writes
	if rc.writes != 5 {
		t.Fatalf("expected 5 writes for plain text 'ab', got %d", rc.writes)
	}
	// First message should be keymap upload (opcode 0)
	opcode0 := wl.Uint32(rc.msgs[0][4:8]) & 0xffff
	if opcode0 != 0 {
		t.Errorf("first msg opcode = %d, want 0 (keymap)", opcode0)
	}
	// Remaining should be key events (opcode 1)
	for i := 1; i < 5; i++ {
		opcode := wl.Uint32(rc.msgs[i][4:8]) & 0xffff
		if opcode != 1 {
			t.Errorf("msg[%d] opcode = %d, want 1 (key)", i, opcode)
		}
	}
}

func TestSendkeys_ComboOnly(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	actions := []keySend{
		{key: "enter", modifiers: modifiers{}},
	}
	if err := k.sendkeys(context.Background(), actions); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}
	// 1 keymap upload + 2 key events (press+release of enter) = 3 writes
	if rc.writes != 3 {
		t.Fatalf("expected 3 writes for enter tap, got %d", rc.writes)
	}
}

func TestSendkeys_ExplicitModifierReleaseOnlySendsKeyUp(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	actions, err := parseKeySend("{ctrl down}{ctrl up}")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if err := k.sendkeys(context.Background(), actions); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}
	if rc.writes != 5 {
		t.Fatalf("expected 5 writes for ctrl hold/release, got %d", rc.writes)
	}
	if state := wl.Uint32(rc.msgs[1][16:20]); state != 1 {
		t.Fatalf("press state = %d, want 1", state)
	}
	if mods := wl.Uint32(rc.msgs[2][8:12]); mods != modControl {
		t.Fatalf("mods after ctrl down = 0x%x, want 0x%x", mods, modControl)
	}
	if state := wl.Uint32(rc.msgs[3][16:20]); state != 0 {
		t.Fatalf("release state = %d, want 0", state)
	}
	if mods := wl.Uint32(rc.msgs[4][8:12]); mods != 0 {
		t.Fatalf("mods after ctrl up = 0x%x, want 0", mods)
	}
}

func TestSendkeys_TextBeforeCombo_OrderedCorrectly(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	// "ab{enter}" — text before combo. The text must be typed BEFORE the enter key.
	actions := []keySend{
		{text: "ab"},
		{key: "enter"},
	}
	if err := k.sendkeys(context.Background(), actions); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}
	// 1 keymap upload + 4 key events for "ab" + 2 key events for enter = 7 writes
	if rc.writes != 7 {
		t.Fatalf("expected 7 writes, got %d", rc.writes)
	}
	// Msg 0: keymap upload (opcode 0)
	// Msgs 1-4: key events for 'a' and 'b' (opcode 1)
	// Msgs 5-6: key events for enter press/release (opcode 1)

	// Check that the first key event (msg 1) is for rune slot kcDynBase (first rune 'a')
	// keycode in sendKey is stored as keycode-8
	firstKeycode := wl.Uint32(rc.msgs[1][12:16])
	if firstKeycode != kcDynBase-8 {
		t.Errorf("first char keycode = %d, want %d (kcDynBase-8)", firstKeycode, kcDynBase-8)
	}
}

func TestSendkeys_ComboBeforeText_OrderedCorrectly(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	// "{enter}ab" — combo before text. The enter must be typed BEFORE the text.
	actions := []keySend{
		{key: "enter"},
		{text: "ab"},
	}
	if err := k.sendkeys(context.Background(), actions); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}
	// 1 keymap upload + 2 key events for enter + 4 key events for "ab" = 7 writes
	if rc.writes != 7 {
		t.Fatalf("expected 7 writes, got %d", rc.writes)
	}
	// Msgs 1-2: enter uses the next slot after the 62 stable
	// ASCII letter/digit keycodes.
	enterSlot := kcDynBase + 62 // stable ASCII letters and digits
	enterKeycode := wl.Uint32(rc.msgs[1][12:16])
	if enterKeycode != enterSlot-8 {
		t.Errorf("enter keycode = %d, want %d (enterSlot-8)", enterKeycode, enterSlot-8)
	}
	// Msgs 3-6: text key events (keycode = kcDynBase-8, kcDynBase+1-8)
}

func TestSendkeys_LiteralAndNamedKeysUseDistinctSlots(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	actions := []keySend{
		{key: "x"},
		{key: "enter"},
	}
	if err := k.sendkeys(context.Background(), actions); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}

	// The literal 'x' uses its stable ASCII slot; named 'enter' uses
	// the next dynamic slot. They must not share a keycode.
	for i, want := range []uint32{kcDynBase + 23, kcDynBase + 62} {
		keycode := wl.Uint32(rc.msgs[1+i*2][12:16]) + 8
		if keycode != want {
			t.Errorf("action %d keycode = %d, want %d", i, keycode, want)
		}
	}
}

func TestSendkeys_RejectsTooManyDynamicSlots(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	runes := make([]rune, maxDynamicKeySlots+1)
	for i := range runes {
		runes[i] = rune(0x10000 + i)
	}

	err := k.sendkeys(context.Background(), []keySend{{key: string(runes)}})
	if err == nil {
		t.Fatal("expected error for too many dynamic key slots")
	}
	if rc.writes != 0 {
		t.Fatalf("expected no protocol writes after slot validation, got %d", rc.writes)
	}
}

func TestSendkeys_RejectsNamedKeyWhenDynamicSlotsAreFull(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	runes := make([]rune, maxDynamicKeySlots)
	for i := range runes {
		runes[i] = rune(0x10000 + i)
	}

	err := k.sendkeys(context.Background(), []keySend{
		{key: string(runes)},
		{key: "enter"},
	})
	if err == nil {
		t.Fatal("expected error when a named key exceeds the dynamic slot limit")
	}
	if rc.writes != 0 {
		t.Fatalf("expected no protocol writes after slot validation, got %d", rc.writes)
	}
}

func TestSendkeys_ModifierCombo(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	actions := []keySend{
		{key: "a", modifiers: modifiers{ctrl: true}},
	}
	if err := k.sendkeys(context.Background(), actions); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}
	// Expected writes:
	// 1. keymap upload
	// 2. modifiers message (Ctrl down)
	// 3. key-a press
	// 4. key-a release (after delay)
	// 5. modifiers message (Ctrl up)
	if rc.writes != 5 {
		t.Fatalf("expected 5 writes for ctrl+a, got %d", rc.writes)
	}
	// Msg 1 should be modifiers (opcode 2)
	opcode1 := wl.Uint32(rc.msgs[1][4:8]) & 0xffff
	if opcode1 != 2 {
		t.Errorf("msg[1] opcode = %d, want 2 (modifiers)", opcode1)
	}
	// Check that ctrl modifier bit is set
	mods := wl.Uint32(rc.msgs[1][8:12])
	if mods&modControl == 0 {
		t.Errorf("ctrl modifier bit not set in first modifiers message")
	}
}

func TestSendkeys_MixedTextAndCombo_AllRunesInKeymap(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	// "Hi{enter}World" — text split by a combo
	// The keymap should include all runes: H, i, W, o, r, l, d
	actions := []keySend{
		{text: "Hi"},
		{key: "enter"},
		{text: "World"},
	}
	if err := k.sendkeys(context.Background(), actions); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}
	// Total: 1 keymap + 4 for Hi + 2 for enter + 10 for World = 17
	expectedWrites := 1 + 4 + 2 + 10
	if rc.writes != expectedWrites {
		t.Fatalf("expected %d writes, got %d", expectedWrites, rc.writes)
	}
}

func TestUploadKeymap_Concurrent(t *testing.T) {
	k := &wlKeyboard{held: make(map[string]uint32)}
	rp := &wl.RawProxy{}
	rp.SetID(1)
	k.kbd = rp
	f := &fakeCtx{}
	k.ctx = f

	text := "xkb_keymap { }"
	errCh := make(chan error, 2)
	go func() { errCh <- k.uploadKeymap(context.Background(), text) }()
	go func() { errCh <- k.uploadKeymap(context.Background(), text) }()
	// collect errors
	e1 := <-errCh
	e2 := <-errCh
	if e1 != nil && !errors.Is(e1, nil) {
		t.Fatalf("err1=%v", e1)
	}
	if e2 != nil && !errors.Is(e2, nil) {
		t.Fatalf("err2=%v", e2)
	}
	if f.writes != 1 {
		t.Fatalf("expected 1 write after concurrent uploads, got %d", f.writes)
	}
}

// Regression: a GTK3 app must see the same physical keycode for accelerator
// letters on successive operations, not a changing one-slot-per-command map.
func TestSendkeys_StableAcceleratorKeymap(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	if err := k.sendkeys(context.Background(), []keySend{{text: "abcXYZ123"}}); err != nil {
		t.Fatal(err)
	}
	initialMap := k.lastKeymap
	initialWrites := rc.writes
	for _, key := range []string{"s", "v", "a", "1"} {
		if err := k.sendkeys(context.Background(), []keySend{{key: key, modifiers: modifiers{ctrl: true}}}); err != nil {
			t.Fatalf("Ctrl+%s: %v", key, err)
		}
		if k.lastKeymap != initialMap {
			t.Fatalf("Ctrl+%s unexpectedly replaced the XKB keymap", key)
		}
	}
	// Each accelerator emits only modifiers down/up and key down/up.
	if got, want := rc.writes-initialWrites, 4*4; got != want {
		t.Fatalf("subsequent accelerator protocol writes = %d, want %d (no keymap uploads)", got, want)
	}
}
