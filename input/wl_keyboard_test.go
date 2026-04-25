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
