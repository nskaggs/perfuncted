//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// A sequence containing a key which the backend cannot send must fail
// before changing desktop state, even if it follows a valid literal or held
// modifier. Invalid UTF-8 must never be silently converted into U+FFFD.
func TestGnomeNativeSequencePreflightRejectsUnknownKeysBeforeValidPrefix(t *testing.T) {
	for _, tc := range []struct{ name, sequence string }{
		{"literal then unknown named key", "private{not-a-real-key}"},
		{"modifier down then unknown named key", "{ctrl down}{not-a-real-key}"},
		{"valid named key then unknown", "{enter}{not-a-real-key}"},
		{"valid accelerator then unknown", "{ctrl+a}{not-a-real-key}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &lifecycleGnomeBridge{}
			backend := &GnomeNativeBackend{bridge: bridge}
			err := backend.Type(context.Background(), tc.sequence)
			if err == nil {
				t.Fatalf("unsupported sequence accepted: %q", tc.sequence)
			}
			if len(bridge.log) != 0 || len(bridge.events) != 0 || backend.held.any() {
				t.Fatalf("invalid sequence mutated desktop: events=%v log=%v held=%+v error=%v", bridge.events, bridge.log, backend.held, err)
			}
		})
	}
}

func TestGnomeNativeRejectsInvalidUTF8BeforeDesktopBridgeCalls(t *testing.T) {
	bad := string([]byte{0xff, 0xfe})
	for _, tc := range []struct {
		name string
		run  func(*GnomeNativeBackend) error
	}{
		{"literal", func(b *GnomeNativeBackend) error { return b.TypeLiteral(context.Background(), "valid"+bad) }},
		{"type", func(b *GnomeNativeBackend) error { return b.Type(context.Background(), "valid"+bad) }},
		{"named key", func(b *GnomeNativeBackend) error { return b.KeyDown(context.Background(), bad) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &lifecycleGnomeBridge{}
			b := &GnomeNativeBackend{bridge: bridge}
			err := tc.run(b)
			if err == nil || !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("invalid UTF-8 accepted: %v", err)
			}
			if len(bridge.log) != 0 || len(bridge.events) != 0 {
				t.Fatalf("invalid UTF-8 sent to desktop: log=%v events=%v", bridge.log, bridge.events)
			}
		})
	}
}

// A stray explicit modifier release at the tail is invalid in XTEST/uinput
// and Wayland keyboard sequences; GNOME must not partially type the prefix
// before finding that the caller never held the modifier.
func TestGnomeNativeRejectsUnheldModifierReleaseBeforePrefix(t *testing.T) {
	for _, seq := range []string{"sensitive{ctrl up}", "{enter}{shift up}", "{alt up}", "{ctrl down}{shift up}"} {
		t.Run(seq, func(t *testing.T) {
			bridge := &lifecycleGnomeBridge{}
			b := &GnomeNativeBackend{bridge: bridge}
			err := b.Type(context.Background(), seq)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unheld modifier release not rejected: %v", err)
			}
			if len(bridge.log) != 0 || len(bridge.events) != 0 || b.held.any() {
				t.Fatalf("unheld modifier release sent partial keyboard events: log=%v keys=%v held=%+v", bridge.log, bridge.events, b.held)
			}
		})
	}
}

// Unicode needs a clipboard fallback on GNOME, which cannot run with a held
// modifier. Invalid late combinations must be rejected before any earlier
// text or key press reaches the window.
func TestGnomeNativePreflightsUnicodeAgainstHeldModifierSequence(t *testing.T) {
	for _, tc := range []struct{ name, seq string }{
		{"modifier before Unicode", "{ctrl down}é{ctrl up}"},
		{"literal prefix before modifier and Unicode", "safe{shift down}é"},
		{"accelerator before modifier and Unicode", "{ctrl+a}{alt down}é"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &lifecycleGnomeBridge{}
			b := &GnomeNativeBackend{bridge: bridge}
			err := b.Type(context.Background(), tc.seq)
			if err == nil || !strings.Contains(err.Error(), "held modifiers") {
				t.Fatalf("invalid clipboard gesture accepted: %v", err)
			}
			if len(bridge.log) != 0 || len(bridge.events) != 0 || b.held.any() {
				t.Fatalf("unsupported gesture partially mutated desktop: log=%v events=%v held=%+v", bridge.log, bridge.events, b.held)
			}
		})
	}
}

func TestGnomeNativeLiteralUnicodeDoesNotPasteWhileCallerHoldsModifier(t *testing.T) {
	bridge := &lifecycleGnomeBridge{}
	b := &GnomeNativeBackend{bridge: bridge}
	if err := b.KeyDown(context.Background(), "ctrl"); err != nil {
		t.Fatal(err)
	}
	bridge.log = nil
	bridge.events = nil
	err := b.TypeLiteral(context.Background(), "é")
	if err == nil || !strings.Contains(err.Error(), "held modifiers") {
		t.Fatalf("Unicode paste with caller-held Ctrl accepted: %v", err)
	}
	if len(bridge.log) != 0 || len(bridge.events) != 0 || !b.held.ctrl {
		t.Fatalf("unsupported paste changed caller-held modifier state: log=%v events=%v held=%+v", bridge.log, bridge.events, b.held)
	}
	if err := b.TypeLiteral(context.Background(), "ASCII"); err != nil {
		t.Fatalf("safe direct text with caller-held Ctrl rejected: %v", err)
	}
	if len(bridge.log) != 1 || bridge.log[0] != "text:ASCII" {
		t.Fatalf("safe text not typed directly: %v", bridge.log)
	}
}
