//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"golang.org/x/sys/unix"
)

func TestWorkflowControlProtocol(t *testing.T) {
	path := shortPrivateSocketDir(t)
	open := func() *os.File {
		f, err := namedOpenDir(unix.AT_FDCWD, path)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	w := newWorkflowLifecycle(context.Background())
	w.id = "test-workflow"
	defer w.cancel()
	c, err := startWorkflowControlIn(open(), w)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := startWorkflowControlIn(open(), w); err == nil {
		_ = duplicate.close()
		t.Fatal("duplicate accepted")
	}
	root := open()
	defer root.Close()
	call := func(op string, want workflowState) workflowReply {
		t.Helper()
		r, err := workflowCall(context.Background(), root, w.id, op)
		if err != nil || r.State != want || !r.Accepted {
			t.Fatalf("%s: %+v %v", op, r, err)
		}
		return r
	}
	call("status", workflowRunning)
	call("pause", workflowPausing)
	done := make(chan error, 1)
	go func() { done <- w.checkpoint() }()
	workflowAwaitState(t, w, workflowPaused)
	call("status", workflowPaused)
	r, err := workflowCall(context.Background(), root, w.id, "resume")
	if err != nil || !r.Accepted || (r.State != workflowResuming && r.State != workflowRunning) {
		t.Fatalf("resume: %+v %v", r, err)
	}
	if err := workflowReceive(t, done); err != nil {
		t.Fatal(err)
	}
	call("pause", workflowPausing)
	go func() { done <- w.checkpoint() }()
	workflowAwaitState(t, w, workflowPaused)
	go func() { _ = w.finish(<-done) }()
	r = call("stop", workflowStopped)
	if r.State != workflowStopped {
		t.Fatalf("stop did not wait for terminal cleanup: %+v", r)
	}
	if _, err := workflowApply(w, workflowRequest{ID: "other", Operation: "stop"}); err == nil {
		t.Fatal("wrong ID accepted")
	}
	if err := c.close(); err != nil {
		t.Fatal(err)
	}
	terminal := call("status", workflowStopped)
	if terminal.LastCompletedStep != 0 || terminal.TerminalError != "" {
		t.Fatalf("unexpected terminal status: %+v", terminal)
	}
	if rejected, err := workflowCall(context.Background(), root, w.id, "resume"); err != nil || rejected.Accepted || rejected.State != workflowStopped {
		t.Fatalf("terminal workflow accepted resume: %+v %v", rejected, err)
	}
	// A completed reservation with the same public ID can be safely reused.
	replacement := newWorkflowLifecycle(context.Background())
	replacement.id = w.id
	reused, err := startWorkflowControlIn(open(), replacement)
	if err != nil {
		t.Fatal(err)
	}
	if finishErr := replacement.finish(nil); finishErr != nil {
		t.Fatal(finishErr)
	}
	if err := reused.close(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowControlSeparateProcessHelper(t *testing.T) {
	if os.Getenv("PF_WORKFLOW_CONTROL_HELPER") != "1" {
		return
	}
	root, err := namedOpenDir(unix.AT_FDCWD, os.Getenv("PF_WORKFLOW_CONTROL_ROOT"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	reply, err := workflowCall(context.Background(), root, os.Getenv("PF_WORKFLOW_CONTROL_ID"), "status")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(reply); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowControlUsesSeparateCLIProcess(t *testing.T) {
	path := shortPrivateSocketDir(t)
	root, err := namedOpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	w := newWorkflowLifecycle(context.Background())
	w.id = "workflow-subprocess"
	c, err := startWorkflowControlIn(root, w)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkflowControlSeparateProcessHelper$")
	cmd.Env = append(os.Environ(),
		"PF_WORKFLOW_CONTROL_HELPER=1",
		"PF_WORKFLOW_CONTROL_ROOT="+path,
		"PF_WORKFLOW_CONTROL_ID="+w.id,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("separate workflow control client: %v: %s", err, output)
	}
	var reply workflowReply
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&reply); err != nil {
		t.Fatalf("decode separate process response %q: %v", output, err)
	}
	if reply.ID != w.id || reply.State != workflowRunning || !reply.Accepted {
		t.Fatalf("unexpected separate process response: %+v", reply)
	}
	if finishErr := w.finish(nil); finishErr != nil {
		t.Fatal(finishErr)
	}
}

func TestWorkflowCLIStopCancelsAndPersistsStatus(t *testing.T) {
	id := "wf-" + newWorkflowLifecycle(context.Background()).id[:24]
	script := filepath.Join(t.TempDir(), "task.pf")
	if err := os.WriteFile(script, []byte("input type first\ninput type second\n"), 0600); err != nil {
		t.Fatal(err)
	}
	in := &workflowTestInput{entered: make(chan string, 2), proceed: make(chan struct{}), cleaned: make(chan struct{})}
	open := func(context.Context) (*perfuncted.Session, error) {
		return pftest.New(&pftest.Screenshotter{}, in, &pftest.Manager{}, nil), nil
	}
	run := runCmd(open, nil)
	run.SetArgs([]string{script, "--workflow-id", id})
	run.SetOut(io.Discard)
	run.SetErr(io.Discard)
	runDone := make(chan error, 1)
	runFinished := make(chan struct{})
	go func() {
		runDone <- run.Execute()
		close(runFinished)
	}()
	defer func() {
		select {
		case <-runFinished:
		default:
			_, _ = executeWorkflowCommand(t, "stop", id)
			select {
			case <-runFinished:
			case <-time.After(5 * time.Second):
				t.Error("controlled run did not stop during test cleanup")
			}
		}
	}()
	if got := workflowReceive(t, in.entered); got != "first" {
		t.Fatalf("first input = %q", got)
	}
	status, err := executeWorkflowCommand(t, "status", id)
	if err != nil || status.State != workflowRunning || status.CurrentStep != 1 || status.CurrentStepPhase != "running" {
		t.Fatalf("running status = %+v, err = %v", status, err)
	}
	stopped, err := executeWorkflowCommand(t, "stop", id)
	if err != nil || stopped.State != workflowStopped || stopped.CurrentStep != 1 || stopped.CurrentStepPhase != "canceled" {
		t.Fatalf("stop result = %+v, err = %v", stopped, err)
	}
	workflowReceive(t, in.cleaned)
	if err := workflowReceive(t, runDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("controlled run error = %v, want context canceled", err)
	}
	terminal, err := executeWorkflowCommand(t, "status", id)
	if err != nil || terminal.State != workflowStopped || terminal.CurrentStep != 1 || terminal.CurrentStepPhase != "canceled" {
		t.Fatalf("persisted status = %+v, err = %v", terminal, err)
	}
	root, err := workflowRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := namedRecover(root, id); err != nil {
		root.Close()
		t.Fatalf("remove completed test reservation: %v", err)
	}
	root.Close()
}

func executeWorkflowCommand(t *testing.T, operation, id string) (workflowReply, error) {
	t.Helper()
	cmd := workflowCmd()
	var output bytes.Buffer
	cmd.SetArgs([]string{operation, id, "--output", "json"})
	cmd.SetOut(&output)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	var reply workflowReply
	if decodeErr := json.Unmarshal(output.Bytes(), &reply); decodeErr != nil {
		return reply, errors.Join(err, fmt.Errorf("decode workflow response %q: %w", output.String(), decodeErr))
	}
	return reply, err
}

func TestWorkflowControlRejectsReusedPIDIdentity(t *testing.T) {
	path := shortPrivateSocketDir(t)
	root, err := namedOpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	w := newWorkflowLifecycle(context.Background())
	w.id = "workflow-pid-check"
	c, err := startWorkflowControlIn(root, w)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
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
	clientRoot, err := namedOpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	defer clientRoot.Close()
	if _, err := workflowCall(context.Background(), clientRoot, w.id, "status"); !errors.Is(err, errNamedStale) {
		t.Fatalf("got %v, want stale owner identity", err)
	}
	if finishErr := w.finish(nil); finishErr != nil {
		t.Fatal(finishErr)
	}
}

func TestWorkflowControlRejectsSocketSymlink(t *testing.T) {
	path := shortPrivateSocketDir(t)
	root, err := namedOpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	w := newWorkflowLifecycle(context.Background())
	w.id = "workflow-symlink"
	c, err := startWorkflowControlIn(root, w)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if err := unix.Unlinkat(int(c.owner.dir.Fd()), "control", 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.Symlinkat("/etc/passwd", int(c.owner.dir.Fd()), "control"); err != nil {
		t.Fatal(err)
	}
	clientRoot, err := namedOpenDir(unix.AT_FDCWD, path)
	if err != nil {
		t.Fatal(err)
	}
	defer clientRoot.Close()
	if _, err := workflowCall(context.Background(), clientRoot, w.id, "status"); err == nil || !strings.Contains(err.Error(), "unsafe workflow socket") {
		t.Fatalf("socket symlink error = %v, want unsafe endpoint", err)
	}
	if _, err := os.Stat("/etc/passwd"); err != nil {
		t.Fatalf("symlink target unexpectedly unavailable: %v", err)
	}
	if finishErr := w.finish(nil); finishErr != nil {
		t.Fatal(finishErr)
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

func TestWorkflowStatusContainsNoErrorText(t *testing.T) {
	w := newWorkflowLifecycle(context.Background())
	w.beginStep(1, "failed")
	if err := w.finish(errors.New("secret typed input must not enter status")); err == nil {
		t.Fatal("expected workflow failure")
	}
	status := w.status()
	if status.LastCompletedStep != 0 || status.CurrentStep != 1 || status.CurrentStepPhase != "failed" || status.State != workflowFailed || status.TerminalError != "operation_failed" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if strings.Contains(status.TerminalError, "secret") {
		t.Fatal("status exposed private error text")
	}
}

func TestWorkflowStopStatusReportsCanceledStep(t *testing.T) {
	w := newWorkflowLifecycle(context.Background())
	w.beginStep(2, "running")
	w.stop()
	if err := w.finish(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("finish error = %v, want canceled", err)
	}
	status := w.status()
	if status.State != workflowStopped || status.CurrentStep != 2 || status.CurrentStepPhase != "canceled" {
		t.Fatalf("unexpected stopped status: %+v", status)
	}
}

func TestWorkflowControlFailureDoesNotAffectOtherRun(t *testing.T) {
	first := newWorkflowLifecycle(context.Background())
	second := newWorkflowLifecycle(context.Background())
	defer first.cancel()
	defer second.cancel()
	if _, err := workflowApply(first, workflowRequest{ID: second.id, Operation: "stop"}); err == nil {
		t.Fatal("foreign workflow ID accepted")
	}
	if err := first.ctx.Err(); err != nil {
		t.Fatalf("invalid request canceled workflow: %v", err)
	}
	if err := second.ctx.Err(); err != nil {
		t.Fatalf("invalid request canceled other workflow: %v", err)
	}
	if first.status().State != workflowRunning || second.status().State != workflowRunning {
		t.Fatal("invalid request changed a workflow state")
	}
}

func TestWorkflowControlWaitsForCancellationCleanup(t *testing.T) {
	w := newWorkflowLifecycle(context.Background())
	input := &workflowTestInput{entered: make(chan string, 1), proceed: make(chan struct{}), cleaned: make(chan struct{})}
	session := pftest.New(&pftest.Screenshotter{}, input, &pftest.Manager{}, nil)
	defer session.Close()
	runner := &scriptRunner{ctx: context.Background(), lifecycle: w, out: &bytes.Buffer{}, pf: session}
	done := make(chan error, 1)
	go func() { done <- runner.run(strings.NewReader("input type first\n")) }()
	workflowReceive(t, input.entered)
	w.stop()
	if err := workflowReceive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want canceled command", err)
	}
	workflowReceive(t, input.cleaned)
	status := w.status()
	if status.State != workflowStopped {
		t.Fatalf("workflow not stopped after command cleanup: %+v", status)
	}
}

func TestControlledWorkflowRejectsHeldKeyBeforeInput(t *testing.T) {
	inputter := &pftest.Inputter{}
	session := pftest.New(&pftest.Screenshotter{}, inputter, &pftest.Manager{}, nil)
	defer session.Close()
	runner := &scriptRunner{ctx: context.Background(), externalLifecycle: true, out: io.Discard, pf: session}
	if err := runner.run(strings.NewReader("input type '{ctrl down}'\n")); err == nil {
		t.Fatal("accepted a held key across the workflow safe point")
	}
	if len(inputter.Calls) != 0 {
		t.Fatalf("invalid held-key sequence dispatched input: %v", inputter.Calls)
	}
	if err := runner.run(strings.NewReader("input type '{ctrl+s}'\n")); err != nil {
		t.Fatalf("atomic key chord failed: %v", err)
	}
	if len(inputter.Calls) != 1 || inputter.Calls[0] != "type:{ctrl+s}" {
		t.Fatalf("atomic chord dispatches = %v", inputter.Calls)
	}
}

func TestWorkflowWaitTerminalDoesNotPoll(t *testing.T) {
	w := newWorkflowLifecycle(context.Background())
	done := make(chan struct{})
	go func() {
		workflowWaitTerminal(w, time.Second)
		close(done)
	}()
	if finishErr := w.finish(nil); finishErr != nil {
		t.Fatal(finishErr)
	}
	workflowReceive(t, done)
}
