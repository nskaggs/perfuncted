package runtime

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// If the process has exited and the caller has already canceled, the wait
// must never randomly report a successful observation from the ready child.
func TestManagedSessionProcessWaitCompletedChildRespectsCancellation(t *testing.T) {
	done := make(chan struct{})
	close(done)
	proc := &SessionProcess{waitDone: done}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for n := 0; n < 300; n++ {
		if err := proc.Wait(ctx, errInvalidArgument); !errors.Is(err, context.Canceled) {
			t.Fatalf("wait %d acknowledged completed child despite canceled caller: %v", n, err)
		}
	}
}

func TestManagedSessionProcessWaitPreservesCancellationCause(t *testing.T) {
	done := make(chan struct{})
	close(done)
	proc := &SessionProcess{waitDone: done}
	cause := errors.New("synthetic owner revoked child observation")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := proc.Wait(ctx, errInvalidArgument); !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("process wait lost cancellation cause: %v", err)
	}
}

// Waiting for a live process group must respect cancellation, preserve the
// caller's cancellation cause, and never terminate a group merely because
// its observer stopped waiting. Only explicit shutdown may signal children.
func TestManagedProcessGroupWaitCancellationPreservesRunningProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	proc := &Group{cmd: cmd, pid: cmd.Process.Pid}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
	if !proc.GroupAlive() {
		t.Fatal("newly started process group not alive")
	}
	if err := proc.WaitGroup(nil, errInvalidArgument); !errors.Is(err, errInvalidArgument) {
		t.Fatalf("nil wait context: %v", err)
	}
	cause := errors.New("shutdown observer canceled")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := proc.WaitGroup(ctx, errInvalidArgument); !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("pre-canceled wait lost cause: %v", err)
	}
	ctx, deadlineCancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer deadlineCancel()
	if err := proc.WaitGroup(ctx, errInvalidArgument); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("live group waiter missed deadline: %v", err)
	}
	if !proc.GroupAlive() {
		t.Fatal("canceled observation terminated process group")
	}
	if proc.waitGroupTimeout(0) {
		t.Fatal("live group reported complete with zero timeout")
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("explicit shutdown failed: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed helper reported successful exit")
	}
	if err := proc.WaitGroup(context.Background(), errInvalidArgument); err != nil {
		t.Fatalf("reaped group not marked gone: %v", err)
	}
	if !proc.waitGroupTimeout(0) {
		t.Fatal("reaped group reported live")
	}
}
