//go:build integration && linux

package flatpaktest

import (
	"os"
	"path/filepath"
	"testing"
)

// Local fast-path for debugging a previously built bundle; the regular full
// integration test always tests the upgrade against its newly built bundle.
func TestFlatpakUpgradeOfPrebuiltCandidate(t *testing.T) {
	candidate := os.Getenv("PF_FLATPAK_TEST_BUNDLE")
	if candidate == "" {
		t.Skip("set PF_FLATPAK_TEST_BUNDLE to an already built Flatpak to debug upgrades")
	}
	if _, err := os.Stat(candidate); err != nil {
		t.Fatal(err)
	}
	// Flatpak's D-Bus proxy creates Unix sockets below XDG_RUNTIME_DIR;
	// Go's nested t.TempDir path can exceed the Linux 108-byte socket limit.
	testRoot, err := os.MkdirTemp("/var/tmp", "pf-upgrade-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(testRoot) })
	dbusHome := filepath.Join(testRoot, "dbus-home")
	if err := mustWriteFlatpakServiceFile(dbusHome); err != nil {
		t.Fatal(err)
	}
	session := mustStartDBusSession(t, dbusHome)
	t.Cleanup(session.close)
	testRealFlatpakReleaseUpgrade(t, session.address, mustRepoRoot(t), testRoot, candidate)
}
