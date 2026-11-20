//go:build linux

package input

import (
	"os"
	"path/filepath"
	"testing"
)

func shortPrivateSocketDir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("/var/tmp", "pf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove private socket directory: %v", err)
		}
	})
	return dir
}

func shortPrivateSocketPath(t testing.TB, name string) string {
	t.Helper()
	return filepath.Join(shortPrivateSocketDir(t), name)
}
