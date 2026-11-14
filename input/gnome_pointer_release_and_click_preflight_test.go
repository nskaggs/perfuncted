package input

import (
	"context"
	"math"
	"strings"
	"testing"
)

// Invalid releases and coordinates must never reach GNOME's virtual device:
// truncation of an int into an int32 can point at a different application.
func TestGnomePointerRejectsInvalidReleasesAndUnrepresentableClicks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		call    func(*GnomeNativeBackend) error
		explain string
	}{
		{"release button zero", func(b *GnomeNativeBackend) error { return b.MouseUp(context.Background(), 0) }, "button"},
		{"release button four", func(b *GnomeNativeBackend) error { return b.MouseUp(context.Background(), 4) }, "button"},
		{"click x positive overflow", func(b *GnomeNativeBackend) error { return b.MouseClick(context.Background(), math.MaxInt, 9, 1) }, "coordinate"},
		{"click y negative overflow", func(b *GnomeNativeBackend) error { return b.MouseClick(context.Background(), 5, math.MinInt, 3) }, "coordinate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &pointerGnomeBridge{}
			b := &GnomeNativeBackend{bridge: bridge}
			err := tc.call(b)
			if err == nil || !strings.Contains(err.Error(), tc.explain) {
				t.Fatalf("invalid pointer request admitted: %v", err)
			}
			if len(bridge.events) != 0 {
				t.Fatalf("invalid pointer request leaked compositor events: %v", bridge.events)
			}
		})
	}
}
