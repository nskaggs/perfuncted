package sessionregistry

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/testutil"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/nskaggs/perfuncted/internal/workflow"
)

func TestReviewWorkflowStatusHonorsCanceledContext(t *testing.T) {
	path := testutil.ShortSocketDir(t)
	root, err := OpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	w := workflow.New(context.Background(), "terminal-cancel")
	control, err := startWorkflowControlIn(root, w)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	root, err = OpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reply, err := CallWorkflow(ctx, root, w.ID(), "status")
	if !errors.Is(err, context.Canceled) || reply.ID != "" {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
}

// A terminal file is valid even during the brief shutdown interval after
// the listener closes but before the stale socket inode is removed.
func TestReviewWorkflowTerminalStatusDuringSocketCleanup(t *testing.T) {
	path := testutil.ShortSocketDir(t)
	root, err := OpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	w := workflow.New(context.Background(), "terminal-cleanup")
	owner, err := ReserveIn(root, w.ID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Unlinkat(int(owner.dir.Fd()), "status", 0); owner.Close() }()
	if err := owner.Publish(); err != nil {
		t.Fatal(err)
	}
	if err := w.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if err := workflowWriteTerminalStatus(owner.dir, w.Status()); err != nil {
		t.Fatal(err)
	}
	if err := owner.listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(namedFDPath(owner.dir, "control")); err != nil {
		t.Fatal(err)
	}
	root, err = OpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	reply, err := CallWorkflow(context.Background(), root, w.ID(), "status")
	if err != nil || !reply.Accepted || reply.State != workflow.Completed {
		t.Fatalf("reply=%+v err=%v", reply, err)
	}
}

// Simulate the owner completing between an authenticated status request and
// its reply. A lost status response may use persisted terminal evidence;
// a mutating request must never be retried or reported as accepted.
func TestReviewWorkflowStatusSurvivesLostTerminalReply(t *testing.T) {
	for _, operation := range []string{"status", "pause"} {
		t.Run(operation, func(t *testing.T) {
			path := testutil.ShortSocketDir(t)
			root, err := OpenDir(unix.AT_FDCWD, path)
			if err != nil {
				t.Fatal(err)
			}
			w := workflow.New(context.Background(), "terminal-reply")
			owner, err := ReserveIn(root, w.ID())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = unix.Unlinkat(int(owner.dir.Fd()), "status", 0); owner.Close() }()
			if err := owner.Publish(); err != nil {
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
				if err == nil && (req.ID != w.ID() || req.Operation != operation) {
					err = errors.New("wrong request")
				}
				if err == nil {
					err = w.Finish(nil)
				}
				if err == nil {
					err = workflowWriteTerminalStatus(owner.dir, w.Status())
				}
				serverDone <- err
			}()
			root, err = OpenDir(unix.AT_FDCWD, path)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			reply, err := CallWorkflow(ctx, root, w.ID(), operation)
			select {
			case serverErr := <-serverDone:
				if serverErr != nil {
					t.Fatal(serverErr)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if operation == "status" {
				if err != nil || !reply.Accepted || reply.State != workflow.Completed {
					t.Fatalf("reply=%+v err=%v", reply, err)
				}
			} else if err == nil || reply.Accepted {
				t.Fatalf("lost mutation falsely confirmed: reply=%+v err=%v", reply, err)
			}
		})
	}
}
