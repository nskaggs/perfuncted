//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// A Type batch must not release an ordinary key that this EIS writer never
// pressed, even after a safe earlier text or key action in the same batch.
func TestRemoteDesktopTypeRejectsUnownedOrdinaryKeyReleaseBeforeAnyWireFrames(t *testing.T) {
	for _, tc := range []struct{ name, batch string }{
		{"unheld letter", "{a up}"},
		{"unheld enter after private text", "private{enter up}"},
		{"unheld function key after valid tap", "{b}{f5 up}"},
		{"double release in same batch", "{a down}{a up}{a up}"},
		{"release without down following text", "confidential{space up}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			err := b.Type(context.Background(), tc.batch)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unowned release in %q accepted: %v", tc.batch, err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 {
				t.Fatalf("refused batch retained held keys: %+v", b.heldKeys)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("invalid batch revoked valid session: %v", err)
			}
		})
	}
}
