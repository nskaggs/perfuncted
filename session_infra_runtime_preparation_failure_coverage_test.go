package perfuncted

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An invalid caller-selected temporary root must stop managed startup before
// any log allocation. The setup wrapper must preserve the underlying error
// and leave both output paths uninitialized, not fall back to an unrelated root.
func TestManagedRuntimePreparationRejectsMissingPreferredTempRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-preferred-temporary-root")
	t.Setenv("TMPDIR", missing)
	logs := filepath.Join(t.TempDir(), "uncreated-private-logs")
	var runtimeDir, logDir string
	err := prepareSessionRuntimeDirs(&runtimeDir, &logDir, logs)
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "managed runtime") {
		t.Fatalf("missing caller temp root: %v", err)
	}
	if runtimeDir != "" || logDir != "" {
		t.Fatalf("failed startup published runtime=%q logs=%q", runtimeDir, logDir)
	}
	if _, statErr := os.Lstat(logs); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed startup allocated log root: %v", statErr)
	}
}
