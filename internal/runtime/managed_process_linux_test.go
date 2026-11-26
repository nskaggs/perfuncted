//go:build linux

package runtime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// A failed pre-start child must not be registered for reaping or cause a
// shutdown to send signals to an unrelated PID or process group.
func TestManagedSessionProcessDoesNotOwnUnstartedCommand(t *testing.T) {
	if got := NewSessionProcess(nil); got != nil {
		t.Fatalf("nil command created a managed process: %+v", got)
	}
	if got := NewSessionProcess(exec.Command("true")); got != nil {
		t.Fatalf("unstarted command created a managed process: %+v", got)
	}
	var missing *SessionProcess
	missing.Stop(time.Millisecond)
	if err := missing.Wait(context.Background(), errInvalidArgument); err != nil {
		t.Fatalf("nil managed process wait must be complete: %v", err)
	}
}

// A session process with no child must not signal anything, and an
// uninitialized application process handle cannot claim an external group.
func TestUnownedManagedProcessStopHasNoSideEffects(t *testing.T) {
	process := &SessionProcess{}
	process.Stop(time.Millisecond)
	var proc *Group
	proc.Stop(time.Millisecond)
	proc = &Group{}
	proc.Stop(time.Millisecond)
	if err := proc.WaitGroup(context.Background(), errInvalidArgument); err != nil {
		t.Fatalf("unowned process has no group to wait for: %v", err)
	}
}

// A retained process handle may outlive its OS process. Without an exec.Cmd,
// shutdown must detect the missing PID, retire group ownership, and never
// signal or wait for a new unrelated process.
func TestManagedProcessStopRetiresMissingUnreapedPID(t *testing.T) {
	const missingPID = 1 << 30 // Larger than the Linux pid_max ceiling.
	if err := syscall.Kill(missingPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Skipf("cannot establish missing pid: %v", err)
	}
	proc := &Group{pid: missingPID}
	proc.Stop(time.Millisecond)
	if !proc.groupGone {
		t.Fatal("stale process group ownership survived stop")
	}
	// Subsequent stops must stay idempotent after retiring a missing group.
	proc.Stop(0)
	if !proc.groupGone {
		t.Fatal("subsequent stop restored ownership of missing group")
	}
}

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
	if WaitForProc(pid, 0) {
		t.Fatal("raw wait4 marked running process reaped")
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("timed-out child already dead: %v", err)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if !WaitForProc(pid, time.Second) {
		t.Fatal("killed child was not reaped")
	}
	var status syscall.WaitStatus
	_, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	if err != syscall.ECHILD {
		t.Fatalf("child not reaped exactly once: %v", err)
	}
	// The existing child has already been reaped. ECHILD is a completed wait,
	// not a reason for the caller to block or signal an unrelated process.
	if !WaitForProc(pid, 0) {
		t.Fatal("already reaped direct child was reported active")
	}
}

func TestWaitForProcReturnsTrueForNaturallyExitedDirectChild(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 7")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Release() }()
	if !WaitForProc(cmd.Process.Pid, time.Second) {
		t.Fatal("naturally exited child was not collected")
	}
	if _, err := os.FindProcess(cmd.Process.Pid); err != nil {
		t.Fatalf("process handle unexpectedly unavailable: %v", err)
	}
	if !WaitForProc(cmd.Process.Pid, 0) {
		t.Fatal("subsequent wait did not observe ECHILD")
	}
}

// A process group that has already disappeared must not delay shutdown, and
// callers must supply a context before waiting on an owned process.
func TestManagedProcessWaitGroupMissingOwnerAndInvalidContext(t *testing.T) {
	const missingPID = 1 << 30 // Outside Linux's pid_max range.
	if err := syscall.Kill(-missingPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Skipf("unable to establish absent process group: %v", err)
	}

	p := &Group{pid: missingPID}
	if err := p.WaitGroup(nil, errInvalidArgument); !errors.Is(err, errInvalidArgument) {
		t.Fatalf("nil-context wait = %v, want invalid argument", err)
	}
	if err := p.WaitGroup(context.Background(), errInvalidArgument); err != nil {
		t.Fatalf("already exited group should not block cleanup: %v", err)
	}
	if !p.groupGone {
		t.Fatal("missing group was not retired after wait")
	}
	if !p.waitGroupTimeout(0) {
		t.Fatal("retired group unexpectedly timed out")
	}
}

// Positive timeout waits for process-exit notification, while zero timeout
// performs a non-blocking inspection and does not consume its channel.
func TestManagedSessionWaitTimeoutZeroAndLateExit(t *testing.T) {
	var missing *SessionProcess
	if !missing.WaitTimeout(time.Second) {
		t.Fatal("missing managed process must be considered complete")
	}
	done := make(chan struct{})
	proc := &SessionProcess{waitDone: done}
	if proc.WaitTimeout(0) {
		t.Fatal("incomplete process treated as exited")
	}
	close(done)
	if !proc.WaitTimeout(0) || !proc.WaitTimeout(time.Second) {
		t.Fatal("completed process did not retain exit readiness")
	}
}
