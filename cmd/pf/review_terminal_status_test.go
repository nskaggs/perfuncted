package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReviewWorkflowStatusHonorsCanceledContext(t *testing.T) {
	path := shortPrivateSocketDir(t)
	root, err := namedOpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	w := newWorkflowLifecycle(context.Background())
	w.id = "terminal-cancel"
	control, err := startWorkflowControlIn(root, w)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.finish(nil); err != nil {
		t.Fatal(err)
	}
	if err := control.close(); err != nil {
		t.Fatal(err)
	}
	root, err = namedOpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reply, err := workflowCall(ctx, root, w.id, "status")
	if !errors.Is(err, context.Canceled) || reply.ID != "" {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
}

// A terminal file is valid even during the brief shutdown interval after
// the listener closes but before the stale socket inode is removed.
func TestReviewWorkflowTerminalStatusDuringSocketCleanup(t *testing.T) {
	path := shortPrivateSocketDir(t)
	root, err := namedOpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	w := newWorkflowLifecycle(context.Background())
	w.id = "terminal-cleanup"
	owner, err := namedReserveIn(root, w.id)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Unlinkat(int(owner.dir.Fd()), "status", 0); owner.close() }()
	if err := owner.publish(); err != nil {
		t.Fatal(err)
	}
	if err := w.finish(nil); err != nil {
		t.Fatal(err)
	}
	if err := workflowWriteTerminalStatus(owner.dir, w.status()); err != nil {
		t.Fatal(err)
	}
	if err := owner.listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(namedFDPath(owner.dir, "control")); err != nil {
		t.Fatal(err)
	}
	root, err = namedOpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	reply, err := workflowCall(context.Background(), root, w.id, "status")
	if err != nil || !reply.Accepted || reply.State != workflowCompleted {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
}
