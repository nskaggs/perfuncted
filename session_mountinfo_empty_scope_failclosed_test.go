package perfuncted

import "testing"

// An empty managed-runtime path must never become a wildcard over all host
// mounts. Cleanup is allowed to inspect only an explicitly scoped runtime.
func TestSessionMountInfoEmptyScopeNeverSelectsHostMounts(t *testing.T) {
	mountinfo := []byte("1 0 0:1 / / rw - tmpfs tmpfs rw\n2 1 0:2 / /var/tmp/private rw - tmpfs tmpfs rw\n")
	if matches := parseSessionSubdirMounts("", mountinfo); len(matches) != 0 {
		t.Fatalf("empty runtime path selected host mounts: %v", matches)
	}
	if matches := parseSessionSubdirMounts("/var/tmp/private-other", mountinfo); len(matches) != 0 {
		t.Fatalf("sibling runtime path selected foreign mounts: %v", matches)
	}
}
