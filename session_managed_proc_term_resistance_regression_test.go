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

// The raw managedProc path owns process-group shutdown without an asynchronous
// reaper. A leader that ignores SIGTERM must be force-killed and reaped, not
// reported stopped merely because the TERM grace elapsed.
func TestManagedProcDirectStopEscalatesAndReapsTermResistantLeader(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "GO_HELPER_READY="+ready, "GO_WANT_IGNORE_SIGTERM=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL); _ = cmd.Wait() })
	if err := waitForFile(context.Background(), ready, 100, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	proc := &managedProc{cmd: cmd, pid: pid}
	if !proc.groupAlive() {
		t.Fatal("running helper group was not detected")
	}
	proc.stop(60 * time.Millisecond)
	if proc.groupAlive() || processGroupAlive(pid) {
		t.Fatal("SIGTERM-resistant helper survived direct stop escalation")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("direct stop failed to reap helper pid=%d: %v", pid, err)
	}
	var status syscall.WaitStatus
	if _, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil); !errors.Is(err, syscall.ECHILD) {
		t.Fatalf("direct stop left waitable process: %v", err)
	}
	// Repeated stop must not signal an unrelated process that inherited the PID.
	proc.stop(0)
	if err := proc.signal(syscall.SIGKILL); err != nil {
		t.Fatalf("signal after reap: %v", err)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatalf("helper never initialized handler: %v", err)
	}
}
