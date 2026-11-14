//go:build linux

package screen

import (
	"image"
	"math"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

// The GNOME bridge is an external source of geometry. Reject malformed
// metadata before rendering or hashing even when the PNG itself decodes.
func TestNormalizeGnomeCaptureRejectsMalformedGeometryMatrix(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	valid := gnomebridge.ScreenCapture{Width: 2, Height: 2, PixelWidth: 2, PixelHeight: 2, Scale: 1}
	tests := []struct {
		name   string
		change func(*gnomebridge.ScreenCapture)
	}{
		{"zero logical width", func(c *gnomebridge.ScreenCapture) { c.Width = 0 }},
		{"negative logical height", func(c *gnomebridge.ScreenCapture) { c.Height = -1 }},
		{"zero pixel width", func(c *gnomebridge.ScreenCapture) { c.PixelWidth = 0 }},
		{"negative pixel height", func(c *gnomebridge.ScreenCapture) { c.PixelHeight = -1 }},
		{"zero scale", func(c *gnomebridge.ScreenCapture) { c.Scale = 0 }},
		{"negative scale", func(c *gnomebridge.ScreenCapture) { c.Scale = -1 }},
		{"not a number scale", func(c *gnomebridge.ScreenCapture) { c.Scale = math.NaN() }},
		{"positive infinite scale", func(c *gnomebridge.ScreenCapture) { c.Scale = math.Inf(1) }},
		{"negative infinite scale", func(c *gnomebridge.ScreenCapture) { c.Scale = math.Inf(-1) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			capture := valid
			tc.change(&capture)
			got, err := normalizeGnomeCapture(frame, capture)
			if got != nil || err == nil || !strings.Contains(err.Error(), "invalid capture geometry") {
				t.Fatalf("malformed capture returned image=%v error=%v", got, err)
			}
		})
	}
}

// Identity geometry must not resample or replace the image, preserving its
// origin and color model for consumers of an unscaled GNOME screenshot.
func TestNormalizeGnomeCaptureIdentityPreservesFrame(t *testing.T) {
	frame := image.NewRGBA(image.Rect(7, 9, 9, 11))
	capture := gnomebridge.ScreenCapture{Width: 2, Height: 2, PixelWidth: 2, PixelHeight: 2, Scale: 1}
	got, err := normalizeGnomeCapture(frame, capture)
	if err != nil {
		t.Fatal(err)
	}
	if got != frame || got.Bounds() != frame.Bounds() {
		t.Fatalf("identity capture was resampled: image=%T bounds=%v", got, got.Bounds())
	}
}
