//go:build linux

package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Parent directories may be shared only under the root-owned sticky-directory
// exception. An attacker-writable parent must not redirect the restore token.
func TestPortalTokenStatePathRejectsUntrustedStickyNonRootParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "shared-parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 01777); err != nil {
		t.Fatal(err)
	}
	_, err := openPrivatePortalDirectory(filepath.Join(parent, "private", "perfuncted"))
	if err == nil || !strings.Contains(err.Error(), "group or world writable") {
		t.Fatalf("untrusted sticky owner accepted: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(parent, "private")); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe parent gained private child: %v", statErr)
	}
}

func TestPortalTokenStatePathAllowsRootOwnedStickyTempParent(t *testing.T) {
	if info, err := os.Stat("/tmp"); err != nil || info.Mode().Perm()&0002 == 0 || info.Mode()&os.ModeSticky == 0 {
		t.Skip("/tmp is not a sticky writable parent")
	}
	path := filepath.Join(privatePortalTokenFixtureDir(t), "state", "perfuncted")
	fd, err := openPrivatePortalDirectory(path)
	if err != nil {
		t.Fatalf("private directory beneath sticky /tmp rejected: %v", err)
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		t.Fatal(err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0777 != 0700 || stat.Uid != uint32(unix.Geteuid()) {
		t.Fatalf("wrong private directory ownership/mode: uid=%d mode=%o", stat.Uid, stat.Mode)
	}
}

func TestPortalTokenStatePathRejectsRelativeAndSymlinkComponent(t *testing.T) {
	if fd, err := openPrivatePortalDirectory("relative/state/perfuncted"); err == nil {
		_ = unix.Close(fd)
		t.Fatal("relative private state directory accepted")
	}
	root := t.TempDir()
	real := filepath.Join(root, "actual")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	fd, err := openPrivatePortalDirectory(filepath.Join(alias, "perfuncted"))
	if err == nil {
		_ = unix.Close(fd)
		t.Fatal("symlinked state parent accepted")
	}
	if _, statErr := os.Stat(filepath.Join(real, "perfuncted")); !os.IsNotExist(statErr) {
		t.Fatalf("symlink traversal created private directory: %v", statErr)
	}
}
