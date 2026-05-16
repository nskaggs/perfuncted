package input

import (
	"context"
	"testing"
	"time"
)

// ── normalizeContext ──────────────────────────────────────────────────────────

func TestNormalizeContext_NilReturnsBackground(t *testing.T) {
	var nilCtx context.Context //nolint:SA1012 // testing nil handling
	got := normalizeContext(nilCtx)
	if got == nil {
		t.Fatal("normalizeContext(nil) returned nil, want non-nil context")
	}
	// Should be equivalent to context.Background(): no deadline, never cancelled.
	select {
	case <-got.Done():
		t.Fatal("normalizeContext(nil) returned a cancelled context")
	default:
	}
}

func TestNormalizeContext_NonNilPassThrough(t *testing.T) {
	ctx := context.Background()
	got := normalizeContext(ctx)
	if got != ctx {
		t.Fatal("normalizeContext(non-nil) should return the same context")
	}
}

// ── sleepContext — zero / negative duration ───────────────────────────────────

func TestSleepContext_ZeroDuration(t *testing.T) {
	// Zero duration returns ctx.Err() immediately (nil for a live context).
	ctx := context.Background()
	if err := sleepContext(ctx, 0); err != nil {
		t.Fatalf("sleepContext(bg, 0) = %v, want nil", err)
	}
}

func TestSleepContext_ZeroDuration_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := sleepContext(ctx, 0)
	if err != context.Canceled {
		t.Fatalf("sleepContext(cancelled, 0) = %v, want context.Canceled", err)
	}
}

func TestSleepContext_NilContext_ZeroDuration(t *testing.T) {
	// nil context is normalised to Background, so 0-duration returns nil.
	var nilCtx context.Context //nolint:SA1012 // testing nil handling
	if err := sleepContext(nilCtx, 0); err != nil {
		t.Fatalf("sleepContext(nil, 0) = %v, want nil", err)
	}
}

func TestSleepContext_NilContext_PositiveDuration(t *testing.T) {
	// nil context is normalised to Background; positive duration should complete.
	var nilCtx context.Context //nolint:SA1012 // testing nil handling
	if err := sleepContext(nilCtx, 1*time.Millisecond); err != nil {
		t.Fatalf("sleepContext(nil, 1ms) = %v, want nil", err)
	}
}

// ── ParseKeySequence — error path ─────────────────────────────────────────────

func TestParseKeySequence_Error(t *testing.T) {
	_, err := ParseKeySequence("{unclosed")
	if err == nil {
		t.Fatal("ParseKeySequence: expected error for unclosed brace, got nil")
	}
}

func TestParseKeySequence_Empty(t *testing.T) {
	seq, err := ParseKeySequence("")
	if err != nil {
		t.Fatalf("ParseKeySequence(\"\") error = %v", err)
	}
	if len(seq) != 0 {
		t.Fatalf("ParseKeySequence(\"\") = %v, want empty", seq)
	}
}

func TestParseKeySequence_KeysOnly(t *testing.T) {
	seq, err := ParseKeySequence("{ctrl+s}{enter}")
	if err != nil {
		t.Fatalf("ParseKeySequence error = %v", err)
	}
	if len(seq) != 2 {
		t.Fatalf("got %d elements, want 2: %v", len(seq), seq)
	}
	// The combo key is "s"; the modifier is captured in the keySend struct
	// but ParseKeySequence returns just the key name.
	if seq[0] != "s" {
		t.Errorf("seq[0] = %q, want \"s\"", seq[0])
	}
	if seq[1] != "enter" {
		t.Errorf("seq[1] = %q, want \"enter\"", seq[1])
	}
}

// ── parseCombo — modifier aliases ─────────────────────────────────────────────

func TestParseCombo_ModifierAliases(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantKey   string
		wantSuper bool
		wantCtrl  bool
	}{
		{name: "WinModifier", input: "{win+d}", wantKey: "d", wantSuper: true},
		{name: "LogoModifier", input: "{logo+l}", wantKey: "l", wantSuper: true},
		{name: "ControlAlias", input: "{control+c}", wantKey: "c", wantCtrl: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ks, err := ParseKeySend(tc.input)
			if err != nil {
				t.Fatalf("ParseKeySend(%q) error = %v", tc.input, err)
			}
			if ks[0].key != tc.wantKey {
				t.Errorf("key = %q, want %q", ks[0].key, tc.wantKey)
			}
			if ks[0].modifiers.super != tc.wantSuper {
				t.Errorf("super = %v, want %v", ks[0].modifiers.super, tc.wantSuper)
			}
			if ks[0].modifiers.ctrl != tc.wantCtrl {
				t.Errorf("ctrl = %v, want %v", ks[0].modifiers.ctrl, tc.wantCtrl)
			}
		})
	}
}

func TestParseCombo_UnknownModifier(t *testing.T) {
	_, err := ParseKeySend("{bogus+s}")
	if err == nil {
		t.Fatal("expected error for unknown modifier bogus")
	}
}

func TestParseCombo_EmptyKey(t *testing.T) {
	_, err := ParseKeySend("{ctrl+}")
	if err == nil {
		t.Fatal("expected error for empty key after +")
	}
}

// ── parseBraced — edge cases ──────────────────────────────────────────────────

func TestParseBraced_WhitespaceOnly(t *testing.T) {
	_, err := ParseKeySend("{   }")
	if err == nil {
		t.Fatal("expected error for whitespace-only braced expression")
	}
}

func TestParseBraced_DownSuffix(t *testing.T) {
	sends, err := ParseKeySend("{enter down}")
	if err != nil {
		t.Fatalf("ParseKeySend({enter down}) error = %v", err)
	}
	if !sends[0].down {
		t.Error("expected down=true for {enter down}")
	}
	if sends[0].key != "enter" {
		t.Errorf("key = %q, want enter", sends[0].key)
	}
}

func TestParseBraced_UpSuffix(t *testing.T) {
	sends, err := ParseKeySend("{tab up}")
	if err != nil {
		t.Fatalf("ParseKeySend({tab up}) error = %v", err)
	}
	if sends[0].down {
		t.Error("expected down=false for {tab up}")
	}
	if sends[0].key != "tab" {
		t.Errorf("key = %q, want tab", sends[0].key)
	}
}

// ── RuneFromKey — edge cases ──────────────────────────────────────────────────

func TestRuneFromKey_MultiByteRune(t *testing.T) {
	// "é" is a single rune but multi-byte UTF-8.
	r, ok := RuneFromKey("é")
	if !ok {
		t.Fatal("RuneFromKey(\"é\") ok = false, want true")
	}
	if r != 'é' {
		t.Fatalf("RuneFromKey(\"é\") = %q, want é", r)
	}
}

func TestRuneFromKey_TwoRunes(t *testing.T) {
	_, ok := RuneFromKey("ab")
	if ok {
		t.Fatal("RuneFromKey(\"ab\") ok = true, want false (two runes)")
	}
}
