//go:build linux

package runtime

import (
	"context"
	"errors"
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
