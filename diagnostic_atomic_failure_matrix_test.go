package perfuncted

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertNoDiagnosticTemporaryArtifacts(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".perfuncted-atomic-") || strings.HasPrefix(entry.Name(), ".perfuncted-png-") {
			t.Fatalf("temporary diagnostic artifact remained after operation: %s", entry.Name())
		}
	}
}

func TestDiagnosticAtomicReplacementPreservesPermissionsAndCompleteBytes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(target, []byte("old private manifest"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("{\"revision\":\"new\"}\n"), bytes.Repeat([]byte("x"), 256*1024)...)
	if err := writeAtomic(target, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("replacement data mismatch: got %d bytes, want %d", len(got), len(payload))
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private diagnostic mode = %o, want 600", info.Mode().Perm())
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
}

func TestDiagnosticAtomicRenameFailurePreservesTargetAndReapsStaging(t *testing.T) {
	dir := t.TempDir()
	// An existing directory cannot be replaced by a staged regular file.
	target := filepath.Join(dir, "existing-directory")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "important")
	if err := os.WriteFile(marker, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(target, []byte("replacement"), 0o600); err == nil {
		t.Fatal("writeAtomic replaced an existing directory")
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "untouched" {
		t.Fatalf("existing target mutated: %q %v", got, err)
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
}

func TestDiagnosticPNGFailedDestinationPreservesOriginalAndCleansStaging(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing-directory")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "important")
	if err := os.WriteFile(marker, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writePNG(target, image.NewRGBA(image.Rect(0, 0, 2, 2))); err == nil {
		t.Fatal("PNG replaced an existing directory")
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "untouched" {
		t.Fatalf("existing target mutated: %q %v", got, err)
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
	if err := writePNG(filepath.Join(dir, "screen.png"), image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(dir, "screen.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := png.Decode(file); err != nil {
		t.Fatalf("written PNG invalid: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "screen.png"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("PNG file permissions %o, want 600", info.Mode().Perm())
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
}

func TestDiagnosticAtomicMissingDirectoryNeverCreatesTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "missing", "manifest.json")
	if err := writeAtomic(target, []byte("secret"), 0o600); !os.IsNotExist(err) {
		t.Fatalf("writeAtomic to missing dir: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("unexpected partial target: %v", err)
	}
	if err := writePNG(filepath.Join(dir, "missing", "screen.png"), image.NewRGBA(image.Rect(0, 0, 1, 1))); !os.IsNotExist(err) {
		t.Fatalf("writePNG to missing dir: %v", err)
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
}
