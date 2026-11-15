package perfuncted

import (
	"image"
	"os"
	"path/filepath"
	"testing"
)

// A capture backend may return a non-nil image with no usable pixels. PNG
// encoding must fail without publishing or overwriting a prior screenshot,
// and the private temporary file must be removed on that failure.
func TestDiagnosticPNGEncodeFailureLeavesExistingScreenshotAndNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "screen.png")
	original := []byte("original private capture")
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writePNG(target, image.NewRGBA(image.Rectangle{})); err == nil {
		t.Fatal("empty image accepted as diagnostic PNG")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != string(original) {
		t.Fatalf("failed PNG write changed existing capture: %q %v", data, err)
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
}
