package perfuncted

import (
	"image"
	"os"
	"path/filepath"
	"testing"
)

// A backend may return a nil image after a failed capture. Diagnostic
// generation must report failure rather than panicking or publishing a file.
func TestDiagnosticPNGWriterRejectsAbsentImagesWithoutPanicking(t *testing.T) {
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{name: "nil interface"},
		{name: "typed nil RGBA", img: (*image.RGBA)(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "capture.png")
			if err := writePNG(path, tc.img); err == nil {
				t.Fatal("missing screenshot accepted")
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("missing capture published file: %v", err)
			}
		})
	}
}
