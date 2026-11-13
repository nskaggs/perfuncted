package perfuncted

import (
	"os"
	"path/filepath"
	"testing"
)

// An attacker who can preplace a hard-linked PID entry must not make an
// unrelated inode authoritative evidence for managed-process cleanup.
// Metadata may be valid decimal text and mode 0600 while still being shared.
func TestSessionPIDMetadataRejectsForeignHardlinkOwnership(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, "unrelated-private-record")
	const privatePID = "74231\n"
	if err := os.WriteFile(foreign, []byte(privatePID), 0600); err != nil {
		t.Fatal(err)
	}
	linkedPID := filepath.Join(dir, "dbus.pid")
	if err := os.Link(foreign, linkedPID); err != nil {
		t.Fatal(err)
	}
	if data, err := readSessionPIDMetadata(linkedPID); err == nil {
		t.Fatalf("hard-linked ownership evidence accepted: %q", data)
	}
	if pid, err := readPIDFile(linkedPID); err == nil || pid != 0 {
		t.Fatalf("hard-linked PID admitted: %d %v", pid, err)
	}
	saved, err := os.ReadFile(foreign)
	if err != nil || string(saved) != privatePID {
		t.Fatalf("foreign file changed: %q %v", saved, err)
	}
	if err := os.Remove(linkedPID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linkedPID, []byte(" 74231 \n"), 0600); err != nil {
		t.Fatal(err)
	}
	pid, err := readPIDFile(linkedPID)
	if err != nil || pid != 74231 {
		t.Fatalf("isolated private PID rejected: %d %v", pid, err)
	}
}
