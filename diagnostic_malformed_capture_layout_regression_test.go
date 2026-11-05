package perfuncted

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Diagnostic screenshots bypass the public ScreenBundle, but must enforce the
// same backing-image validity before PNG encoding sensitive desktop pixels.
func TestDiagnosticScreenshotRejectsTruncatedPixelBufferWithoutOverwriting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame image.Image
	}{
		{"RGBA", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 11)}},
		{"Gray", &image.Gray{Rect: image.Rect(0, 0, 2, 2), Stride: 2, Pix: make([]byte, 3)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: tc.frame}, nil, nil, nil, nil)
			defer session.Close()
			dir := t.TempDir()
			path := filepath.Join(dir, "screenshot.png")
			if err := os.WriteFile(path, []byte("original-private-capture"), 0600); err != nil {
				t.Fatal(err)
			}
			err := session.captureScreenshot(context.Background(), dir)
			if err == nil || !strings.Contains(err.Error(), "invalid capture") {
				t.Fatalf("malformed diagnostic frame accepted: %v", err)
			}
			bytes, readErr := os.ReadFile(path)
			if readErr != nil || string(bytes) != "original-private-capture" {
				t.Fatalf("existing capture overwritten: bytes=%q err=%v", bytes, readErr)
			}
			assertNoDiagnosticTemporaryArtifacts(t, dir)
			if errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected filesystem failure rather than capture validation: %v", err)
			}
		})
	}
}
