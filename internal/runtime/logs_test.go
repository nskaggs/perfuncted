package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Simultaneous sessions may share an approved log root, but they must not
// share a writable directory or log file. Fresh logs remain owner-private.
func TestConcurrentSessionLogsArePrivateAndDoNotCollide(t *testing.T) {
	root := filepath.Join(t.TempDir(), "logs")
	const sessions = 24
	type result struct {
		dir string
		err error
	}
	results := make(chan result, sessions)
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dir, err := CreateSessionLogDir(root)
			results <- result{dir, err}
		}()
	}
	wg.Wait()
	close(results)
	seen := make(map[string]bool)
	for r := range results {
		if r.err != nil {
			t.Fatalf("concurrent log directory creation failed: %v", r.err)
		}
		if seen[r.dir] {
			t.Fatalf("two sessions received the same private log directory: %s", r.dir)
		}
		seen[r.dir] = true
		info, err := os.Stat(r.dir)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
			t.Fatalf("session directory not private: %s info=%v err=%v", r.dir, info, err)
		}
		path := filepath.Join(r.dir, "session.log")
		file, err := OpenPrivateSessionLog(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(r.dir); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("session log not private: %s info=%v err=%v", path, info, err)
		}
		// A second opener must not be allowed to append or overwrite earlier logs.
		if again, err := OpenPrivateSessionLog(path); !errors.Is(err, os.ErrExist) {
			if again != nil {
				_ = again.Close()
			}
			t.Fatalf("second log opener did not fail exclusively: %s err=%v", path, err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != r.dir {
			t.Fatalf("session log contents changed: %s content=%q err=%v", path, data, err)
		}
	}
	if len(seen) != sessions {
		t.Fatalf("created %d session logs, want %d", len(seen), sessions)
	}
}

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
	directory, err := CreateSessionLogDir("")
	if directory != "" || err == nil {
		t.Fatalf("invalid default log root accepted: directory=%q err=%v", directory, err)
	}
	if !errors.Is(err, os.ErrInvalid) && !errors.Is(err, os.ErrNotExist) {
		// ENOTDIR is the normal error for a path beneath a regular file.
		var pathErr *os.PathError
		if !errors.As(err, &pathErr) {
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
	CloseLogFile(file)
	CloseLogFile(nil)
	data, err := os.ReadFile(file.Name())
	if err != nil || string(data) != "retained private log" {
		t.Fatalf("idempotent close damaged log: %q err=%v", data, err)
	}
}

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
	dir, err := CreateSessionLogDir(root)
	if dir != "" || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unwritable log directory was accepted: dir=%q err=%v", dir, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed log creation left artifacts: entries=%v err=%v", entries, err)
	}
}

// Session startup triggers log retention. A configured root with a symlinked
// ancestor must not redirect startup cleanup into an unrelated directory.
func TestCreateSessionLogDirRejectsSymlinkedAncestorBeforeCleanup(t *testing.T) {
	base := t.TempDir()
	external := filepath.Join(base, "external")
	logRoot := filepath.Join(external, "logs")
	victim := filepath.Join(logRoot, SessionLogPrefix+"expired")
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
	dir, err := CreateSessionLogDir(filepath.Join(alias, "logs"))
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
