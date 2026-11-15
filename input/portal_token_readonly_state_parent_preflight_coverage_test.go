//go:build linux

package input

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"golang.org/x/sys/unix"
)

// A private but read-only state parent is not permission to redirect portal
// restore-token storage to an alternate directory. The failing mkdir must not
// leave a lockfile or token behind, even when all ancestors can be traversed.
func TestPortalTokenStoreRejectsReadOnlyStateParentWithoutArtifacts(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0700) })
	rt := env.FromEnviron([]string{"XDG_STATE_HOME=" + parent})
	lock, err := lockPortalTokenStore(rt)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("read-only state parent opened token store")
	}
	if !errors.Is(err, unix.EACCES) {
		t.Fatalf("read-only parent not rejected by mkdir: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(parent, "perfuncted")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected parent contains portal artifacts: %v", statErr)
	}
}
