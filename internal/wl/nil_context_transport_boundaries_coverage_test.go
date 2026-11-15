package wl

import (
	"context"
	"errors"
	"testing"
)

// Low-level Wayland operations accept a nil caller context as an unrestricted
// request. With no connected transport they must remain inert rather than
// dereference nil or acquire a socket that is not owned by the caller.
func TestNilCallerContextAndMissingWaylandTransportAreSafe(t *testing.T) {
	var ctx Context
	if err := ctx.WriteMsgContext(nil, []byte{1, 2, 3, 4}, nil); err != nil {
		t.Fatalf("unconnected Wayland write with nil context: %v", err)
	}
	if err := ctx.DispatchContext(nil); err != nil {
		t.Fatalf("unconnected Wayland dispatch with nil context: %v", err)
	}
	if err := ctx.RoundTripContext(nil); err != nil {
		t.Fatalf("unconnected Wayland round-trip with nil context: %v", err)
	}
	called := 0
	if err := ctx.WithOperationContext(nil, func() error { called++; return nil }); err != nil || called != 1 {
		t.Fatalf("nil caller context could not run safe operation: calls=%d err=%v", called, err)
	}
}

// Cancellation must take precedence over missing transport in each protocol
// operation, without executing a user callback.
func TestCanceledWaylandProtocolOperationsRejectBeforeTransport(t *testing.T) {
	var ctx Context
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"write", func() error { return ctx.WriteMsgContext(canceled, nil, nil) }},
		{"dispatch", func() error { return ctx.DispatchContext(canceled) }},
		{"round trip", func() error { return ctx.RoundTripContext(canceled) }},
		{"operation", func() error { return ctx.WithOperationContext(canceled, func() error { called = true; return nil }) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled request accepted: %v", err)
			}
		})
	}
	if called {
		t.Fatal("canceled operation invoked callback")
	}
}
