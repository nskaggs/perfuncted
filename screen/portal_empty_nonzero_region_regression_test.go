//go:build linux

package screen

import (
	"context"
	"image"
	"strings"
	"testing"
	"time"
)

// Only the zero rectangle is the documented full-screen sentinel. Other
// empty or inverted rectangles must not silently disclose a full screenshot.
func TestPortalRejectsEmptyNonzeroCaptureRegionsBeforeConsent(t *testing.T) {
	for _, tc := range []struct {
		name string
		rect image.Rectangle
	}{
		{"inverted", image.Rectangle{Min: image.Pt(5, 5), Max: image.Pt(2, 2)}},
		{"zero width", image.Rect(2, 2, 2, 4)},
		{"offset empty", image.Rect(2, 2, 2, 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, portal := newSyntheticScreenshotPortal(t)
			b.SetCaptureTimeout(70 * time.Millisecond)
			portal.mu.Lock()
			portal.skip = true
			portal.mu.Unlock()
			img, err := b.Grab(context.Background(), tc.rect)
			if err == nil || img != nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("invalid region %v returned image=%v error=%v", tc.rect, img, err)
			}
		})
	}
}
