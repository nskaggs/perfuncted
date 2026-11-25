package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessUsesRuntimeDirAt(t *testing.T) {
	procRoot := t.TempDir()
	pidDir := filepath.Join(procRoot, "123")
	if err := os.Mkdir(pidDir, 0o700); err != nil {
		t.Fatalf("mkdir process dir: %v", err)
	}
	environPath := filepath.Join(pidDir, "environ")

	writeEnvironment := func(values ...string) {
		t.Helper()
		if err := os.WriteFile(environPath, []byte(strings.Join(values, "\x00")+"\x00"), 0o600); err != nil {
			t.Fatalf("write environ: %v", err)
		}
	}

	writeEnvironment("PATH=/usr/bin", "XDG_RUNTIME_DIR=/tmp/perfuncted-xdg-owned")
	if !processUsesRuntimeDirAt(procRoot, 123, "/tmp/perfuncted-xdg-owned") {
		t.Fatal("matching runtime directory was not recognized")
	}
	if processUsesRuntimeDirAt(procRoot, 123, "/tmp/perfuncted-xdg-other") {
		t.Fatal("mismatched runtime directory was accepted")
	}

	writeEnvironment("PATH=/usr/bin", "OTHER=/tmp/perfuncted-xdg-owned")
	if processUsesRuntimeDirAt(procRoot, 123, "/tmp/perfuncted-xdg-owned") {
		t.Fatal("runtime directory value under another key was accepted")
	}
	if processUsesRuntimeDirAt(procRoot, 999, "/tmp/perfuncted-xdg-owned") {
		t.Fatal("missing process was accepted")
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
