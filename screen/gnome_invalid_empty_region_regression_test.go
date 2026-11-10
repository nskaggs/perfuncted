//go:build linux

package screen

import (
	"image"
	"strings"
	"testing"
)

// Both GNOME screenshot paths use validateGnomeCaptureRect before
// choosing full-screen capture. Only an all-zero rectangle may mean full.
func TestGnomeCaptureRejectsNonzeroEmptyRegions(t *testing.T) {
	for _, rect := range []image.Rectangle{
		image.Rect(1, 1, 1, 1),
		{Min: image.Pt(8, 8), Max: image.Pt(2, 2)},
		image.Rect(-3, 4, 7, 4),
	} {
		t.Run(rect.String(), func(t *testing.T) {
			err := validateGnomeCaptureRect(rect)
			if err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("validateGnomeCaptureRect(%v) err=%v; want invalid region", rect, err)
			}
		})
	}
	if err := validateGnomeCaptureRect(image.Rectangle{}); err != nil {
		t.Fatalf("zero rectangle should remain full-screen sentinel: %v", err)
	}
}
