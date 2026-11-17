package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessRuntimeOwnershipRequiresExactEnvironmentValue(t *testing.T) {
	procRoot := t.TempDir()
	const pid = 4242
	procDir := filepath.Join(procRoot, "4242")
	if err := os.Mkdir(procDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "environ"), []byte("XDG_RUNTIME_DIR=/run/user/1000/pf-xdg-evil\x00PATH=/usr/bin\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	if processUsesRuntimeDirAt(procRoot, 0, "/run/user/1000/pf-xdg") {
		t.Fatal("process without a PID was treated as a runtime owner")
	}
	if processUsesRuntimeDirAt(procRoot, pid, "") {
		t.Fatal("process without a runtime path was treated as a runtime owner")
	}
	if processUsesRuntimeDirAt(procRoot, pid, "/run/user/1000/pf-xdg") {
		t.Fatal("prefix-sharing runtime path was treated as an exact owner match")
	}
	if err := os.WriteFile(filepath.Join(procDir, "environ"), []byte("XDG_RUNTIME_DIR=/run/user/1000/pf-xdg\x00PATH=/usr/bin\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	if !processUsesRuntimeDirAt(procRoot, pid, "/run/user/1000/pf-xdg") {
		t.Fatal("process with exact runtime ownership was not recognized")
	}
}

type cancelAtErrCallContext struct {
	context.Context
	cancel context.CancelCauseFunc
	target int
	calls  int
}

func (c *cancelAtErrCallContext) Err() error {
	c.calls++
	if c.calls == c.target {
		c.cancel(errors.New("caller withdrew cleanup authority"))
	}
	return c.Context.Err()
}

func TestRateLimitedSessionCleanupPreservesConcurrentCancellation(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	previousTime, previousAttempt := lastCleanupTime, cleanupAttempt
	lastCleanupTime = time.Now()
	cleanupStaleSessionsMu.Unlock()
	t.Cleanup(func() {
		cleanupStaleSessionsMu.Lock()
		lastCleanupTime, cleanupAttempt = previousTime, previousAttempt
		cleanupStaleSessionsMu.Unlock()
	})

	base, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 2}
	err := CleanupStaleSessionsContext(ctx, time.Minute)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(base)) {
		t.Fatalf("rate-limited cleanup error = %v, want caller cancellation and cause", err)
	}
}
