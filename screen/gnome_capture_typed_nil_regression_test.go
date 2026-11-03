//go:build linux

package screen

import (
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"image"
	"testing"
)

func TestNormalizeGnomeCaptureRejectsTypedNilFrame(t *testing.T) {
	var rgba *image.RGBA
	capture := gnomebridge.ScreenCapture{Width: 2, Height: 2, PixelWidth: 2, PixelHeight: 2, Scale: 1}
	got, err := normalizeGnomeCapture(rgba, capture)
	if err == nil || got != nil {
		t.Fatalf("invalid GNOME image accepted: %v, %v", got, err)
	}
}
