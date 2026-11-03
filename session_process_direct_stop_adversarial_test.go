package perfuncted

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

// The direct managed-session stop path must terminate and reap a real helper;
// an interrupted waiter cannot assume ownership of the reap or release the
// process group while it is still running.
func TestManagedSessionDirectStopTerminatesAndReapsActiveHelper(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "GO_HELPER_READY="+ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := newManagedSessionProcess(cmd)
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	if err := waitForFile(context.Background(), ready, 100, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if process.waitTimeout(0) {
		t.Fatal("running helper prematurely marked reaped")
	}
	if process.waitTimeout(10 * time.Millisecond) {
		t.Fatal("running helper completed during timeout")
	}
	process.stop(250 * time.Millisecond)
	if !process.waitTimeout(0) {
		t.Fatal("direct stop returned without reaping helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := process.wait(ctx); err == nil {
		t.Fatal("terminated helper reported successful exit")
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("terminated helper still exists: %v", err)
	}
	// Repeated stops must not send signals to a recycled process identity.
	process.stop(0)
}

func TestManagedSessionWaitPropagatesActualNonzeroExitAndReapsOnce(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 23")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := newManagedSessionProcess(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := process.wait(ctx)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("actual child exit status lost: %v", err)
	}
	if !process.waitTimeout(0) {
		t.Fatal("exit published before reap completed")
	}
	if err2 := process.wait(ctx); !errors.As(err2, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("second waiter did not see same result: %v", err2)
	}
	var status syscall.WaitStatus
	if _, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WNOHANG, nil); !errors.Is(err, syscall.ECHILD) {
		t.Fatalf("child not reaped exactly once: %v", err)
	}
}
