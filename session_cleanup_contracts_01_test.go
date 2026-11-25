package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/env"
	sessionruntime "github.com/nskaggs/perfuncted/internal/runtime"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestStaleSessionCleanupNilContextAndThrottledCancellation(t *testing.T) {
	err := CleanupStaleSessionsContext(nil, time.Minute) //nolint:staticcheck // Explicit nil-context validation is under test.
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil cleanup context returned %v", err)
	}
	cleanupStaleSessionsMu.Lock()
	savedTime, savedAttempt := lastCleanupTime, cleanupAttempt
	lastCleanupTime = time.Now()
	cleanupStaleSessionsMu.Unlock()
	t.Cleanup(func() {
		cleanupStaleSessionsMu.Lock()
		lastCleanupTime, cleanupAttempt = savedTime, savedAttempt
		cleanupStaleSessionsMu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CleanupStaleSessionsContext(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("throttled canceled cleanup returned %v", err)
	}
}

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

func TestStaleCleanupPreservesRuntimeUsedByUnownedProcessGroup(t *testing.T) {
	runtimeRoot := t.TempDir()
	t.Setenv("TMPDIR", runtimeRoot)
	dir, err := os.MkdirTemp(runtimeRoot, "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("create managed runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionOwnerPIDFile), []byte("2147483647"), 0o600); err != nil {
		t.Fatalf("write stale owner PID: %v", err)
	}

	readyPath := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = env.Merge(
		os.Environ(),
		"GO_WANT_HELPER_PROCESS=1",
		"GO_HELPER_READY="+readyPath,
		"XDG_RUNTIME_DIR="+dir,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start runtime user: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if err := sessionruntime.WaitForFile(context.Background(), readyPath, 100, 5*time.Millisecond); err != nil {
		t.Fatalf("wait for runtime user readiness: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sway.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		t.Fatalf("write child PID: %v", err)
	}

	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	if err := CleanupStaleSessionsContext(context.Background(), 24*time.Hour); err != nil {
		t.Fatalf("cleanup stale sessions: %v", err)
	}

	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatalf("cleanup terminated a process it does not own: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("cleanup removed runtime still referenced by a live child: %v", err)
	}
}
