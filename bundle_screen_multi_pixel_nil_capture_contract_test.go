package perfuncted

import (
	"context"
	"image"
	"strings"
	"testing"
)

// The shared grab helper rejects nil (including typed-nil) images before
// returning. Multiple-pixel reads must preserve that rejection and never
// expose a partially initialized pixel slice.
func TestScreenMultiplePixelsRejectsNilCaptureThroughSharedValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame image.Image
	}{
		{name: "nil image", frame: nil},
		{name: "typed nil image", frame: (*image.RGBA)(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: tc.frame}, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			pixels, err := session.Screen.GetMultiplePixels(context.Background(), []image.Point{{X: 3, Y: 4}, {X: 4, Y: 4}})
			if pixels != nil || err == nil || !strings.Contains(err.Error(), "nil image") {
				t.Fatalf("invalid capture exposed multiple pixels: pixels=%v err=%v", pixels, err)
			}
		})
	}
}
