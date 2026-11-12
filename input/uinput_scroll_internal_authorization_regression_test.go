package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The internal wheel dispatch is also a security boundary: callers may
// reach it only with a live operation context and validated click count.
// Neither revoked work nor an invalid wheel request can cause a device write,
// including on a lazily allocated pointer device.
func TestUinputScrollInternalAuthorizationRejectsRevokedOrInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name       string
		horizontal bool
		delta      int
		clicks     int
		canceled   bool
	}{
		{name: "revoked vertical", delta: 1, clicks: 1, canceled: true},
		{name: "revoked horizontal", horizontal: true, delta: -1, clicks: -7, canceled: true},
		{name: "invalid negative vertical", delta: 1, clicks: -1},
		{name: "invalid negative horizontal", horizontal: true, delta: -1, clicks: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mouse := &recordingMouse{}
			b := &UinputBackend{mouse: mouse}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			err := b.scroll(ctx, tc.horizontal, tc.delta, tc.clicks)
			if tc.canceled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("revoked operation returned %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "scroll") {
				t.Fatalf("invalid scroll was admitted: %v", err)
			}
			if events := mouse.eventsSnapshot(); len(events) != 0 {
				t.Fatalf("unauthorized scroll wrote %v", events)
			}
		})
	}
}
