//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"testing"
	"time"
)

// D-Bus can acknowledge input or screenshot operations after their caller has
// revoked authority. Both transports must return cancellation, preserving a
// distinct cause when present, and must not report a successful side effect.
func TestGNOMEBridgeInFlightInputAndCaptureCancellation(t *testing.T) {
	for _, tc := range []struct {
		name          string
		capture       bool
		distinctCause bool
	}{
		{name: "input cancellation with reason", distinctCause: true},
		{name: "input plain cancellation"},
		{name: "capture plain cancellation", capture: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			object := &blockingObject{seen: make(chan context.Context, 1)}
			client := &Client{obj: object, supportsFDs: true}
			ctx, cancel := context.WithCancelCause(context.Background())
			result := make(chan error, 1)
			go func() {
				if tc.capture {
					result <- client.capture(ctx, "CaptureFull", 0, nil)
					return
				}
				result <- client.Key(ctx, 65, true)
			}()
			select {
			case <-object.seen:
			case <-time.After(time.Second):
				cancel(nil)
				t.Fatal("in-flight operation never reached its bounded bridge call")
			}
			reason := errors.New("input session authority withdrawn")
			if tc.distinctCause {
				cancel(reason)
			} else {
				cancel(nil)
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("revoked operation reported success or wrong failure: %v", err)
				}
				if tc.distinctCause && !errors.Is(err, reason) {
					t.Fatalf("revocation cause was lost: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("bridge operation did not unblock when canceled")
			}
		})
	}
}
