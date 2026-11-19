package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

// The window-close waiter must propagate a compositor query error rather
// than interpreting an unavailable backend as an empty window list.
func TestWaitClosePropagatesCompositorQueryFailure(t *testing.T) {
	failure := errors.New("synthetic window-list transport failure")
	backend := &pftest.Manager{Err: failure}
	opens := 0
	cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) {
		opens++
		return pftest.New(nil, nil, backend, nil), nil
	}, nil)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"wait-close", "title=Editor", "--poll=1ms", "--timeout=200ms"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("backend query failure lost: %v", err)
	}
	if opens != 1 || stdout.Len() != 0 {
		t.Fatalf("query failure reported success: opens=%d output=%q", opens, stdout.String())
	}
}

// A denied desktop session cannot act on or report a maximized window.
func TestMaximizePropagatesSessionAuthorizationFailure(t *testing.T) {
	denied := errors.New("synthetic window authorization denied")
	opens := 0
	cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) { opens++; return nil, denied }, nil)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"maximize", "Editor"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, denied) {
		t.Fatalf("authorization failure lost: %v", err)
	}
	if opens != 1 || stdout.Len() != 0 {
		t.Fatalf("authorization rejection reported success: opens=%d output=%q", opens, stdout.String())
	}
}

// A nonexistent maximize target must not dispatch a mutation to a different
// available window with a similar title.
func TestMaximizeMissingTargetRefusesMutation(t *testing.T) {
	manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "different", Title: "Unrelated"}}}}
	cmd := windowCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, manager, nil), nil }, nil)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"maximize", "Editor"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, window.ErrWindowNotFound) {
		t.Fatalf("missing target accepted: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("missing target claimed success: %q", stdout.String())
	}
}
