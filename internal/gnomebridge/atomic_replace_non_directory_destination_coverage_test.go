//go:build linux

package gnomebridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Refuse to replace a non-directory GNOME extension target. A failed
// directory exchange must preserve both staged content and existing files.
func TestAtomicReplaceDirectoryRejectsExistingRegularFileWithoutMutation(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	target := filepath.Join(root, extensionUUID)
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "metadata.json"), []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing regular file"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := atomicReplaceDirectory(stage, target)
	if err == nil || !strings.Contains(err.Error(), "existing extension target is not a directory") {
		t.Fatalf("accepted non-directory extension target: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "existing regular file" {
		t.Fatalf("existing extension target mutated: %q %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(stage, "metadata.json"))
	if err != nil || string(got) != "staged" {
		t.Fatalf("staged extension mutated: %q %v", got, err)
	}
}

// An existing symlink must not be swapped away or followed during replacement.
func TestAtomicReplaceDirectoryPreservesExistingSymlinkAndTarget(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	external := filepath.Join(root, "external")
	dest := filepath.Join(root, extensionUUID)
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, "sentinel"), []byte("unaltered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, dest); err != nil {
		t.Fatal(err)
	}
	if err := atomicReplaceDirectory(stage, dest); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("accepted symlink target: %v", err)
	}
	if got, err := os.Readlink(dest); err != nil || got != external {
		t.Fatalf("destination symlink mutated: %q %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(external, "sentinel")); err != nil || string(got) != "unaltered" {
		t.Fatalf("symlink destination contents mutated: %q %v", got, err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("staging directory lost on rejected symlink: %v", err)
	}
}
