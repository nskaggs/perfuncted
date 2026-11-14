package clipboard

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Clipboard contents must remain private even if the caller uses a permissive
// process umask. Each active owner also needs a distinct staging file.
func TestWaylandInputPrivateModeAndIndependentOwnership(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	previous := syscall.Umask(0)
	defer syscall.Umask(previous)

	first, firstPath, err := waylandInput("first private selection")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	defer os.Remove(firstPath)
	second, secondPath, err := waylandInput("second private selection")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	defer os.Remove(secondPath)

	if firstPath == secondPath {
		t.Fatal("independent owners reused a staging file")
	}
	for _, path := range []string{firstPath, secondPath} {
		if filepath.Dir(path) != root {
			t.Fatalf("staging path outside controlled directory: %q", path)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0600 {
			t.Fatalf("staging file %q has mode %04o; want 0600", path, mode)
		}
	}
	firstBytes, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != "first private selection" || string(secondBytes) != "second private selection" {
		t.Fatal("clipboard staging files mixed owner contents")
	}
}
