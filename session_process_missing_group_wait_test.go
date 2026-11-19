//go:build linux

package perfuncted

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"
)

// A process group that has already disappeared must not delay shutdown, and
// callers must supply a context before waiting on an owned process.
func TestManagedProcessWaitGroupMissingOwnerAndInvalidContext(t *testing.T) {
	const missingPID = 1 << 30 // Outside Linux's pid_max range.
	if err := syscall.Kill(-missingPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Skipf("unable to establish absent process group: %v", err)
	}

	p := &managedProc{pid: missingPID}
	if err := p.waitGroup(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil-context wait = %v, want invalid argument", err)
	}
	if err := p.waitGroup(context.Background()); err != nil {
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
	var missing *managedSessionProcess
	if !missing.waitTimeout(time.Second) {
		t.Fatal("missing managed process must be considered complete")
	}
	done := make(chan struct{})
	proc := &managedSessionProcess{waitDone: done}
	if proc.waitTimeout(0) {
		t.Fatal("incomplete process treated as exited")
	}
	close(done)
	if !proc.waitTimeout(0) || !proc.waitTimeout(time.Second) {
		t.Fatal("completed process did not retain exit readiness")
	}
}
