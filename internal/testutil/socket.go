// Package testutil contains shared private test fixtures.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// ShortSocketDir creates a private directory with a short path for Unix sockets.
// It deliberately uses /var/tmp instead of the potentially long caller TMPDIR.
func ShortSocketDir(t testing.TB) string {
	t.Helper()
	return shortDir(t, "/var/tmp", "socket")
}

// ShortSocketPath returns a path in a fresh private socket directory.
func ShortSocketPath(t testing.TB, name string) string {
	t.Helper()
	return filepath.Join(ShortSocketDir(t), name)
}

// ShortRuntimeDir creates a private directory under the current temporary root.
// Unlike socket fixtures, runtime cleanup tests need to respect TMPDIR.
func ShortRuntimeDir(t testing.TB) string {
	t.Helper()
	return shortDir(t, os.TempDir(), "runtime")
}

func shortDir(t testing.TB, parent, kind string) string {
	t.Helper()
	dir, err := os.MkdirTemp(parent, "pf-")
	if err != nil {
		t.Fatalf("create short %s directory: %v", kind, err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove short %s directory: %v", kind, err)
		}
	})
	return dir
}
