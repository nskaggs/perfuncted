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

func TestSyntheticScreenshotPortalRejectsMalformedAuthorizationCode(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	path := filepath.Join(t.TempDir(), "valid.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if encodeErr := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 2, 2))); encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}

	// A malicious or incompatible portal must not be able to return an
	// untyped/string authorization status and have it interpreted as "granted".
	portal.mu.Lock()
	portal.uri = "file://" + path
	portal.malformedCode = "0"
	portal.mu.Unlock()

	img, err := backend.Grab(context.Background(), image.Rectangle{})
	if err == nil || img != nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("screenshot accepted malformed permission status: image=%v err=%v", img, err)
	}
}
