package workflow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestWorkflowStopStatusReportsCanceledStep(t *testing.T) {
	w := New(context.Background(), "")
	w.BeginStep(2, "running")
	w.Stop()
	if err := w.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("finish error = %v, want canceled", err)
	}
	status := w.Status()
	if status.State != Stopped || status.CurrentStep != 2 || status.CurrentStepPhase != "canceled" {
		t.Fatalf("unexpected stopped status: %+v", status)
	}
}

func TestWorkflowStatusContainsNoErrorText(t *testing.T) {
	w := New(context.Background(), "")
	w.BeginStep(1, "failed")
	if err := w.Finish(errors.New("secret typed input must not enter status")); err == nil {
		t.Fatal("expected workflow failure")
	}
	status := w.Status()
	if status.LastCompletedStep != 0 || status.CurrentStep != 1 || status.CurrentStepPhase != "failed" || status.State != Failed || status.TerminalError != "operation_failed" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if strings.Contains(status.TerminalError, "secret") {
		t.Fatal("status exposed private error text")
	}
}
