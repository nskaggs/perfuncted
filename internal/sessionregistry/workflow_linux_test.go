//go:build linux

package sessionregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"github.com/nskaggs/perfuncted/v2/internal/workflow"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestWorkflowWaitTerminalDoesNotPoll(t *testing.T) {
	w := workflow.New(context.Background(), "")
	done := make(chan struct{})
	go func() {
		workflowWaitTerminal(w, time.Second)
		close(done)
	}()
	if finishErr := w.Finish(nil); finishErr != nil {
		t.Fatal(finishErr)
	}
	workflowReceive(t, done)
}

func TestWorkflowControlFailureDoesNotAffectOtherRun(t *testing.T) {
	first := workflow.New(context.Background(), "")
	second := workflow.New(context.Background(), "")
	defer first.Cancel()
	defer second.Cancel()
	if _, err := workflowApply(first, workflowRequest{ID: second.ID(), Operation: "stop"}); err == nil {
		t.Fatal("foreign workflow ID accepted")
	}
	if err := first.Context().Err(); err != nil {
		t.Fatalf("invalid request canceled workflow: %v", err)
	}
	if err := second.Context().Err(); err != nil {
		t.Fatalf("invalid request canceled other workflow: %v", err)
	}
	if first.Status().State != workflow.Running || second.Status().State != workflow.Running {
		t.Fatal("invalid request changed a workflow state")
	}
}

func TestWorkflowRequestValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{name: "unknown field", data: `{"id":"x","operation":"status","extra":true}` + "\n"},
		{name: "trailing object", data: `{"id":"x","operation":"status"}{}` + "\n"},
		{name: "oversized", data: `{"id":"x","operation":"status"}` + strings.Repeat(" ", workflowControlFrameLimit) + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := workflowReadRequest(strings.NewReader(tc.data)); err == nil {
				t.Fatal("accepted invalid workflow request")
			}
		})
	}
}

func TestWorkflowControlRejectsSocketSymlink(t *testing.T) {
	path := testutil.ShortSocketDir(t)
	root, err := OpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	w := workflow.New(context.Background(), "workflow-symlink")
	c, err := startWorkflowControlIn(root, w)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := unix.Unlinkat(int(c.owner.dir.Fd()), "control", 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.Symlinkat("/etc/passwd", int(c.owner.dir.Fd()), "control"); err != nil {
		t.Fatal(err)
	}
	clientRoot, err := OpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	defer clientRoot.Close()
	if _, err := CallWorkflow(context.Background(), clientRoot, w.ID(), "status"); err == nil || !strings.Contains(err.Error(), "unsafe workflow socket") {
		t.Fatalf("socket symlink error = %v, want unsafe endpoint", err)
	}
	if _, err := os.Stat("/etc/passwd"); err != nil {
		t.Fatalf("symlink target unexpectedly unavailable: %v", err)
	}
	if finishErr := w.Finish(nil); finishErr != nil {
		t.Fatal(finishErr)
	}
}

func TestWorkflowControlRejectsReusedPIDIdentity(t *testing.T) {
	path := testutil.ShortSocketDir(t)
	root, err := OpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	w := workflow.New(context.Background(), "workflow-pid-check")
	c, err := startWorkflowControlIn(root, w)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	record, err := namedRead(c.owner.dir)
	if err != nil {
		t.Fatal(err)
	}
	record.Start = "0" // same PID, different process start identity
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(namedFDPath(c.owner.dir, "record"), data, 0600); err != nil {
		t.Fatal(err)
	}
	clientRoot, err := OpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	defer clientRoot.Close()
	if _, err := CallWorkflow(context.Background(), clientRoot, w.ID(), "status"); !errors.Is(err, errNamedStale) {
		t.Fatalf("got %v, want stale owner identity", err)
	}
	if finishErr := w.Finish(nil); finishErr != nil {
		t.Fatal(finishErr)
	}
}

func TestWorkflowControlUsesSeparateCLIProcess(t *testing.T) {
	path := testutil.ShortSocketDir(t)
	root, err := OpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	w := workflow.New(context.Background(), "workflow-subprocess")
	c, err := startWorkflowControlIn(root, w)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkflowControlSeparateProcessHelper$")
	cmd.Env = append(os.Environ(),
		"PF_WORKFLOW_CONTROL_HELPER=1",
		"PF_WORKFLOW_CONTROL_ROOT="+path,
		"PF_WORKFLOW_CONTROL_ID="+w.ID(),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("separate workflow control client: %v: %s", err, output)
	}
	var reply WorkflowReply
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&reply); err != nil {
		t.Fatalf("decode separate process response %q: %v", output, err)
	}
	if reply.ID != w.ID() || reply.State != workflow.Running || !reply.Accepted {
		t.Fatalf("unexpected separate process response: %+v", reply)
	}
	if finishErr := w.Finish(nil); finishErr != nil {
		t.Fatal(finishErr)
	}
}

