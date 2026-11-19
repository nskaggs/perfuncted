//go:build linux

package perfuncted

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

// A retained process handle may outlive its OS process. Without an exec.Cmd,
// shutdown must detect the missing PID, retire group ownership, and never
// signal or wait for a new unrelated process.
func TestManagedProcessStopRetiresMissingUnreapedPID(t *testing.T) {
	const missingPID = 1 << 30 // Larger than the Linux pid_max ceiling.
	if err := syscall.Kill(missingPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Skipf("cannot establish missing pid: %v", err)
	}
	proc := &managedProc{pid: missingPID}
	proc.stop(time.Millisecond)
	if !proc.groupGone {
		t.Fatal("stale process group ownership survived stop")
	}
	// Subsequent stops must stay idempotent after retiring a missing group.
	proc.stop(0)
	if !proc.groupGone {
		t.Fatal("subsequent stop restored ownership of missing group")
	}
}
