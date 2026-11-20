package perfuncted

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Once the private runtime directory exists, an invalid caller-selected log
// root must still fail startup without creating or redirecting any log files.
func TestManagedRuntimePreparationRejectsNonDirectoryLogRoot(t *testing.T) {
	privateRoot := t.TempDir()
	t.Setenv("TMPDIR", privateRoot)
	invalidLogRoot := filepath.Join(t.TempDir(), "not-a-log-directory")
	const original = "unrelated private file"
	if err := os.WriteFile(invalidLogRoot, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	var runtimeDir, logDir string
	err := prepareSessionRuntimeDirs(&runtimeDir, &logDir, invalidLogRoot)
	if err == nil || !strings.Contains(err.Error(), "session: create logs:") {
		t.Fatalf("invalid log root not reported: %v", err)
	}
	if logDir != "" {
		t.Fatalf("failed preparation published log directory %q", logDir)
	}
	if runtimeDir == "" || !isPrivateManagedSessionDir(runtimeDir) {
		t.Fatalf("failed preparation omitted owned runtime cleanup handle: %q", runtimeDir)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(runtimeDir); err != nil {
			t.Errorf("remove owned runtime: %v", err)
		}
	})
	content, readErr := os.ReadFile(invalidLogRoot)
	if readErr != nil || string(content) != original {
		t.Fatalf("failed startup modified unrelated file: content=%q err=%v", content, readErr)
	}
}

// A process can exit between directory enumeration and stat inspection.
// Missing metadata must not be interpreted as an owned live process.
func TestProcessSnapshotRejectsDisappearedStatFile(t *testing.T) {
	root := t.TempDir()
	processDir := filepath.Join(root, "391")
	if err := os.Mkdir(processDir, 0700); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readProcessSnapshotAt(root, 391)
	if !errors.Is(err, os.ErrNotExist) || snapshot != (processSnapshot{}) {
		t.Fatalf("disappeared process metadata acquired identity: %+v err=%v", snapshot, err)
	}
}
