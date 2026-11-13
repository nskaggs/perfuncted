//go:build linux

package input

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// The lock file is a security boundary, not just a mutual-exclusion hint.
// Another hard link makes its ownership ambiguous even when both links are
// private regular files; no authorization store may open through that link.
func TestPortalRestoreLockRejectsHardlinkedPrivateLockFile(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	initial, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreLockFile)
	external := filepath.Join(privatePortalTokenFixtureDir(t), "foreign-hardlink")
	if err := os.Link(lockPath, external); err != nil {
		t.Fatal(err)
	}
	lock, err := lockPortalTokenStore(rt)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("hard-linked lock admitted")
	}
	if err == nil {
		t.Fatal("hard-linked lock was not rejected")
	}
	original, readErr := os.ReadFile(external)
	if readErr != nil || len(original) != 0 {
		t.Fatalf("external lock modified: %q %v", original, readErr)
	}
	if err := os.Remove(external); err != nil {
		t.Fatal(err)
	}
	recovered, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatalf("hardlink removal did not restore access: %v", err)
	}
	defer recovered.Close()
	if err := recovered.store("authorized-after-recovery"); err != nil {
		t.Fatal(err)
	}
	if token, err := recovered.consume(); err != nil || token != "authorized-after-recovery" {
		t.Fatalf("recovered store changed grant: %q %v", token, err)
	}
}

// A state path supplied by the environment is never permitted to redirect
// into a relative directory, even if the caller has a valid HOME fallback.
func TestPortalRestoreStateHomeRelativePathFailsClosedWithoutFallback(t *testing.T) {
	root := privatePortalTokenFixtureDir(t)
	rt := env.FromEnviron([]string{"HOME=" + root, "XDG_STATE_HOME=relative/private"})
	lock, err := lockPortalTokenStore(rt)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("relative state root admitted")
	}
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("relative state root did not fail validation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".local", "state", "perfuncted")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid explicit state home silently fell back to HOME: %v", err)
	}
}
