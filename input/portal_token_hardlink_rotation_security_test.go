//go:build linux

package input

import (
	"os"
	"path/filepath"
	"testing"
)

// Replacement must never truncate a foreign hard-linked file. An untrusted
// previous token record can share an inode with unrelated user-owned data.
func TestPortalTokenRotationReplacesHardlinkWithoutModifyingForeignFile(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, lockErr := lockPortalTokenStore(rt)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	defer lock.Close()

	outside := filepath.Join(t.TempDir(), "unrelated-private-data")
	original := []byte("foreign secret must stay intact\n")
	if err := os.WriteFile(outside, original, 0600); err != nil {
		t.Fatal(err)
	}
	stored := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
	if err := os.Link(outside, stored); err != nil {
		t.Fatal(err)
	}

	if err := lock.store("fresh-authorized-token"); err != nil {
		t.Fatalf("atomic replacement of untrusted hardlink: %v", err)
	}
	gotForeign, err := os.ReadFile(outside)
	if err != nil || string(gotForeign) != string(original) {
		t.Fatalf("foreign hardlink target modified: got=%q err=%v", gotForeign, err)
	}
	if stat, statErr := os.Stat(stored); statErr != nil || !stat.Mode().IsRegular() || stat.Mode().Perm() != 0600 {
		t.Fatalf("new token not private regular file: stat=%v err=%v", stat, statErr)
	}
	got, err := lock.consume()
	if err != nil || got != "fresh-authorized-token" {
		t.Fatalf("new token not consumed: token=%q err=%v", got, err)
	}
	if _, statErr := os.Lstat(stored); !os.IsNotExist(statErr) {
		t.Fatalf("consumed token still present: %v", statErr)
	}
	gotForeign, err = os.ReadFile(outside)
	if err != nil || string(gotForeign) != string(original) {
		t.Fatalf("foreign file changed after consume: %q %v", gotForeign, err)
	}
}
