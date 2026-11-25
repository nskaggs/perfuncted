package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

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

// Simulate the owner completing between an authenticated status request and
// its reply. A lost status response may use persisted terminal evidence;
// a mutating request must never be retried or reported as accepted.
func TestReviewWorkflowStatusSurvivesLostTerminalReply(t *testing.T) {
	for _, operation := range []string{"status", "pause"} {
		t.Run(operation, func(t *testing.T) {
			path := shortPrivateSocketDir(t)
			root, err := namedOpenDir(unix.AT_FDCWD, path)
			if err != nil {
				t.Fatal(err)
			}
			w := newWorkflowLifecycle(context.Background())
			w.id = "terminal-reply"
			owner, err := namedReserveIn(root, w.id)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = unix.Unlinkat(int(owner.dir.Fd()), "status", 0); owner.close() }()
			if err := owner.publish(); err != nil {
				t.Fatal(err)
			}
			serverDone := make(chan error, 1)
			go func() {
				conn, err := owner.listener.AcceptUnix()
				if err != nil {
					serverDone <- err
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(time.Second))
				req, err := workflowReadRequest(conn)
				if err == nil && (req.ID != w.id || req.Operation != operation) {
					err = errors.New("wrong request")
				}
				if err == nil {
					err = w.finish(nil)
				}
				if err == nil {
					err = workflowWriteTerminalStatus(owner.dir, w.status())
				}
				serverDone <- err
			}()
			root, err = namedOpenDir(unix.AT_FDCWD, path)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			reply, err := workflowCall(ctx, root, w.id, operation)
			select {
			case serverErr := <-serverDone:
				if serverErr != nil {
					t.Fatal(serverErr)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if operation == "status" {
				if err != nil || !reply.Accepted || reply.State != workflowCompleted {
					t.Fatalf("reply=%+v err=%v", reply, err)
				}
			} else if err == nil || reply.Accepted {
				t.Fatalf("lost mutation falsely confirmed: reply=%+v err=%v", reply, err)
			}
		})
	}
}
