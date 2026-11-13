package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An unreadable runtime root is a scan failure, not a successful cleanup.
// The claimed throttle window must be released so a repaired environment can
// be scanned again immediately rather than suppressing stale-session recovery.
func TestStaleSessionScanFailureAllowsImmediateCorrectedRootRetry(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	priorTime, priorAttempt := lastCleanupTime, cleanupAttempt
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	t.Cleanup(func() {
		cleanupStaleSessionsMu.Lock()
		lastCleanupTime, cleanupAttempt = priorTime, priorAttempt
		cleanupStaleSessionsMu.Unlock()
	})

	parent := t.TempDir()
	badRoot := filepath.Join(parent, "not-a-directory")
	if err := os.WriteFile(badRoot, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", badRoot)
	if err := CleanupStaleSessionsContext(context.Background(), 0); err == nil {
		t.Fatal("invalid runtime root was silently accepted")
	}
	cleanupStaleSessionsMu.Lock()
	released := lastCleanupTime.IsZero()
	cleanupStaleSessionsMu.Unlock()
	if !released {
		t.Fatal("failed scan retained cleanup throttle claim")
	}

	goodRoot := filepath.Join(parent, "valid-root")
	if err := os.Mkdir(goodRoot, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", goodRoot)
	dir := filepath.Join(goodRoot, "perfuncted-xdg-stale")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStaleSessionsContext(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrected root was not scanned after failure: %v", err)
	}
	data, err := os.ReadFile(badRoot)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("unrelated runtime file changed: %q %v", data, err)
	}
}
