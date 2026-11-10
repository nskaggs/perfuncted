package perfuncted

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Managed runtime names are not authority to overwrite arbitrary files:
// a preexisting symlink must never redirect generated Sway config writes.
func TestManagedSwayConfigWriteCannotClobberSymlinkTarget(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	foreign := filepath.Join(t.TempDir(), "foreign-settings")
	if err := os.WriteFile(foreign, []byte("preserve settings"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(runtimeDir, "sway.conf")
	if err := os.Symlink(foreign, target); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: runtimeDir}
	path, err := infra.writeEmbeddedConfig("configs/headless.conf", image.Pt(1920, 1080))
	// Rejecting the unsafe path is acceptable, as is safely replacing only
	// the symlink itself. Following the symlink is never acceptable.
	data, readErr := os.ReadFile(foreign)
	if readErr != nil || string(data) != "preserve settings" {
		t.Fatalf("generated compositor config changed foreign file %q: %v", data, readErr)
	}
	if err == nil {
		if path != target {
			t.Fatalf("unexpected config path: %q", path)
		}
		info, statErr := os.Lstat(path)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unsafe generated config: %+v %v", info, statErr)
		}
		saved, readErr := os.ReadFile(path)
		if readErr != nil || !strings.Contains(string(saved), "resolution 1920x1080") {
			t.Fatalf("wrong isolated config: %v", readErr)
		}
	}
}

// PID metadata is equally sensitive: process startup is not authorized to
// change arbitrary user files through a symlink with the expected PID name.
func TestManagedChildPIDWriteCannotClobberSymlinkTarget(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	foreign := filepath.Join(t.TempDir(), "user-file")
	if err := os.WriteFile(foreign, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	pidfile := filepath.Join(runtimeDir, "child.pid")
	if err := os.Symlink(foreign, pidfile); err != nil {
		t.Fatal(err)
	}
	(&sessionInfra{xdgDir: runtimeDir}).writeChildPID("child.pid", os.Getpid())
	saved, readErr := os.ReadFile(foreign)
	if readErr != nil || string(saved) != "preserve" {
		t.Fatalf("PID writer changed foreign file %q: %v", saved, readErr)
	}
}

// PID filenames are not a path API. Even a syntactically valid relative path
// must never escape the directory where this session owns metadata.
func TestManagedChildPIDWriteRejectsTraversalAndEmbeddedPath(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	foreignDir, err := os.MkdirTemp(filepath.Dir(runtimeDir), "perfuncted-owned-outside-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(foreignDir) })
	foreign := filepath.Join(foreignDir, "foreign.pid")
	if err := os.WriteFile(foreign, []byte("do not replace"), 0600); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: runtimeDir}
	for _, name := range []string{"", ".", "..", "nested/child.pid", "../" + filepath.Base(foreignDir) + "/foreign.pid", "child\x00.pid"} {
		infra.writeChildPID(name, os.Getpid())
		got, readErr := os.ReadFile(foreign)
		if readErr != nil || string(got) != "do not replace" {
			t.Fatalf("unsafe pid name %q changed foreign file: %q %v", name, got, readErr)
		}
	}
	if entries, readErr := os.ReadDir(runtimeDir); readErr != nil || len(entries) != 0 {
		t.Fatalf("invalid PID paths left runtime files: %v %v", entries, readErr)
	}
}

// Existing hard links must not be modified in place: the writer must publish
// a fresh inode rather than opening a foreign inode through an alias.
func TestManagedSessionFileWritesReplaceOnlyOwnedDirectoryEntry(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	foreign := filepath.Join(runtimeDir, "foreign")
	if err := os.WriteFile(foreign, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(runtimeDir, "child.pid")
	if err := os.Link(foreign, alias); err != nil {
		t.Skipf("hardlink unavailable: %v", err)
	}
	infra := &sessionInfra{xdgDir: runtimeDir}
	infra.writeChildPID("child.pid", os.Getpid())
	retained, readErr := os.ReadFile(foreign)
	if readErr != nil || string(retained) != "retain" {
		t.Fatalf("write changed hardlink sibling: %q %v", retained, readErr)
	}
	pid, pidErr := readPIDFile(alias)
	if pidErr != nil || pid != os.Getpid() {
		t.Fatalf("fresh PID metadata unavailable: %d %v", pid, pidErr)
	}
	info, statErr := os.Stat(alias)
	if statErr != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("PID file mode not private: %v %v", info, statErr)
	}
	matches, globErr := filepath.Glob(filepath.Join(runtimeDir, ".perfuncted-managed-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("pending atomic writes leaked: %v %v", matches, globErr)
	}
}
