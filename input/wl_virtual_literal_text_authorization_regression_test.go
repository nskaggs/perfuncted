package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Literal keyboard injection has to pass the same operation-admission and
// all-or-nothing text validation as the parsed Type method. Invalid trailing
// UTF-8 cannot inject the valid prefix or alter the keyboard's held state.
func TestWlVirtualLiteralTypingRejectsMalformedTextAndRevokedOperations(t *testing.T) {
	b, peer := newCapturedWlVirtualBackend(t)
	keyboard, wire := newTestKeyboard()
	b.kbd = keyboard
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.TypeLiteral(ctx, "authorized text"); !errors.Is(err, context.Canceled) {
		t.Fatalf("revoked caller injected literal text: %v", err)
	}
	malformed := "valid prefix" + string([]byte{0xff})
	if err := b.TypeLiteral(context.Background(), malformed); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("malformed text was not rejected: %v", err)
	}
	if len(wire.msgs) != 0 || len(keyboard.held) != 0 || keyboard.mods != 0 {
		t.Fatalf("rejected text injected %d events or modified keys: held=%v mods=%d", len(wire.msgs), keyboard.held, keyboard.mods)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.TypeLiteral(context.Background(), "post-close text"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed backend accepted keyboard request: %v", err)
	}
	if len(wire.msgs) != 0 || len(readCapturedWlWrites(t, peer)) != 0 {
		t.Fatalf("rejected literal text leaked virtual-keyboard protocol writes")
	}
}
