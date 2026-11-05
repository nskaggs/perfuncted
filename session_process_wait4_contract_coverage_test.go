//go:build linux

package perfuncted

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// waitForProc owns a raw wait4(2) reap only for direct managed children.
// It must not report that a still-running child was reaped on timeout.
func TestWaitForProcRetainsRunningChildAfterTimeoutThenReaps(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 5")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL); _ = cmd.Process.Release() })
	if waitForProc(pid, 0) {
		t.Fatal("raw wait4 marked running process reaped")
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("timed-out child already dead: %v", err)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if !waitForProc(pid, time.Second) {
		t.Fatal("killed child was not reaped")
	}
	var status syscall.WaitStatus
	_, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	if err != syscall.ECHILD {
		t.Fatalf("child not reaped exactly once: %v", err)
	}
	// The existing child has already been reaped. ECHILD is a completed wait,
	// not a reason for the caller to block or signal an unrelated process.
	if !waitForProc(pid, 0) {
		t.Fatal("already reaped direct child was reported active")
	}
}

func TestWaitForProcReturnsTrueForNaturallyExitedDirectChild(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 7")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Release() }()
	if !waitForProc(cmd.Process.Pid, time.Second) {
		t.Fatal("naturally exited child was not collected")
	}
	if _, err := os.FindProcess(cmd.Process.Pid); err != nil {
		t.Fatalf("process handle unexpectedly unavailable: %v", err)
	}
	if !waitForProc(cmd.Process.Pid, 0) {
		t.Fatal("subsequent wait did not observe ECHILD")
	}
}
