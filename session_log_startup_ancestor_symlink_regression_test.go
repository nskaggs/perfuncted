package perfuncted

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Session startup triggers log retention. A configured root with a symlinked
// ancestor must not redirect startup cleanup into an unrelated directory.
func TestCreateSessionLogDirRejectsSymlinkedAncestorBeforeCleanup(t *testing.T) {
	base := t.TempDir()
	external := filepath.Join(base, "external")
	logRoot := filepath.Join(external, "logs")
	victim := filepath.Join(logRoot, sessionLogPrefix+"expired")
	if err := os.MkdirAll(victim, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(victim, "important")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-15 * 24 * time.Hour)
	if err := os.Chtimes(victim, old, old); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(external, alias); err != nil {
		t.Fatal(err)
	}
	dir, err := createSessionLogDir(filepath.Join(alias, "logs"))
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	if err == nil {
		t.Error("startup accepted log root through a symlinked ancestor")
	}
	if b, readErr := os.ReadFile(sentinel); readErr != nil || string(b) != "preserve" {
		t.Fatalf("startup retention deleted external log: content=%q err=%v", b, readErr)
	}
}
