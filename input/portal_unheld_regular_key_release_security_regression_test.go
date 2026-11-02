//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// A direct KeyUp only releases input held by this serialized EIS writer. An
// unsolicited regular-key release must not affect key state owned by a different
// operation or another client, just as an unheld modifier release is rejected.
func TestRemoteDesktopKeyUpRejectsUnownedOrdinaryKeysWithoutWireWrites(t *testing.T) {
	for _, key := range []string{"a", "enter", "f5", "space"} {
		t.Run(key, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			err := b.KeyUp(context.Background(), key)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unheld key %q was released: %v", key, err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("unheld key mutated ownership: %+v", b.heldKeys)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("validation unexpectedly revoked session: %v", err)
			}
			if err := b.KeyDown(context.Background(), key); err != nil {
				t.Fatalf("valid key down rejected: %v", err)
			}
			for i := 0; i < 2; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatalf("down frame %d: %v", i, err)
				}
			}
			if err := b.KeyUp(context.Background(), key); err != nil {
				t.Fatalf("owned key release refused: %v", err)
			}
			for i := 0; i < 2; i++ {
				if _, err := readSyntheticEISFrame(peer); err != nil {
					t.Fatalf("up frame %d: %v", i, err)
				}
			}
			if len(b.heldKeys) != 0 {
				t.Fatalf("owned release retained state: %+v", b.heldKeys)
			}
		})
	}
}
