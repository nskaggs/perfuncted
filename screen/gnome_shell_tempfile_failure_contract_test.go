//go:build linux

package screen

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A capture cannot proceed without a private transport file. Failure to
// create it must not send a screenshot request to the compositor.
func TestGnomeShellCaptureRejectsUnwritableTemporaryDirectoryWithoutDBus(t *testing.T) {
	backend, service := newSyntheticGnomeScreenshotBackend(t)
	service.mu.Lock()
	previous := len(service.observed)
	service.mu.Unlock()
	invalid := filepath.Join(t.TempDir(), "missing", "transport")
	t.Setenv("TMPDIR", invalid)
	for _, tc := range []struct {
		name    string
		capture func() (image.Image, error)
	}{
		{"full", func() (image.Image, error) { return backend.Grab(context.Background(), image.Rectangle{}) }},
		{"area", func() (image.Image, error) { return backend.Grab(context.Background(), image.Rect(0, 0, 5, 5)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img, err := tc.capture()
			if img != nil || err == nil || !strings.Contains(err.Error(), "temp file") {
				t.Fatalf("capture with inaccessible TMPDIR image=%v error=%v", img, err)
			}
		})
	}
	service.mu.Lock()
	after := len(service.observed)
	service.mu.Unlock()
	if after != previous {
		t.Fatalf("inaccessible TMPDIR dispatched %d screenshot requests", after-previous)
	}
	if _, err := os.Stat(invalid); !os.IsNotExist(err) {
		t.Fatalf("capture created unexpected path or returned wrong stat error: %v", err)
	}
}

func TestGnomeScreenshotDecodeRejectsMissingAndInvalidPNG(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent.png")
	if img, err := decodeScreenshot(missing); img != nil || err == nil || !strings.Contains(err.Error(), "open") {
		t.Fatalf("missing screenshot image=%v err=%v", img, err)
	}
	malformed := filepath.Join(dir, "malformed.png")
	if err := os.WriteFile(malformed, []byte("not a PNG"), 0600); err != nil {
		t.Fatal(err)
	}
	if img, err := decodeScreenshot(malformed); img != nil || err == nil || !strings.Contains(err.Error(), "decode PNG") {
		t.Fatalf("corrupt screenshot image=%v err=%v", img, err)
	}
}
