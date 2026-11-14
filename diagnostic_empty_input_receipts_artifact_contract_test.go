package perfuncted

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No input operations is an authoritative empty history. Optional failure
// diagnostics must serialize it as [] rather than null, without inventing
// actions, and must not include the file when receipts were not requested.
func TestFailureDiagnosticSerializesEmptyInputReceiptsAsArray(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	dir := t.TempDir()
	manifest := &failureManifest{ArtifactErrors: make(map[string]string)}
	_ = session.captureFailureArtifacts(context.Background(), dir, manifest, FailureBundleOptions{IncludeInputReceipts: true})
	path := filepath.Join(dir, "input-receipts.json")
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) != "[]" {
		t.Fatalf("empty input receipts artifact = %q, err=%v; want []", data, err)
	}
	found := false
	for _, artifact := range manifest.Artifacts {
		if artifact == "input-receipts.json" {
			found = true
		}
	}
	if !found {
		t.Fatal("empty input history missing from recorded artifacts")
	}
	second := t.TempDir()
	manifest = &failureManifest{ArtifactErrors: make(map[string]string)}
	_ = session.captureFailureArtifacts(context.Background(), second, manifest, FailureBundleOptions{})
	if _, err := os.Stat(filepath.Join(second, "input-receipts.json")); !os.IsNotExist(err) {
		t.Fatalf("unrequested input history written: %v", err)
	}
}