func TestWorkflowControlSeparateProcessHelper(t *testing.T) {
	if os.Getenv("PF_WORKFLOW_CONTROL_HELPER") != "1" {
		return
	}
	root, err := OpenDir(unix.AT_FDCWD, os.Getenv("PF_WORKFLOW_CONTROL_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	reply, err := CallWorkflow(context.Background(), root, os.Getenv("PF_WORKFLOW_CONTROL_ID"), "status")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(reply); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowControlProtocol(t *testing.T) {
	path := testutil.ShortSocketDir(t)
	open := func() *os.File {
		f, err := OpenDir(unix.AT_FDCWD, path)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	w := workflow.New(context.Background(), "test-workflow")
	defer w.Cancel()
	c, err := startWorkflowControlIn(open(), w)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := startWorkflowControlIn(open(), w); err == nil {
		_ = duplicate.Close()
		t.Fatal("duplicate accepted")
	}
	root := open()
	defer root.Close()
	call := func(op string, want workflow.State) WorkflowReply {
		t.Helper()
		r, err := CallWorkflow(context.Background(), root, w.ID(), op)
		if err != nil || r.State != want || !r.Accepted {
			t.Fatalf("%s: %+v %v", op, r, err)
		}
		return r
	}
	call("status", workflow.Running)
	call("pause", workflow.Pausing)
	done := make(chan error, 1)
	go func() { done <- w.Checkpoint() }()
	workflowAwaitState(t, w, workflow.Paused)
	call("status", workflow.Paused)
	r, err := CallWorkflow(context.Background(), root, w.ID(), "resume")
	if err != nil || !r.Accepted || (r.State != workflow.Resuming && r.State != workflow.Running) {
		t.Fatalf("resume: %+v %v", r, err)
	}
	if err := workflowReceive(t, done); err != nil {
		t.Fatal(err)
	}
	call("pause", workflow.Pausing)
	go func() { done <- w.Checkpoint() }()
	workflowAwaitState(t, w, workflow.Paused)
	go func() { _ = w.Finish(<-done) }()
	r = call("stop", workflow.Stopped)
	if r.State != workflow.Stopped {
		t.Fatalf("stop did not wait for terminal cleanup: %+v", r)
	}
	if _, err := workflowApply(w, workflowRequest{ID: "other", Operation: "stop"}); err == nil {
		t.Fatal("wrong ID accepted")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	terminal := call("status", workflow.Stopped)
	if terminal.LastCompletedStep != 0 || terminal.TerminalError != "" {
		t.Fatalf("unexpected terminal status: %+v", terminal)
	}
	if rejected, err := CallWorkflow(context.Background(), root, w.ID(), "resume"); err != nil || rejected.Accepted || rejected.State != workflow.Stopped {
		t.Fatalf("terminal workflow accepted resume: %+v %v", rejected, err)
	}
	// A completed reservation with the same public ID can be safely reused.
	replacement := workflow.New(context.Background(), w.ID())
	reused, err := startWorkflowControlIn(open(), replacement)
	if err != nil {
		t.Fatal(err)
	}
	if finishErr := replacement.Finish(nil); finishErr != nil {
		t.Fatal(finishErr)
	}
	if err := reused.Close(); err != nil {
		t.Fatal(err)
	}
}
