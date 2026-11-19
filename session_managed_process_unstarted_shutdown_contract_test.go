//go:build linux

package perfuncted

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// A failed pre-start child must not be registered for reaping or cause a
// shutdown to send signals to an unrelated PID or process group.
func TestManagedSessionProcessDoesNotOwnUnstartedCommand(t *testing.T) {
	if got := newManagedSessionProcess(nil); got != nil {
		t.Fatalf("nil command created a managed process: %+v", got)
	}
	if got := newManagedSessionProcess(exec.Command("true")); got != nil {
		t.Fatalf("unstarted command created a managed process: %+v", got)
	}
	var missing *managedSessionProcess
	missing.stop(time.Millisecond)
	if err := missing.wait(context.Background()); err != nil {
		t.Fatalf("nil managed process wait must be complete: %v", err)
	}
}

// A session process with no child must not signal anything, and an
// uninitialized application process handle cannot claim an external group.
func TestUnownedManagedProcessStopHasNoSideEffects(t *testing.T) {
	process := &managedSessionProcess{}
	process.stop(time.Millisecond)
	var proc *managedProc
	proc.stop(time.Millisecond)
	proc = &managedProc{}
	proc.stop(time.Millisecond)
	if err := proc.waitGroup(context.Background()); err != nil {
		t.Fatalf("unowned process has no group to wait for: %v", err)
	}
}
