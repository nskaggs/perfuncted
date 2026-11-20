//go:build linux

package input

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// The state hierarchy and the token lock have separate write-permission
// boundaries. Neither may silently redirect persistent portal credentials to
// another root when the existing private directory becomes read-only.
func TestPortalTokenLockRejectsPrivateStoreWriteDenial(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory mode bits")
	}
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, string) string
	}{
		{
			name: "cannot create state directory",
			prepare: func(t *testing.T, root string) string {
				return filepath.Join(root, "state")
			},
		},
		{
			name: "cannot create lock file",
			prepare: func(t *testing.T, root string) string {
				state := filepath.Join(root, "state")
				if err := os.MkdirAll(filepath.Join(state, "perfuncted"), 0700); err != nil {
					t.Fatal(err)
				}
				return state
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := privatePortalTokenFixtureDir(t)
			state := tc.prepare(t, root)
			if err := os.Chmod(root, 0500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(root, 0700); err != nil {
					t.Errorf("restore private root: %v", err)
				}
			})
			// The lock-file test must retain traversability to the nested
			// application directory while denying its local creation.
			if tc.name == "cannot create lock file" {
				appDir := filepath.Join(state, "perfuncted")
				if err := os.Chmod(appDir, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(appDir, 0700) })
			}
			lock, err := lockPortalTokenStore(env.FromEnviron([]string{"XDG_STATE_HOME=" + state}))
			if lock != nil || !errors.Is(err, os.ErrPermission) {
				if lock != nil {
					_ = lock.Close()
				}
				t.Fatalf("unwritable private store accepted: lock=%v err=%v", lock, err)
			}
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(state, "perfuncted", portalRestoreLockFile)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("denied store created or replaced lock file: %v", err)
			}
		})
	}
}
