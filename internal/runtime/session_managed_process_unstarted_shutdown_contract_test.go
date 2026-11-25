//go:build linux

package runtime

import (
	"context"
	"os/exec"
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
