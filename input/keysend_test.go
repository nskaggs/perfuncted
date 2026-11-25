package input

import (
	"context"
	"errors"
	"github.com/bendahl/uinput"
	"strings"
	"testing"
)

type cancelAtKeysendCheck struct {
	context.Context
	checks   int
	cancelAt int
}

func (c *cancelAtKeysendCheck) Err() error {
	c.checks++
	if c.checks >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestKeysendCoverageCancellationAtParserBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		cancelAt int
	}{
		{"between actions", "literal", 2},
		{"during closing brace scan", "{enter}", 3},
		{"during literal scan", "literal", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &cancelAtKeysendCheck{Context: context.Background(), cancelAt: tc.cancelAt}
			_, err := parseKeySendContext(ctx, tc.text)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected canceled parser, got %v after %d checks", err, ctx.checks)
			}
		})
	}
}

func TestKeysendCoverageChunkedIndexStopsAfterCancellation(t *testing.T) {
	ctx := &cancelAtKeysendCheck{Context: context.Background(), cancelAt: 2}
	index, err := indexByteContext(ctx, strings.Repeat("a", 32*1024+1), 'z')
	if index != -1 || !errors.Is(err, context.Canceled) || ctx.checks != 2 {
		t.Fatalf("index=%d err=%v checks=%d", index, err, ctx.checks)
	}
}

func TestKeysendCoverageKeyExpressionAndGrammarRejections(t *testing.T) {
	_, err := parseKeySendContext(context.Background(), "{"+strings.Repeat("a", maxKeyExpressionBytes+1)+"}")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize expression: %v", err)
	}
	if _, err := parseCombo("plain-key-without-modifier", false, false); err == nil {
		t.Fatal("accepted combo without separator")
	}
	if err := ValidateKeySequence(nil, "{enter}"); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := ValidateKeySequence(context.Background(), "{unfinished"); err == nil {
		t.Fatal("malformed sequence accepted")
	}
	if err := ValidateKeySequence(context.Background(), "{ctrl+s}literal"); err != nil {
		t.Fatalf("valid syntax: %v", err)
	}
	if got, err := parseBraced("ctrl down"); err != nil || !got.down || got.up || got.key != "ctrl" {
		t.Fatalf("explicit down parse: %+v, %v", got, err)
	}
}

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
			want:  keySend{key: "shift", up: true},
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
			sends, err := parseKeySend(tc.input)
			if err != nil {
				t.Fatalf("parseKeySend(%q) error = %v", tc.input, err)
			}
			if len(sends) != 1 {
				t.Fatalf("parseKeySend(%q) returned %d sends, want 1", tc.input, len(sends))
			}
			if sends[0] != tc.want {
				t.Errorf("parseKeySend(%q) = %+v, want %+v", tc.input, sends[0], tc.want)
			}
		})
	}
}

func TestParseKeySend_MixedTextAndKeys(t *testing.T) {
	sends, err := parseKeySend("hello{enter}world")
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
	sends, err := parseKeySend("{ctrl down}v{ctrl up}")
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
	if sends[2].key != "ctrl" || sends[2].down || !sends[2].up {
		t.Error("expected ctrl up")
	}
}

func TestValidateWorkflowKeySend(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sequence string
		wantErr  bool
	}{
		{name: "literal text", sequence: "hello"},
		{name: "atomic chord", sequence: "{ctrl+s}"},
		{name: "explicit balanced hold", sequence: "{ctrl down}v{ctrl up}", wantErr: true},
		{name: "unreleased key", sequence: "{ctrl down}", wantErr: true},
		{name: "unowned release", sequence: "{ctrl up}", wantErr: true},
		{name: "invalid sequence", sequence: "{invalid+}", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateWorkflowKeySend(context.Background(), tc.sequence); (err != nil) != tc.wantErr {
				t.Fatalf("ValidateWorkflowKeySend(%q) error = %v, want error %v", tc.sequence, err, tc.wantErr)
			}
		})
	}
	if err := ValidateWorkflowKeySend(nil, "{ctrl+s}"); err != nil {
		t.Fatalf("nil context: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidateWorkflowKeySend(canceled, "a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: got %v, want context canceled", err)
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
			_, err := parseKeySend(tc.input)
			if err == nil {
				t.Fatalf("parseKeySend(%q) expected error, got nil", tc.input)
			}
		})
	}
}

