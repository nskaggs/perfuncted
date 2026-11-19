package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/pftest"
)

// A failed button transition must not print success, or try the opposite
// transition to compensate for an action the compositor refused.
func TestInputButtonCommandsPropagateBackendRejection(t *testing.T) {
	denied := errors.New("synthetic compositor refused button transition")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"press", []string{"input", "mousedown", "--button", "1"}, "mousedown"},
		{"release", []string{"input", "mouseup", "--button", "1"}, "mouseup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &pftest.Inputter{Err: denied}
			cmd := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, backend, nil, nil), nil }
			})
			var out, stderr bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&stderr)
			cmd.SetArgs(tc.args)
			if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, denied) {
				t.Fatalf("provider rejection lost: %v", err)
			}
			if len(backend.Calls) != 1 || backend.Calls[0] != tc.want {
				t.Fatalf("unexpected input dispatched: %v", backend.Calls)
			}
			if out.Len() != 0 {
				t.Fatalf("rejected button command reported success: %q", out.String())
			}
		})
	}
}

type untrustedRelativeCoordinateInput struct{ *pftest.Inputter }

//nolint:unparam // The synthetic coordinate-space provider intentionally returns a valid contract with no error.
func (untrustedRelativeCoordinateInput) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	return input.CoordinateSpaceInfo{Kind: input.CoordinateSpacePhysical, ScaleX: 2, ScaleY: 2}, nil
}

func TestRelativePointerMoveRejectsUnverifiedCoordinateSpaceBeforeInput(t *testing.T) {
	backend := untrustedRelativeCoordinateInput{Inputter: &pftest.Inputter{}}
	session := pftest.New(nil, backend, nil, nil)
	cmd := inputCmd(nil, nil, func(context.Context) (*perfuncted.Session, error) { return session, nil })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"move-relative", "--dx", "3", "--dy", "4"})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("unverified coordinate-space move accepted")
	}
	if len(backend.Calls) != 0 || out.Len() != 0 {
		t.Fatalf("rejected coordinate-space move dispatched %v or printed %q", backend.Calls, out.String())
	}
}
