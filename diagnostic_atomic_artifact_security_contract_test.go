package perfuncted

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Diagnostic artifacts can contain private application titles and screenshots.
// Atomic replacement must not follow an existing symlink out of the bundle.
func TestDiagnosticWriteAtomicReplacesSymlinkWithoutTouchingForeignFile(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(t.TempDir(), "unrelated-private-document")
	if err := os.WriteFile(foreign, []byte("do not modify"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.Symlink(foreign, path); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, []byte("private diagnostic"), 0600); err != nil {
		t.Fatalf("atomic manifest replacement: %v", err)
	}
	got, err := os.ReadFile(foreign)
	if err != nil || string(got) != "do not modify" {
		t.Fatalf("foreign file changed: %q %v", got, err)
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != "private diagnostic" {
		t.Fatalf("manifest bytes wrong: %q %v", got, err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatalf("manifest is not private regular file: %v %v", info, err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".perfuncted-atomic-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("stale diagnostic staging files: %v %v", matches, err)
	}
}

func TestDiagnosticWriteAtomicFailsClosedWithoutWritableParent(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent", "manifest.json")
	if err := writeAtomic(missing, []byte("private"), 0600); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing parent returned %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("manifest created after invalid parent: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed write left files behind: %v %v", entries, err)
	}
}

func TestDiagnosticWritePNGRejectsMissingParentAndDoesNotWriteOutsideBundle(t *testing.T) {
	dir := t.TempDir()
	err := writePNG(filepath.Join(dir, "missing", "screen.png"), nil)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing screenshot directory returned %v", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("failed screenshot created files: %v %v", entries, readErr)
	}
	if strings.Contains(err.Error(), "private") {
		t.Fatalf("unexpected diagnostic data in error: %v", err)
	}
}
