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

// A valid screenshot authorization and a valid local file path do not imply
// that the portal supplied a valid image. Failed decoding must not publish
// pixels or strand the capture gate for the next request.
func TestScreenshotPortalRejectsAuthorizedCorruptPNGAndRecovers(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	bad := filepath.Join(t.TempDir(), "invalid-capture.png")
	if err := os.WriteFile(bad, []byte("not a PNG, despite being a portal-approved screenshot"), 0600); err != nil {
		t.Fatal(err)
	}
	portal.mu.Lock()
	portal.uri = "file://" + bad
	portal.mu.Unlock()
	img, err := backend.Grab(context.Background(), image.Rectangle{})
	if err == nil || img != nil || !strings.Contains(err.Error(), "decode PNG") {
		t.Fatalf("malformed authorized image accepted: image=%v err=%v", img, err)
	}
	if len(backend.captureGate) != 0 {
		t.Fatal("malformed screenshot left capture gate held")
	}
	good := filepath.Join(t.TempDir(), "recovered.png")
	file, err := os.Create(good)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	portal.mu.Lock()
	portal.uri = "file://" + good
	portal.mu.Unlock()
	img, err = backend.Grab(context.Background(), image.Rectangle{})
	if err != nil || img == nil || img.Bounds().Dx() != 3 || img.Bounds().Dy() != 2 {
		t.Fatalf("portal remained unusable after malformed PNG: image=%v err=%v", img, err)
	}
	if len(backend.captureGate) != 0 {
		t.Fatal("successful screenshot left capture gate held")
	}
}
