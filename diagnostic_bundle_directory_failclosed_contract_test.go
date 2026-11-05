package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Bundle creation can happen after a failed UI interaction and must never
// write diagnostic material outside the requested directory.
func TestFailureBundleRejectsUnwritableExplicitDestinationBeforeCollecting(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	root := t.TempDir()
	blocker := filepath.Join(root, "file-not-directory")
	if err := os.WriteFile(blocker, []byte("keep unrelated content"), 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(blocker, "private-diagnostics")
	path, err := session.CaptureFailureBundle(context.Background(), FailureBundleOptions{Directory: destination, Operation: "private operation"})
	if err == nil || path != "" || !strings.Contains(err.Error(), "create failure bundle directory") {
		t.Fatalf("invalid directory accepted: path=%q error=%v", path, err)
	}
	data, readErr := os.ReadFile(blocker)
	if readErr != nil || string(data) != "keep unrelated content" {
		t.Fatalf("outside file modified: data=%q error=%v", data, readErr)
	}

}

func TestFailureBundleRejectsUnavailableAutomaticTempDirectory(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	inaccessible := filepath.Join(t.TempDir(), "missing-temp-directory")
	t.Setenv("TMPDIR", inaccessible)
	path, err := session.CaptureFailureBundle(context.Background(), FailureBundleOptions{Operation: "private operation"})
	if path != "" || !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "create failure bundle directory") {
		t.Fatalf("missing TMPDIR accepted: path=%q error=%v", path, err)
	}
	if _, statErr := os.Stat(inaccessible); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failure created temp storage: %v", statErr)
	}
}
