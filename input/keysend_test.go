package input

import (
	"testing"
)

func TestParseKeySend_LiteralText(t *testing.T) {
	sends, err := ParseKeySend("hello world")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if len(sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sends))
	}
	if sends[0].text != "hello world" {
		t.Errorf("text = %q, want %q", sends[0].text, "hello world")
	}
}

func TestParseKeySend_NamedKey(t *testing.T) {
	sends, err := ParseKeySend("{enter}")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if len(sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sends))
	}
	if sends[0].key != "enter" {
		t.Errorf("key = %q, want %q", sends[0].key, "enter")
	}
	if sends[0].down {
		t.Error("expected down=false for plain key tap")
	}
}

func TestParseKeySend_KeyDown(t *testing.T) {
	sends, err := ParseKeySend("{ctrl down}")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if len(sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sends))
	}
	if sends[0].key != "ctrl" {
		t.Errorf("key = %q, want %q", sends[0].key, "ctrl")
	}
	if !sends[0].down {
		t.Error("expected down=true")
	}
}

func TestParseKeySend_KeyUp(t *testing.T) {
	sends, err := ParseKeySend("{shift up}")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if len(sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sends))
	}
	if sends[0].key != "shift" {
		t.Errorf("key = %q, want %q", sends[0].key, "shift")
	}
	if sends[0].down {
		t.Error("expected down=false for key up")
	}
}

func TestParseKeySend_BraceCombo(t *testing.T) {
	sends, err := ParseKeySend("{ctrl+s}")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if len(sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sends))
	}
	if sends[0].key != "s" {
		t.Errorf("key = %q, want %q", sends[0].key, "s")
	}
	if !sends[0].modifiers.ctrl {
		t.Error("expected ctrl modifier")
	}
}

func TestParseKeySend_BraceComboMultipleModifiers(t *testing.T) {
	sends, err := ParseKeySend("{ctrl+shift+left}")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if len(sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sends))
	}
	if sends[0].key != "left" {
		t.Errorf("key = %q, want %q", sends[0].key, "left")
	}
	m := sends[0].modifiers
	if !m.ctrl || !m.shift || m.alt || m.super {
		t.Errorf("expected ctrl+shift only, got %+v", m)
	}
}

func TestParseKeySend_BraceComboSuper(t *testing.T) {
	sends, err := ParseKeySend("{super+tab}")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if len(sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sends))
	}
	if sends[0].key != "tab" {
		t.Errorf("key = %q, want %q", sends[0].key, "tab")
	}
	if !sends[0].modifiers.super {
		t.Error("expected super modifier")
	}
}

func TestParseKeySend_BraceComboMetaAlias(t *testing.T) {
	sends, err := ParseKeySend("{meta+tab}")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if !sends[0].modifiers.super {
		t.Error("expected super modifier for meta alias")
	}
}

func TestParseKeySend_BraceComboAllModifiers(t *testing.T) {
	sends, err := ParseKeySend("{ctrl+alt+shift+super+a}")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if len(sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sends))
	}
	m := sends[0].modifiers
	if !m.ctrl || !m.alt || !m.shift || !m.super {
		t.Errorf("expected all modifiers, got %+v", m)
	}
	if sends[0].key != "a" {
		t.Errorf("key = %q, want %q", sends[0].key, "a")
	}
}

func TestParseKeySend_MixedTextAndKeys(t *testing.T) {
	sends, err := ParseKeySend("hello{enter}world")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if len(sends) != 3 {
		t.Fatalf("expected 3 sends, got %d", len(sends))
	}
	if sends[0].text != "hello" {
		t.Errorf("sends[0].text = %q, want %q", sends[0].text, "hello")
	}
	if sends[1].key != "enter" {
		t.Errorf("sends[1].key = %q, want %q", sends[1].key, "enter")
	}
	if sends[2].text != "world" {
		t.Errorf("sends[2].text = %q, want %q", sends[2].text, "world")
	}
}

