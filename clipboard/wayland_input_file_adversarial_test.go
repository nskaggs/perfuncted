package clipboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWaylandInputPreservesExactBytesAndRewinds(t *testing.T) {
	text := "first\x00line\nπ\n" + strings.Repeat("x", 128*1024)
	f, path, err := waylandInput(text)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	defer os.Remove(path)
	if !strings.HasPrefix(filepath.Base(path), "perfuncted-clipboard-input-") {
		t.Fatalf("unexpected temporary name: %q", path)
	}
	if pos, err := f.Seek(0, 1); err != nil || pos != 0 {
		t.Fatalf("input not rewound: offset=%d err=%v", pos, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != text {
		t.Fatalf("clipboard input bytes differ: got %d bytes, want %d", len(got), len(text))
	}
}

func TestWaylandInputReturnsCreateFailureWithoutFile(t *testing.T) {
	root := t.TempDir()
	absent := filepath.Join(root, "no-such-directory")
	t.Setenv("TMPDIR", absent)
	f, path, err := waylandInput("secret")
	if f != nil || path != "" || err == nil || !strings.Contains(err.Error(), "clipboard set: create input:") || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("create failure = (%v, %q, %v)", f, path, err)
	}
}
