package perfuncted

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A caller-selected short /tmp root must not be duplicated in the cleanup
// search list. This test only inspects roots; it never allocates there.
func TestManagedSessionExplicitShortTempRootIsNotDuplicated(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	roots := managedSessionTempRoots()
	if len(roots) != 1 || roots[0] != "/tmp" {
		t.Fatalf("short root should be unique: %q", roots)
	}
}

// Nested Wayland startup requires an actual parent socket. A missing host
// session must be rejected before opening any log or spawning a compositor.
func TestNestedManagedSessionWithoutHostWaylandRefusesSpawn(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("DISPLAY", "")
	dir := t.TempDir()
	infra := &sessionInfra{ctx: context.Background(), xdgDir: dir, logDir: dir, wlDisplay: "wayland-1"}
	err := infra.launchSway(context.Background(), filepath.Join(dir, "unused.conf"), sessionModeNested)
	if err == nil || !strings.Contains(err.Error(), "requires a host Wayland socket") || infra.swayCmd != nil {
		t.Fatalf("nested startup authorized absent host: err=%v process=%v", err, infra.swayCmd)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "sway-session.log")); !os.IsNotExist(statErr) {
		t.Fatalf("nested startup created a log before host validation: %v", statErr)
	}
}
