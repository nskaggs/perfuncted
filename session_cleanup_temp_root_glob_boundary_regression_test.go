package perfuncted

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TMPDIR is a literal filesystem path, not a glob expression. A bracket in
// a caller-owned temp path must not make live cleanup skip its actual runtime.
func TestStaleRuntimeDiscoverySupportsLiteralMetacharactersInTempRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "private[unfinished")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	owned := filepath.Join(root, "perfuncted-xdg-a")
	if err := os.Mkdir(owned, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := staleSessionDirectories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, owned) {
		t.Fatalf("cleanup skipped managed runtime under literal TMPDIR %q: %+v", root, got)
	}
}

func TestStaleRuntimeDiscoveryDoesNotInterpretTempRootPatterns(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "sandbox[ab]")
	foreign := filepath.Join(parent, "sandboxa")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	owned := filepath.Join(root, "perfuncted-xdg-owned")
	intruder := filepath.Join(foreign, "perfuncted-xdg-foreign")
	for _, dir := range []string{owned, intruder} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	got, err := staleSessionDirectories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, owned) || slices.Contains(got, intruder) {
		t.Fatalf("cleanup glob escaped literal root: owned=%q foreign=%q discovered=%v", owned, intruder, got)
	}
}

// Cleanup is permitted to inspect actual managed runtime directories, not
// old same-prefix files or symlinks that happen to appear in TMPDIR.
func TestStaleRuntimeDiscoveryIgnoresSamePrefixFilesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	owned := filepath.Join(root, "perfuncted-xdg-owned")
	file := filepath.Join(root, "perfuncted-xdg-not-a-runtime")
	link := filepath.Join(root, "perfuncted-xdg-link")
	if err := os.Mkdir(owned, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("do not delete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(owned, link); err != nil {
		t.Fatal(err)
	}
	got, err := staleSessionDirectories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, owned) || slices.Contains(got, file) || slices.Contains(got, link) {
		t.Fatalf("cleanup accepted non-directories: %v", got)
	}
}

// A matching name is not enough to own a filesystem object. A runtime that
// disappeared or was replaced after discovery must not be deleted if the
// replacement is now a same-prefix regular file or symlink.
func TestReapSessionDirRefusesNondirectoryReplacement(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	outside := filepath.Join(root, "private-original")
	if err := os.WriteFile(outside, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"regular file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(root, "perfuncted-xdg-"+kind)
			var err error
			if kind == "regular file" {
				err = os.WriteFile(path, []byte("keep this"), 0600)
			} else {
				err = os.Symlink(outside, path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := reapSessionDir(context.Background(), path); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("cleanup deleted non-directory %q: %v", path, err)
			}
			if data, err := os.ReadFile(outside); err != nil || string(data) != "preserve" {
				t.Fatalf("foreign file changed: %q %v", data, err)
			}
		})
	}
}
