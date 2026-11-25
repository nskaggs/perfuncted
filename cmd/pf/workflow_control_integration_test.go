//go:build integration && linux

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
)

func TestWorkflowControlHeadlessWayland(t *testing.T) {
	if os.Getenv("PF_TEST_DISPLAY_SERVER") != "headless-wayland" {
		t.Skip("requires the managed headless Wayland session")
	}
	id := "wf-integration-" + newWorkflowLifecycle(context.Background()).id[:12]
	script := filepath.Join(t.TempDir(), "workflow.pf")
	longInput := strings.Repeat("a", 2000)
	if err := os.WriteFile(script, []byte("input type first\ninput type "+longInput+"\ninput type must-not-run\n"), 0600); err != nil {
		t.Fatal(err)
	}
	open := func(ctx context.Context) (*perfuncted.Session, error) {
		return perfuncted.Open(ctx,
			perfuncted.WithHeadless(perfuncted.SessionConfig{LogDir: filepath.Join(os.TempDir(), "perfuncted-logs", id)}),
			perfuncted.Require(perfuncted.CapabilityInput),
			perfuncted.WithTrace(true),
			perfuncted.WithTraceOut(io.Discard),
		)
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
			_, _ = workflowIntegrationCall(t, id, "stop")
			select {
			case <-runFinished:
			case <-time.After(15 * time.Second):
				t.Error("headless controlled run did not stop during cleanup")
			}
		}
		root, err := workflowRegistry()
		if err == nil {
			if recoverErr := namedRecover(root, id); recoverErr != nil && !errors.Is(recoverErr, os.ErrNotExist) {
				t.Errorf("recover completed test workflow: %v", recoverErr)
			}
			_ = root.Close()
		}
	}()

	if _, err := waitWorkflowIntegrationState(t, id, 10*time.Second, func(status workflowStatus) bool {
		return status.State == workflowRunning
	}); err != nil {
		t.Fatal(err)
	}
	if paused, err := workflowIntegrationCall(t, id, "pause"); err != nil || !paused.Accepted || paused.State != workflowPausing {
		t.Fatalf("pause request = %+v, err = %v", paused, err)
	}
	paused, err := waitWorkflowIntegrationState(t, id, 15*time.Second, func(status workflowStatus) bool {
		return status.State == workflowPaused && status.CurrentStep == 1 && status.CurrentStepPhase == "waiting"
	})
	if err != nil {
		t.Fatal(err)
	}
	if paused.LastCompletedStep != 0 {
		t.Fatalf("paused before first command but completed %d steps", paused.LastCompletedStep)
	}
	if resumed, err := workflowIntegrationCall(t, id, "resume"); err != nil || !resumed.Accepted {
		t.Fatalf("resume = %+v, err = %v", resumed, err)
	}
	if _, err := waitWorkflowIntegrationState(t, id, 10*time.Second, func(status workflowStatus) bool {
		return status.State == workflowRunning && status.CurrentStep == 2 && status.CurrentStepPhase == "running" && status.LastCompletedStep == 1
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if status, err := workflowReadIntegrationStatus(id); err != nil || status.State != workflowRunning || status.CurrentStep != 2 {
		t.Fatalf("long input did not remain in flight before stop: %+v, err = %v", status, err)
	}
	stopped, err := workflowIntegrationCall(t, id, "stop")
	if err != nil || !stopped.Accepted || stopped.State != workflowStopped || stopped.CurrentStep != 2 || stopped.CurrentStepPhase != "canceled" || stopped.LastCompletedStep != 1 {
		t.Fatalf("stop result = %+v, err = %v", stopped, err)
	}
	if err := workflowReceive(t, runDone); !errors.Is(err, context.Canceled) {
		t.Fatalf("run result = %v, want context canceled", err)
	}
	if status, err := workflowReadIntegrationStatus(id); err != nil || status.State != workflowStopped || status.CurrentStep != 2 {
		t.Fatalf("terminal status = %+v, err = %v", status, err)
	}
}

func workflowIntegrationCall(t *testing.T, id, operation string) (workflowReply, error) {
	t.Helper()
	// Invoke the public Cobra command against the live owner rather than
	// calling the underlying socket transport directly.
	return executeWorkflowCommand(t, operation, id)
}

func workflowReadIntegrationStatus(id string) (workflowStatus, error) {
	root, err := workflowRegistry()
	if err != nil {
		return workflowStatus{}, err
	}
	defer root.Close()
	reply, err := workflowCall(context.Background(), root, id, "status")
	return workflowStatus{
		ID:                reply.ID,
		State:             reply.State,
		LastCompletedStep: reply.LastCompletedStep,
		CurrentStep:       reply.CurrentStep,
		CurrentStepPhase:  reply.CurrentStepPhase,
		TerminalError:     reply.TerminalError,
	}, err
}

func waitWorkflowIntegrationState(t *testing.T, id string, timeout time.Duration, match func(workflowStatus) bool) (workflowStatus, error) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		status, err := workflowReadIntegrationStatus(id)
		if err == nil && match(status) {
			return status, nil
		}
		select {
		case <-deadline.C:
			return status, errors.New("timed out waiting for workflow state")
		case <-time.After(25 * time.Millisecond):
		}
	}
}
