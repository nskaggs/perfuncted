package input

import (
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

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
func (f *fakeCtx) WriteMsg(data, oob []byte) error {
	f.writes++
	f.lastData = append([]byte{}, data...)
	return nil
}
func (f *fakeCtx) Dispatch() error { return nil }
func (f *fakeCtx) Close() error    { return nil }

func TestUploadKeymap_Caching(t *testing.T) {
	k := &wlKeyboard{held: make(map[string]uint32)}
	// prepare a raw proxy with a fixed id
	rp := &wl.RawProxy{}
	rp.SetID(123)
	k.kbd = rp
	f := &fakeCtx{}
	k.ctx = f

	text := "xkb_keymap { }"
	if err := k.uploadKeymap(text); err != nil {
		t.Fatalf("upload1 error: %v", err)
	}
	if f.writes != 1 {
		t.Fatalf("expected 1 write, got %d", f.writes)
	}
	// second call should be a no-op
	if err := k.uploadKeymap(text); err != nil {
		t.Fatalf("upload2 error: %v", err)
	}
	if f.writes != 1 {
		t.Fatalf("expected still 1 write, got %d", f.writes)
	}

	// different text should cause another write
	if err := k.uploadKeymap(text + "x"); err != nil {
		t.Fatalf("upload3 error: %v", err)
	}
	if f.writes != 2 {
		t.Fatalf("expected 2 writes, got %d", f.writes)
	}
}

func TestSendkeys_Empty(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	if err := k.sendkeys(nil); err != nil {
		t.Fatalf("sendkeys(nil): %v", err)
	}
	if rc.writes != 0 {
		t.Fatalf("expected 0 writes, got %d", rc.writes)
	}
	if err := k.sendkeys([]keySend{}); err != nil {
		t.Fatalf("sendkeys([]): %v", err)
	}
	if rc.writes != 0 {
		t.Fatalf("expected 0 writes, got %d", rc.writes)
	}
}

func TestSendkeys_PlainText(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	actions := []keySend{{text: "ab"}}
	if err := k.sendkeys(actions); err != nil {
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
	if err := k.sendkeys(actions); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}
	// 1 keymap upload + 2 key events (press+release of enter) = 3 writes
	if rc.writes != 3 {
		t.Fatalf("expected 3 writes for enter tap, got %d", rc.writes)
	}
}

func TestSendkeys_TextBeforeCombo_OrderedCorrectly(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	// "ab{enter}" — text before combo. The text must be typed BEFORE the enter key.
	actions := []keySend{
		{text: "ab"},
		{key: "enter"},
	}
	if err := k.sendkeys(actions); err != nil {
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
	if err := k.sendkeys(actions); err != nil {
		t.Fatalf("sendkeys: %v", err)
	}
	// 1 keymap upload + 2 key events for enter + 4 key events for "ab" = 7 writes
	if rc.writes != 7 {
		t.Fatalf("expected 7 writes, got %d", rc.writes)
	}
	// Msgs 1-2: enter key events. enter is assigned a dynamic slot by sendkeys:
	//   allRunes = ['a','b'], nextSlot = kcDynBase + 2 = 39
	//   namedSlots["enter"] = 39, so keycode = 39 - 8 = 31
	enterSlot := kcDynBase + 2 // 2 runes in "ab"
	enterKeycode := wl.Uint32(rc.msgs[1][12:16])
	if enterKeycode != enterSlot-8 {
		t.Errorf("enter keycode = %d, want %d (enterSlot-8)", enterKeycode, enterSlot-8)
	}
	// Msgs 3-6: text key events (keycode = kcDynBase-8, kcDynBase+1-8)
}

func TestSendkeys_ModifierCombo(t *testing.T) {
	k, rc := newSendkeysTestKeyboard()
	actions := []keySend{
		{key: "a", modifiers: modifiers{ctrl: true}},
	}
	if err := k.sendkeys(actions); err != nil {
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
	if err := k.sendkeys(actions); err != nil {
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
	go func() { errCh <- k.uploadKeymap(text) }()
	go func() { errCh <- k.uploadKeymap(text) }()
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
