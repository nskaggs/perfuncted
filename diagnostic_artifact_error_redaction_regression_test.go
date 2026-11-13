package perfuncted

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Individual capture providers can return private screenshot paths, window
// titles, and other user-controlled text. Preserve the error for the caller,
// but persist only the artifact name and generic failure status in manifests.
func TestDiagnosticArtifactErrorDoesNotPersistProviderPrivateText(t *testing.T) {
	const secret = "private-window-title-and-auth-token-c47ad"
	cause := errors.New("screenshot capture denied for " + secret)
	manifest := &failureManifest{ArtifactErrors: make(map[string]string)}
	collector := &failureArtifactCollector{manifest: manifest}
	collector.capture("screenshot.png", func() error { return cause })
	if len(collector.errors) != 1 || !errors.Is(collector.errors[0], cause) {
		t.Fatalf("original capture failure lost: %v", collector.errors)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "screenshot capture denied") {
		t.Fatalf("persisted diagnostic manifest leaked provider failure: %s", data)
	}
	if manifest.ArtifactErrors["screenshot.png"] == "" || len(manifest.Artifacts) != 0 {
		t.Fatalf("capture failure status lost: %+v", manifest)
	}
}