func TestParseKeySend_HoldAndRelease(t *testing.T) {
	sends, err := ParseKeySend("{ctrl down}v{ctrl up}")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if len(sends) != 3 {
		t.Fatalf("expected 3 sends, got %d", len(sends))
	}
	if sends[0].key != "ctrl" || !sends[0].down {
		t.Error("expected ctrl down")
	}
	// "v" without braces is literal text
	if sends[1].text != "v" {
		t.Errorf("sends[1].text = %q, want %q", sends[1].text, "v")
	}
	if sends[2].key != "ctrl" || sends[2].down {
		t.Error("expected ctrl up")
	}
}

func TestParseKeySend_UnclosedBrace(t *testing.T) {
	_, err := ParseKeySend("{enter")
	if err == nil {
		t.Fatal("expected error for unclosed brace")
	}
}

func TestParseKeySend_EmptyBraces(t *testing.T) {
	_, err := ParseKeySend("{}")
	if err == nil {
		t.Fatal("expected error for empty braces")
	}
}

func TestParseKeySend_BraceComboEndsWithModifier(t *testing.T) {
	_, err := ParseKeySend("{ctrl+shift}")
	if err == nil {
		t.Fatal("expected error for combo ending with modifier")
	}
}

func TestParseKeySend_CaseInsensitive(t *testing.T) {
	sends, err := ParseKeySend("{ENTER}")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if sends[0].key != "enter" {
		t.Errorf("key = %q, want %q", sends[0].key, "enter")
	}
}

func TestParseKeySend_BraceComboCaseInsensitive(t *testing.T) {
	sends, err := ParseKeySend("{CTRL+SHIFT+T}")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	if sends[0].key != "t" {
		t.Errorf("key = %q, want %q", sends[0].key, "t")
	}
	if !sends[0].modifiers.ctrl || !sends[0].modifiers.shift {
		t.Errorf("expected ctrl+shift, got %+v", sends[0].modifiers)
	}
}

func TestParseKeySend_LiteralBracesInOutput(t *testing.T) {
	// Literal text is never wrapped in braces by ParseKeySend.
	// The braces are stripped during parsing.
	sends, err := ParseKeySend("a{b}c")
	if err != nil {
		t.Fatalf("ParseKeySend: %v", err)
	}
	// "a" is literal, "{b}" is key "b", "c" is literal
	if len(sends) != 3 {
		t.Fatalf("expected 3 sends, got %d", len(sends))
	}
	if sends[0].text != "a" {
		t.Errorf("sends[0].text = %q, want %q", sends[0].text, "a")
	}
	if sends[1].key != "b" {
		t.Errorf("sends[1].key = %q, want %q", sends[1].key, "b")
	}
	if sends[2].text != "c" {
		t.Errorf("sends[2].text = %q, want %q", sends[2].text, "c")
	}
}

func TestParseKeySequence(t *testing.T) {
	seq, err := ParseKeySequence("hello{enter}")
	if err != nil {
		t.Fatalf("ParseKeySequence: %v", err)
	}
	want := []string{"hello", "enter"}
	if len(seq) != len(want) {
		t.Fatalf("got %v, want %v", seq, want)
	}
	for i, s := range want {
		if seq[i] != s {
			t.Errorf("seq[%d] = %q, want %q", i, seq[i], s)
		}
	}
}

func TestRuneFromKey(t *testing.T) {
	tests := []struct {
		key      string
		wantRune rune
		wantOK   bool
	}{
		{"a", 'a', true},
		{"z", 'z', true},
		{"", 0, false},
		{"enter", 0, false},
		{"ab", 0, false},
	}
	for _, tt := range tests {
		r, ok := RuneFromKey(tt.key)
		if ok != tt.wantOK {
			t.Errorf("RuneFromKey(%q) ok = %v, want %v", tt.key, ok, tt.wantOK)
		}
		if ok && r != tt.wantRune {
			t.Errorf("RuneFromKey(%q) = %q, want %q", tt.key, r, tt.wantRune)
		}
	}
}
