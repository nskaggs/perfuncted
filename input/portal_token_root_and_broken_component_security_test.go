//go:build linux

package input

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"golang.org/x/sys/unix"
)

// The filesystem root is not a private application-owned storage directory.
// Opening it directly must fail before a token lock or any credential can be
// created in a world-visible directory.
func TestPortalPrivateDirectoryRejectsFilesystemRoot(t *testing.T) {
	fd, err := openPrivatePortalDirectory("/")
	if fd >= 0 {
		_ = unix.Close(fd)
		t.Fatalf("filesystem root accepted as private token directory: fd=%d", fd)
	}
	if err == nil || !strings.Contains(err.Error(), "invalid component") {
		t.Fatalf("filesystem root rejection=%v", err)
	}
}

// A dangling state path symlink must not be followed or replaced. In
// particular, mkdir-on-miss must not turn a failed O_NOFOLLOW traversal into
// a writable location for a restore credential.
func TestPortalTokenStoreRejectsDanglingStateSymlinkWithoutCreatingGrant(t *testing.T) {
	root := privatePortalTokenFixtureDir(t)
	link := filepath.Join(root, "dangling")
	target := filepath.Join(root, "missing-target")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	rt := env.FromEnviron([]string{"XDG_STATE_HOME=" + filepath.Join(link, "state")})
	lock, err := lockPortalTokenStore(rt)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("dangling state symlink yielded credential store")
	}
	if err == nil {
		t.Fatal("dangling state symlink accepted")
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("foreign symlink was replaced: info=%v err=%v", info, err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("untrusted target materialized: %v", err)
	}
}
