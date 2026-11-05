package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

// A helper that deliberately ignores SIGTERM must still be killed and reaped
// by the direct managed-session shutdown path.
func TestManagedSessionStopEscalatesAfterIgnoredSIGTERM(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "GO_HELPER_READY="+ready, "GO_WANT_IGNORE_SIGTERM=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := newManagedSessionProcess(cmd)
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	if err := waitForFile(context.Background(), ready, 100, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// The helper has installed the ignore handler before reporting ready.
	if _, err := os.Stat(ready); err != nil {
		t.Fatal(err)
	}
	process.stop(30 * time.Millisecond)
	if !process.waitTimeout(0) {
		t.Fatal("stop returned without reaping SIGTERM-resistant helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := process.wait(ctx); err == nil {
		t.Fatal("forced termination reported success")
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("helper still exists after stop escalation: %v", err)
	}
	process.stop(0)
}
