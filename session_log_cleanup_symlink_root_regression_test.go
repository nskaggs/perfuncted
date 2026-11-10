package perfuncted

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// CleanupSessionLogs is an exported maintenance entry point. Unlike normal
// session startup, callers can supply a symlink root: cleanup must not follow
// it into a separate tree and delete unrelated directories there.
func TestCleanupSessionLogsNeverFollowsSymlinkRoot(t *testing.T) {
	base := t.TempDir()
	external := filepath.Join(base, "external")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(external, sessionLogPrefix+"expired")
	if err := os.Mkdir(victim, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(victim, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("do-not-delete"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(victim, old, old); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "log-alias")
	if err := os.Symlink(external, alias); err != nil {
		t.Fatal(err)
	}
	CleanupSessionLogs(alias, 24*time.Hour)
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("followed symlink root and deleted external log: %v", err)
	}
}