func TestParseKeySend_LiteralBracesInOutput(t *testing.T) {
	// Literal text is never wrapped in braces by ParseKeySend.
	// The braces are stripped during parsing.
	sends, err := parseKeySend("a{b}c")
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

func TestValidateKeySequenceCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidateKeySequence(ctx, "{enter}"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled validation: %v", err)
	}
}

// Resolving a key name to an evdev code happens on every keystroke of a portal
// RemoteDesktop session.
func BenchmarkRemoteKeyCode(b *testing.B) {
	for _, name := range []string{"a", "q", "z", "0", "9", "space", "enter", "Return"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := remoteKeyCode(name); err != nil {
					b.Fatalf("remoteKeyCode(%q): %v", name, err)
				}
			}
		})
	}
}

// A literal character lookup takes the rune-table path rather than the key-name
// path.
func BenchmarkRemoteKeyCodeRune(b *testing.B) {
	for _, r := range []rune{'a', 'Z', '7', '!'} {
		b.Run(string(r), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := remoteKeyCode(string(r)); err != nil {
					b.Fatalf("remoteKeyCode(%q): %v", r, err)
				}
			}
		})
	}
}

func BenchmarkQwertyRuneMap(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if len(qwertyRuneMap()) == 0 {
			b.Fatal("empty rune map")
		}
	}
}

func TestRemoteDesktopKeycodeMappingsCoverModifiersNavigationAndFunctionKeys(t *testing.T) {
	tests := []struct {
		name string
		want uint32
	}{
		{"a", uinput.KeyA}, {"z", uinput.KeyZ}, {"0", uinput.Key0}, {"9", uinput.Key9},
		{"f1", uinput.KeyF1}, {"f4", uinput.KeyF4}, {"f10", uinput.KeyF10},
		{"f11", uinput.KeyF11}, {"f12", uinput.KeyF12},
		{"space", uinput.KeySpace}, {"enter", uinput.KeyEnter},
		{"tab", uinput.KeyTab}, {"backspace", uinput.KeyBackspace}, {"esc", uinput.KeyEsc},
		{"ctrl", uinput.KeyLeftctrl}, {"alt", uinput.KeyLeftalt},
		{"shift", uinput.KeyLeftshift}, {"super", uinput.KeyLeftmeta},
		{"up", uinput.KeyUp}, {"down", uinput.KeyDown},
		{"left", uinput.KeyLeft}, {"right", uinput.KeyRight},
		{"home", uinput.KeyHome}, {"end", uinput.KeyEnd},
		{"pageup", uinput.KeyPageup}, {"pagedown", uinput.KeyPagedown},
		{"insert", uinput.KeyInsert}, {"delete", uinput.KeyDelete},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, err := remoteKeyCode(tc.name)
			if err != nil || code != tc.want {
				t.Fatalf("remote key %q = %d, %v want %d", tc.name, code, err, tc.want)
			}
		})
	}
	for _, invalid := range []string{"", "  ", "DefinitelyNotAKeyboardKey", "not-real", "🔑"} {
		if code, err := remoteKeyCode(invalid); err == nil {
			t.Fatalf("invalid key name %q mapped to %d", invalid, code)
		}
	}
	if upper, err := remoteKeyCode(" A "); err != nil || upper != uinput.KeyA {
		t.Fatalf("trimmed upper-case a should map to key A: %d %v", upper, err)
	}
	if code, err := remoteKeyCode(strings.ToUpper("f12")); err != nil || code != uinput.KeyF12 {
		t.Fatalf("uppercase function key mapping %d, %v", code, err)
	}
}
