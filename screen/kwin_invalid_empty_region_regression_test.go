package screen

import (
	"context"
	"image"
	"strings"
	"testing"
)

// Only the all-zero rectangle is a full-screen sentinel. Invalid nonzero
// empty rectangles must not reach either KWin transport method.
func TestKWinRejectsNonzeroEmptyCaptureRegionsWithoutDispatch(t *testing.T) {
	for _, rect := range []image.Rectangle{
		image.Rect(1, 1, 1, 1),
		{Min: image.Pt(8, 8), Max: image.Pt(2, 2)},
		image.Rect(-3, 4, 7, 4),
	} {
		t.Run(rect.String(), func(t *testing.T) {
			tr := &fakeKWinTransport{active: image.NewRGBA(image.Rect(0, 0, 2, 2)), area: image.NewRGBA(image.Rect(0, 0, 2, 2))}
			b := &KWinShotBackend{transport: tr}
			img, err := b.Grab(context.Background(), rect)
			if img != nil || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("Grab(%v) image=%v err=%v; want invalid region", rect, img, err)
			}
			hash, err := b.GrabRegionHash(context.Background(), rect)
			if hash != 0 || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("GrabRegionHash(%v) hash=%08x err=%v; want invalid region", rect, hash, err)
			}
			if tr.activeCalls != 0 || tr.areaCalls != 0 {
				t.Fatalf("invalid region dispatched active=%d area=%d", tr.activeCalls, tr.areaCalls)
			}
		})
	}
}
