package perfuncted

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A final-component Lstat does not protect a cleanup root reached through a
// symlink in an ancestor. The target subtree is unrelated to the chosen root.
func TestCleanupSessionLogsRejectsSymlinkAncestor(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	logs := filepath.Join(outside, "logs")
	victim := filepath.Join(logs, sessionLogPrefix+"foreign")
	if err := os.MkdirAll(victim, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(victim, "important.txt")
	if err := os.WriteFile(sentinel, []byte("leave alone"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(victim, old, old); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	CleanupSessionLogs(filepath.Join(alias, "logs"), 24*time.Hour)
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "leave alone" {
		t.Fatalf("cleanup followed ancestor symlink: data=%q err=%v", got, err)
	}
}
