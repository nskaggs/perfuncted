//go:build linux

package input

import (
	"errors"
	"strings"
	"testing"
)

// An EIS transport loss during teardown is a lost authorization, not a
// successful release. Cover both the held-input release and the stop-device
// message after a prior successful session setup.
func TestRemoteDesktopReleaseHeldInputRevokesOnActualTransportLoss(t *testing.T) {
	for _, tc := range []struct {
		name    string
		setHeld func(*RemoteDesktopBackend)
		want    string
	}{
		{name: "failed held key release", setHeld: func(b *RemoteDesktopBackend) { b.heldKeys[30] = true }, want: "EIS transport failed"},
		{name: "failed stop-device request", setHeld: func(*RemoteDesktopBackend) {}, want: "stop EIS device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newSyntheticRevocableRemoteDesktop(t)
			tc.setHeld(b)
			if err := b.sender.client.conn.Close(); err != nil {
				t.Fatal(err)
			}
			err := b.releaseHeldInput()
			if !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("lost transport was not treated as lost authorization: %v", err)
			}
			if got := b.stateError(); !errors.Is(got, ErrRemoteDesktopRevoked) {
				t.Fatalf("transport loss left backend active: %v", got)
			}
			if tc.want == "EIS transport failed" && !b.heldKeys[30] {
				t.Fatal("failed held-key release silently discarded ownership")
			}
		})
	}
}
