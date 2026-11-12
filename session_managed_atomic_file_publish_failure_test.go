package perfuncted

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The atomic managed-file publisher must never expose a partial replacement
// or leave private staging files when publication fails. A directory at the
// destination forces rename failure after the temporary file has been written.
func TestManagedSessionFileAtomicPublishFailureLeavesNoStagedData(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "runtime.pid")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve unrelated state"), 0600); err != nil {
		t.Fatal(err)
	}
	secret := bytes.Repeat([]byte("new private process metadata\n"), 4096)
	if err := writeManagedSessionFile(target, secret, 0600); err == nil {
		t.Fatal("published file over existing directory")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "preserve unrelated state" {
		t.Fatalf("failed publication modified target directory: %q %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "runtime.pid" || !entries[0].IsDir() {
		t.Fatalf("failed publication left staged sensitive data: %v %v", entries, err)
	}
	// After the obstructing directory is removed, the same path must remain
	// usable for a correctly private, complete replacement.
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedSessionFile(target, secret, 0600); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(saved, secret) {
		t.Fatalf("successful replacement incomplete: bytes=%d err=%v", len(saved), err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("published metadata not private: %v %v", info, err)
	}
	entries, err = os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "runtime.pid" {
		t.Fatalf("successful publication left temporary files: %v %v", entries, err)
	}
}

func TestManagedSessionFileMissingParentDoesNotCreatePartialDestination(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "missing", "child.pid")
	if err := writeManagedSessionFile(absent, []byte("private"), 0600); err == nil {
		t.Fatal("writing into absent parent succeeded")
	}
	if _, err := os.Stat(filepath.Dir(absent)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed write created unexpected directory: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed write leaked files: %v %v", entries, err)
	}
}
