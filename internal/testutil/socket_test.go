package testutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestShortDirectoriesArePrivateAndCleaned(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	var socketDir, runtimeDir string
	t.Run("fixtures", func(t *testing.T) {
		path := ShortSocketPath(t, "wayland.sock")
		socketDir = filepath.Dir(path)
		runtimeDir = ShortRuntimeDir(t)
		if filepath.Dir(socketDir) != "/var/tmp" || filepath.Base(path) != "wayland.sock" || filepath.Dir(runtimeDir) != root {
			t.Fatalf("unexpected fixture locations: %q, %q", path, runtimeDir)
		}
		for _, dir := range []string{socketDir, runtimeDir} {
			info, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !info.IsDir() || info.Mode().Perm() != 0700 {
				t.Fatalf("fixture directory %q mode = %v", dir, info.Mode())
			}
		}
	})
	for _, dir := range []string{socketDir, runtimeDir} {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("fixture directory %q survived cleanup: %v", dir, err)
		}
	}
}
