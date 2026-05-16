package input

import (
	"testing"
)

type keySendCase struct {
	name  string
	input string
	want  keySend
}

func TestParseKeySend_Table(t *testing.T) {
	cases := []keySendCase{
		{
			name:  "LiteralText",
			input: "hello world",
			want:  keySend{text: "hello world"},
		},
		{
			name:  "NamedKey",
			input: "{enter}",
			want:  keySend{key: "enter"},
		},
		{
			name:  "KeyDown",
			input: "{ctrl down}",
			want:  keySend{key: "ctrl", down: true},
		},
		{
			name:  "KeyUp",
			input: "{shift up}",
			want:  keySend{key: "shift"},
		},
		{
			name:  "BraceCombo",
			input: "{ctrl+s}",
			want:  keySend{key: "s", modifiers: modifiers{ctrl: true}},
		},
		{
			name:  "BraceComboMultipleModifiers",
			input: "{ctrl+shift+left}",
			want:  keySend{key: "left", modifiers: modifiers{ctrl: true, shift: true}},
		},
		{
			name:  "BraceComboSuper",
			input: "{super+tab}",
			want:  keySend{key: "tab", modifiers: modifiers{super: true}},
		},
		{
			name:  "BraceComboMetaAlias",
			input: "{meta+tab}",
			want:  keySend{key: "tab", modifiers: modifiers{super: true}},
		},
		{
			name:  "BraceComboAllModifiers",
			input: "{ctrl+alt+shift+super+a}",
			want:  keySend{key: "a", modifiers: modifiers{ctrl: true, alt: true, shift: true, super: true}},
		},
		{
			name:  "CaseInsensitive",
			input: "{ENTER}",
			want:  keySend{key: "enter"},
		},
		{
			name:  "BraceComboCaseInsensitive",
			input: "{CTRL+SHIFT+T}",
			want:  keySend{key: "t", modifiers: modifiers{ctrl: true, shift: true}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sends, err := ParseKeySend(tc.input)
			if err != nil {
				t.Fatalf("ParseKeySend(%q) error = %v", tc.input, err)
			}
			if len(sends) != 1 {
				t.Fatalf("ParseKeySend(%q) returned %d sends, want 1", tc.input, len(sends))
			}
			if sends[0] != tc.want {
				t.Errorf("ParseKeySend(%q) = %+v, want %+v", tc.input, sends[0], tc.want)
			}
		})
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

func TestParseKeySend_Errors(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"UnclosedBrace", "{enter"},
		{"EmptyBraces", "{}"},
		{"BraceComboEndsWithModifier", "{ctrl+shift}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseKeySend(tc.input)
			if err == nil {
				t.Fatalf("ParseKeySend(%q) expected error, got nil", tc.input)
			}
		})
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
