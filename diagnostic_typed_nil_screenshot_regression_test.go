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

// Diagnostic collection is best-effort. An untrusted capture backend returning
// a typed nil image must produce an artifact error, not crash collection.
func TestDiagnosticScreenshotRejectsTypedNilWithoutPublishingArtifact(t *testing.T) {
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: (*image.RGBA)(nil)}, nil, nil, nil, nil)
	defer session.Close()
	dir := t.TempDir()
	err := session.captureScreenshot(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "nil image") {
		t.Fatalf("typed nil screenshot accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "screenshot.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid screenshot published: %v", err)
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
}
