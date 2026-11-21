package perfuncted

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
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
			dir, err := createSessionLogDir(root)
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
		file, err := openPrivateSessionLog(path)
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
		if again, err := openPrivateSessionLog(path); !errors.Is(err, os.ErrExist) {
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
