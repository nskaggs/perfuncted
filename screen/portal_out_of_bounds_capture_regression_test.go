//go:build linux

package screen

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The portal delivers a full-screen PNG. A requested region completely
// outside that image is not a successful capture of transparent pixels.
func TestSyntheticScreenshotPortalRejectsWhollyOutsideCaptureRegion(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	path := filepath.Join(t.TempDir(), "screen.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	portal.mu.Lock()
	portal.uri = "file://" + path
	portal.mu.Unlock()
	for _, rect := range []image.Rectangle{image.Rect(8, 8, 10, 10), image.Rect(-10, -10, -8, -8)} {
		img, err := backend.Grab(context.Background(), rect)
		if err == nil || img != nil || !strings.Contains(err.Error(), "outside") {
			t.Fatalf("out-of-bounds screenshot %v returned image=%v error=%v", rect, img, err)
		}
	}
}
