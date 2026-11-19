package perfuncted

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A log parent can be a real, private directory while refusing creation of
// new children (for example a read-only or permission-revoked log volume).
// Failure must not be translated into success or redirect logs elsewhere.
func TestSessionLogCreationRejectsUnwritablePrivateParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can bypass directory mode bits")
	}
	root := filepath.Join(t.TempDir(), "readonly-private-logs")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(root, 0700); err != nil {
			t.Errorf("restore private log directory: %v", err)
		}
	})
	dir, err := createSessionLogDir(root)
	if dir != "" || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unwritable log directory was accepted: dir=%q err=%v", dir, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed log creation left artifacts: entries=%v err=%v", entries, err)
	}
}
