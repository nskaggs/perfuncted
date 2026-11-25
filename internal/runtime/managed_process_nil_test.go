package runtime

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"os/exec"
	"syscall"
	"testing"
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
