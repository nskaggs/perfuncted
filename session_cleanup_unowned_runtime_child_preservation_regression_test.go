package perfuncted

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	sessionruntime "github.com/nskaggs/perfuncted/internal/runtime"
)

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
