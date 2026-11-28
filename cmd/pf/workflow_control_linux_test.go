//go:build linux

package main

import (
	"github.com/nskaggs/perfuncted/v2/internal/sessionregistry"

	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/internal/workflow"
	"github.com/nskaggs/perfuncted/v2/pftest"
)

func TestWorkflowCLIStopCancelsAndPersistsStatus(t *testing.T) {
	id := "wf-" + workflow.New(context.Background(), "").ID()[:24]
	script := filepath.Join(t.TempDir(), "task.pf")
	if err := os.WriteFile(script, []byte("input sendkeys first\ninput sendkeys second\n"), 0600); err != nil {
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
	if err != nil || status.State != workflow.Running || status.CurrentStep != 1 || status.CurrentStepPhase != "running" {
		t.Fatalf("running status = %+v, err = %v", status, err)
	}
	stopped, err := executeWorkflowCommand(t, "stop", id)
	if err != nil || stopped.State != workflow.Stopped || stopped.CurrentStep != 1 || stopped.CurrentStepPhase != "canceled" {
		t.Fatalf("stop result = %+v, err = %v", stopped, err)
	}
	workflowReceive(t, in.cleaned)
	if err := workflowReceive(t, runDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("controlled run error = %v, want context canceled", err)
	}
	terminal, err := executeWorkflowCommand(t, "status", id)
	if err != nil || terminal.State != workflow.Stopped || terminal.CurrentStep != 1 || terminal.CurrentStepPhase != "canceled" {
		t.Fatalf("persisted status = %+v, err = %v", terminal, err)
	}
	root, err := sessionregistry.WorkflowRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err := sessionregistry.Recover(root, id); err != nil {
		root.Close()
		t.Fatalf("remove completed test reservation: %v", err)
	}
	root.Close()
}

func executeWorkflowCommand(t *testing.T, operation, id string) (sessionregistry.WorkflowReply, error) {
	t.Helper()
	cmd := workflowCmd()
	var output bytes.Buffer
	cmd.SetArgs([]string{operation, id, "--output", "json"})
	cmd.SetOut(&output)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	var reply sessionregistry.WorkflowReply
	if decodeErr := json.Unmarshal(output.Bytes(), &reply); decodeErr != nil {
		return reply, errors.Join(err, fmt.Errorf("decode workflow response %q: %w", output.String(), decodeErr))
	}
	return reply, err
}

func TestWorkflowControlWaitsForCancellationCleanup(t *testing.T) {
	w := workflow.New(context.Background(), "")
	input := &workflowTestInput{entered: make(chan string, 1), proceed: make(chan struct{}), cleaned: make(chan struct{})}
	session := pftest.New(&pftest.Screenshotter{}, input, &pftest.Manager{}, nil)
	defer session.Close()
	runner := &scriptRunner{ctx: context.Background(), lifecycle: w, out: &bytes.Buffer{}, pf: session}
	done := make(chan error, 1)
	go func() { done <- runner.run(strings.NewReader("input sendkeys first\n")) }()
	workflowReceive(t, input.entered)
	w.Stop()
	if err := workflowReceive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want canceled command", err)
	}
	workflowReceive(t, input.cleaned)
	status := w.Status()
	if status.State != workflow.Stopped {
		t.Fatalf("workflow not stopped after command cleanup: %+v", status)
	}
}

func TestControlledWorkflowRejectsHeldKeyBeforeInput(t *testing.T) {
	inputter := &pftest.Inputter{}
	session := pftest.New(&pftest.Screenshotter{}, inputter, &pftest.Manager{}, nil)
	defer session.Close()
	runner := &scriptRunner{ctx: context.Background(), externalLifecycle: true, out: io.Discard, pf: session}
	if err := runner.run(strings.NewReader("input sendkeys '{ctrl down}'\n")); err == nil {
		t.Fatal("accepted a held key across the workflow safe point")
	}
	if len(inputter.Calls) != 0 {
		t.Fatalf("invalid held-key sequence dispatched input: %v", inputter.Calls)
	}
	if err := runner.run(strings.NewReader("input sendkeys '{ctrl+s}'\n")); err != nil {
		t.Fatalf("atomic key chord failed: %v", err)
	}
	if len(inputter.Calls) != 1 || inputter.Calls[0] != "type:{ctrl+s}" {
		t.Fatalf("atomic chord dispatches = %v", inputter.Calls)
	}
}
