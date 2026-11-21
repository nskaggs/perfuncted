//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"testing"
)

// A revoked capture must not progress to backend initialization or expose pixels,
// even when the caller supplies a malformed region or a disconnected backend.
func TestPortalGrabRevokedContextPrecedesBackendAndRegionChecks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name    string
		backend *PortalDBusBackend
		rect    image.Rectangle
	}{
		{name: "nil backend and full capture"},
		{name: "disconnected backend and invalid rectangle", backend: &PortalDBusBackend{}, rect: image.Rect(4, 4, 4, 8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img, err := tc.backend.Grab(ctx, tc.rect)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Grab() error = %v, want context.Canceled", err)
			}
			if img != nil {
				t.Fatalf("Grab() returned pixels for a revoked capture: %v", img.Bounds())
			}
		})
	}
}
