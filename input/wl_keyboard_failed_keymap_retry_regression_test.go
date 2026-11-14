//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A failed virtual-keyboard keymap upload cannot be cached as accepted:
// retry must resend the keymap after transport recovery, while successful
// identical subsequent uploads must remain idempotent.
func TestVirtualKeyboardRetriesUnacknowledgedKeymapAfterTransportRecovery(t *testing.T) {
	keyboard, transport := newFailingKeyboard(0)
	const keymap = "xkb_keymap { xkb_keycodes { <K008> = 8; }; };"
	if err := keyboard.uploadKeymap(context.Background(), keymap); err == nil {
		t.Fatal("failed keymap upload was acknowledged")
	}
	if keyboard.lastKeymap != "" || transport.writes != 0 {
		t.Fatalf("failed keymap was cached: last=%q writes=%d", keyboard.lastKeymap, transport.writes)
	}
	transport.successWrites = 1
	if err := keyboard.uploadKeymap(context.Background(), keymap); err != nil {
		t.Fatalf("keymap upload did not recover: %v", err)
	}
	if keyboard.lastKeymap != keymap || transport.writes != 1 {
		t.Fatalf("recovered keymap was not cached: last=%q writes=%d", keyboard.lastKeymap, transport.writes)
	}
	if err := keyboard.uploadKeymap(context.Background(), keymap); err != nil || transport.writes != 1 {
		t.Fatalf("duplicate keymap transmission: err=%v writes=%d", err, transport.writes)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := keyboard.uploadKeymap(canceled, keymap+" changed"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled upload was acknowledged: %v", err)
	}
	if keyboard.lastKeymap != keymap || transport.writes != 1 {
		t.Fatal("canceled upload changed the accepted keymap")
	}
	// The accepted frame remains a normal Wayland keymap request.
	if got := wl.Uint32(transport.msgs[0][4:8]) & 0xffff; got != 0 {
		t.Fatalf("unexpected keymap request opcode: %d", got)
	}
}
