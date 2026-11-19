package perfuncted

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A managed log root may become unreadable while retaining private session
// directories. Retention must not infer that those directories are absent,
// follow a fallback path, or modify an unrelated root.
func TestSessionLogRetentionDoesNotTouchUnreadableRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory read permission")
	}
	root := filepath.Join(t.TempDir(), "private-retention")
	child := filepath.Join(root, sessionLogPrefix+"retained")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(child, "private.log")
	if err := os.WriteFile(payload, []byte("preserve private audit"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(child, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(root, 0700); err != nil {
			t.Errorf("restore log root permissions: %v", err)
		}
	})
	CleanupSessionLogs(root, time.Hour)
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(payload)
	if err != nil || string(got) != "preserve private audit" {
		t.Fatalf("unreadable retention root lost audit data: got=%q err=%v", got, err)
	}
}
