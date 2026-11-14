package perfuncted

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// ScreenBundle delegates nil caller contexts to backendContext, which treats
// them as Background. Post-capture checks must use that effective context too.
func TestScreenCaptureRegionNilContextDoesNotPanicAfterSuccessfulGrab(t *testing.T) {
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: image.NewRGBA(image.Rect(0, 0, 2, 2))}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	path := filepath.Join(t.TempDir(), "capture.png")
	defer func() {
		if failure := recover(); failure != nil {
			t.Errorf("CaptureRegion nil context panic: %v", failure)
		}
	}()
	if err := session.Screen.CaptureRegion(nil, image.Rect(0, 0, 2, 2), path); err != nil {
		t.Fatalf("nil context capture: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	actual, err := png.Decode(f)
	if err != nil || actual.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Fatalf("capture file bounds=%v err=%v", actual, err)
	}
}

func TestScreenMultiplePixelsNilContextDoesNotPanicAfterSuccessfulGrab(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))
	frame.SetRGBA(0, 0, color.RGBA{R: 0x33, G: 0x44, B: 0x55, A: 0xff})
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	defer func() {
		if failure := recover(); failure != nil {
			t.Errorf("GetMultiplePixels nil context panic: %v", failure)
		}
	}()
	pixels, err := session.Screen.GetMultiplePixels(nil, []image.Point{{X: 1, Y: 1}})
	if err != nil || len(pixels) != 1 || pixels[0] != (color.RGBA{R: 0x33, G: 0x44, B: 0x55, A: 0xff}) {
		t.Fatalf("nil context pixels=%v err=%v", pixels, err)
	}
}
