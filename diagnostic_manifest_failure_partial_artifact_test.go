package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Bundle collection is best-effort, but failure to publish the manifest must
// be returned even when earlier artifacts succeeded. The atomic writer must
// not replace an existing directory or leave private staging files behind.
func TestFailureBundleManifestPublicationFailurePreservesCompletedArtifacts(t *testing.T) {
	s := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer s.Close()
	directory := t.TempDir()
	blocked := filepath.Join(directory, "manifest.json")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(blocked, "unrelated-private")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	actual, err := s.CaptureFailureBundle(context.Background(), FailureBundleOptions{Directory: directory, Operation: "redacted operation"})
	if actual != directory || err == nil || !strings.Contains(err.Error(), "manifest.json") {
		t.Fatalf("manifest publication failure was lost: path=%q err=%v", actual, err)
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("manifest collision damaged existing data: %q err=%v", data, readErr)
	}
	trace, traceErr := os.Stat(filepath.Join(directory, "trace.txt"))
	if traceErr != nil || !trace.Mode().IsRegular() || trace.Mode().Perm() != 0600 {
		t.Fatalf("successful trace artifact not retained privately: %+v err=%v", trace, traceErr)
	}
	assertNoDiagnosticTemporaryArtifacts(t, directory)
}

// Invalid callers must be rejected before any filesystem work, including
// when they request an explicit directory that has not yet been created.
func TestFailureBundleNilSessionAndContextCannotCreateArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uncreated")
	var absent *Session
	got, err := absent.CaptureFailureBundle(context.Background(), FailureBundleOptions{Directory: path})
	if got != "" || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil session returned path=%q err=%v", got, err)
	}
	s := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer s.Close()
	got, err = s.CaptureFailureBundle(nil, FailureBundleOptions{Directory: path})
	if got != "" || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil context returned path=%q err=%v", got, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid call created artifact directory: %v", err)
	}
}
