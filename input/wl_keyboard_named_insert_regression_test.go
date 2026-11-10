package input

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestVirtualKeyboardInsertIsOneNamedKeyNotSixLetters(t *testing.T) {
	k, wire := newTestKeyboard()
	if err := k.sendkeys(context.Background(), []keySend{{key: "insert"}}); err != nil {
		t.Fatalf("insert tap: %v", err)
	}
	events := 0
	for _, msg := range wire.msgs {
		if len(msg) >= 20 && wl.Uint32(msg[4:8])&0xffff == 1 {
			events++
		}
	}
	if events != 2 {
		t.Fatalf("insert emitted %d key events, want one press and release", events)
	}
}

func TestVirtualKeyboardInsertCanBeHeldAndReleased(t *testing.T) {
	k, wire := newTestKeyboard()
	if err := k.sendkeys(context.Background(), []keySend{{key: "insert", down: true}}); err != nil {
		t.Fatalf("hold insert: %v", err)
	}
	if len(k.held) != 1 {
		t.Fatalf("insert hold missing: %v", k.held)
	}
	prior := len(wire.msgs)
	if err := k.sendkeys(context.Background(), []keySend{{key: "insert"}}); err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("held Insert accepted tap: %v", err)
	}
	if len(wire.msgs) != prior {
		t.Fatalf("invalid insert tap sent %d events", len(wire.msgs)-prior)
	}
	if err := k.sendkeys(context.Background(), []keySend{{key: "insert", up: true}}); err != nil {
		t.Fatalf("release insert: %v", err)
	}
	if len(k.held) != 0 {
		t.Fatalf("insert remained held: %v", k.held)
	}
}
