package runtime

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/contextutil"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

var errInvalidArgument = errors.New("invalid argument")

func TestNilAndZeroManagedProcessLifecyclesAreSafe(t *testing.T) {
	var managed *SessionProcess
	if err := managed.Wait(context.Background(), errInvalidArgument); err != nil {
		t.Fatalf("wait on nil managed process = %v, want nil", err)
	}
	if !managed.WaitTimeout(0) {
		t.Fatal("nil managed process was not treated as complete")
	}
	managed.Stop(0)
	if NewSessionProcess(nil) != nil || NewSessionProcess(&exec.Cmd{}) != nil {
		t.Fatal("unstarted command produced a managed process")
	}

	var proc *Group
	if proc.GroupAlive() || ProcessGroupAlive(0) {
		t.Fatal("invalid process group was reported alive")
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal nil process = %v, want nil", err)
	}
	if err := proc.WaitGroup(nil, errInvalidArgument); err != nil {
		t.Fatalf("wait nil process group = %v, want nil", err)
	}
	proc.Stop(0)
	zero := &Group{}
	if err := zero.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal zero process group = %v, want nil", err)
	}
	if err := zero.WaitGroup(context.Background(), errInvalidArgument); err != nil {
		t.Fatalf("wait zero process group = %v, want nil", err)
	}
	if err := zero.WaitGroup(nil, errInvalidArgument); err != nil {
		t.Fatalf("wait zero process group with nil context = %v, want nil", err)
	}
	if !zero.waitGroupTimeout(0) {
		t.Fatal("zero process group did not complete immediately")
	}

	if contextutil.ErrorWithCause(nil) != nil || contextutil.ErrorWithCause(context.Background()) != nil {
		t.Fatal("inactive cancellation context produced an error")
	}
	cause := errors.New("session shutdown requested")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := contextutil.ErrorWithCause(ctx); !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("cancellation error = %v, want context and cause", err)
	}
}

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

// Observe the precise point at which wait starts listening for cancellation.
// The child never exits, so the only permitted completion is caller revocation.
type enteringWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *enteringWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestManagedSessionProcessWaitRevokedWhileChildStillRunning(t *testing.T) {
	for attempt := 0; attempt < 30; attempt++ {
		childExited := make(chan struct{})
		process := &SessionProcess{waitDone: childExited}
		cause := errors.New("session ownership was revoked")
		parent, cancel := context.WithCancelCause(context.Background())
		observed := &enteringWaitContext{Context: parent, entered: make(chan struct{})}
		result := make(chan error, 1)
		go func() { result <- process.Wait(observed, errInvalidArgument) }()
		select {
		case <-observed.entered:
		case <-time.After(time.Second):
			cancel(cause)
			t.Fatal("process wait did not enter its cancellation selector")
		}
		cancel(cause)
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
				t.Fatalf("wait attempt %d lost cancellation or cause: %v", attempt, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("wait attempt %d remained blocked after cancellation", attempt)
		}
		select {
		case <-childExited:
			t.Fatal("canceling wait modified the child completion signal")
		default:
		}
	}
}
