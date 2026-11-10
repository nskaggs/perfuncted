package screen

import (
	"context"
	"image"
	"strings"
	"testing"
)

// A nonzero empty rect is malformed, not the full-output sentinel. It must
// fail before capture, rather than silently expose a full desktop frame.
func TestWlrScreencopyRejectsNonzeroEmptyRegionsBeforeCapture(t *testing.T) {
	for _, rect := range []image.Rectangle{
		image.Rect(1, 1, 1, 1),
		{Min: image.Pt(8, 8), Max: image.Pt(2, 2)},
		image.Rect(-3, 4, 7, 4),
	} {
		t.Run(rect.String(), func(t *testing.T) {
			b := &WlrScreencopyBackend{}
			img, err := b.Grab(context.Background(), rect)
			if img != nil || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("Grab(%v) returned image=%v err=%v; want invalid region", rect, img, err)
			}
			hash, err := b.GrabRegionHash(context.Background(), rect)
			if hash != 0 || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("GrabRegionHash(%v) returned hash=%08x err=%v; want invalid region", rect, hash, err)
			}
		})
	}
}
