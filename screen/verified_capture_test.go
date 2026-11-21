package screen

import (
	"errors"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted/find"
)

func TestVerifiedPhysicalRectRequiresCompleteFrameCoverage(t *testing.T) {
	got, err := verifiedPhysicalRect(image.Rect(1, 2, 4, 5), 2, 8, 10)
	if err != nil || got != image.Rect(2, 4, 8, 10) {
		t.Fatalf("scaled complete rectangle = %v, %v", got, err)
	}

	if got, err := verifiedPhysicalRect(image.Rect(0, 0, 10, 10), 2, 18, 18); !errors.Is(err, find.ErrUnverifiedCaptureGeometry) || !got.Empty() {
		t.Fatalf("clipped scaled rectangle = %v, %v", got, err)
	}
}
