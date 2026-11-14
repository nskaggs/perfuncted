package perfuncted

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The implicit log location is beneath TMPDIR. When that parent is not a
// directory, startup must fail rather than silently write private logs into
// a different location or damage the caller's existing file.
func TestSessionLogDefaultRootFailsClosedWhenTempParentIsFile(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("private existing content"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", blocker)
	directory, err := createSessionLogDir("")
	if directory != "" || err == nil {
		t.Fatalf("invalid default log root accepted: directory=%q err=%v", directory, err)
	}
	if !errors.Is(err, os.ErrInvalid) && !errors.Is(err, os.ErrNotExist) {
		// ENOTDIR is the normal error for a path beneath a regular file.
		if _, ok := err.(*os.PathError); !ok {
			t.Fatalf("unexpected log root failure: %v", err)
		}
	}
	data, err := os.ReadFile(blocker)
	if err != nil || string(data) != "private existing content" {
		t.Fatalf("default log root overwrote existing data: %q err=%v", data, err)
	}
}

// Teardown can race with another owner which already closed an acquired log.
// The close helper is best-effort and must never reopen or modify the file.
func TestSessionLogCloseHandlesAlreadyClosedFileWithoutChangingContents(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "private-log-")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("retained private log"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	logFileClose(file)
	logFileClose(nil)
	data, err := os.ReadFile(file.Name())
	if err != nil || string(data) != "retained private log" {
		t.Fatalf("idempotent close damaged log: %q err=%v", data, err)
	}
}
