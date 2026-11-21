package perfuncted

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Non-nil screenshots can still have no encodable pixels. A failed PNG
// encoder must leave neither a published capture nor a temporary artifact.
func TestDiagnosticPNGEncodingRejectsZeroSizedImageAndCleansStaging(t *testing.T) {
	for _, rect := range []image.Rectangle{
		image.Rect(0, 0, 0, 0),
		image.Rect(0, 0, 4, 0),
		image.Rect(0, 0, 0, 4),
	} {
		t.Run(rect.String(), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "screen.png")
			err := writePNG(path, image.NewRGBA(rect))
			if err == nil || !strings.Contains(err.Error(), "invalid image size") {
				t.Fatalf("zero-pixel screenshot unexpectedly encoded: %v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("zero-pixel screenshot published: %v", err)
			}
			assertNoDiagnosticTemporaryArtifacts(t, dir)
		})
	}
}
