package main

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
)

func TestAccessibilityEventsPropagateOutputFailure(t *testing.T) {
	failure := errors.New("event output consumer failed")
	out := &screenCommandFailureWriter{failAt: 1, failure: failure}
	cmd := accessibilityCmd(func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliEventsFake{}), nil
	}, nil)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(out)
	cmd.SetArgs([]string{"events"})
	err := cmd.ExecuteContext(context.Background())
	if !errors.Is(err, failure) || out.writes != 1 {
		t.Fatalf("event output error=%v writes=%d, want original writer failure", err, out.writes)
	}
}
