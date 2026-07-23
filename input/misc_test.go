package input

import (
	"context"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/ctxutil"
)

// ── ctxutil.Default ──────────────────────────────────────────────────────────

func TestNormalizeContext_NilReturnsBackground(t *testing.T) {
	var nilCtx context.Context //nolint:SA1012 // testing nil handling
	got := ctxutil.Default(nilCtx)
	if got == nil {
		t.Fatal("ctxutil.Default(nil) returned nil, want non-nil context")
	}
	// Should be equivalent to context.Background(): no deadline, never cancelled.
	select {
	case <-got.Done():
		t.Fatal("ctxutil.Default(nil) returned a cancelled context")
	default:
	}
}

func TestNormalizeContext_NonNilPassThrough(t *testing.T) {
	ctx := context.Background()
	got := ctxutil.Default(ctx)
	if got != ctx {
		t.Fatal("ctxutil.Default(non-nil) should return the same context")
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
	if !sends[0].up {
		t.Error("expected up=true for {tab up}")
	}
	if sends[0].key != "tab" {
		t.Errorf("key = %q, want tab", sends[0].key)
	}
}
