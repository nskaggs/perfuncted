package input

import (
	"context"
	"errors"
	"testing"
)

// A caller may cancel after the transport operation starts but before its
// reply is acknowledged. XTEST must not report success from that operation;
// similarly, a device failure and cancellation must both remain observable.
func TestXTestOperationDoesNotAcknowledgeLateCanceledRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail error
	}{
		{name: "transport succeeded"},
		{name: "transport failed", fail: errors.New("synthetic X11 operation failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &XTestBackend{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			executed := false
			err := b.withOperation(ctx, func(context.Context) error {
				executed = true
				cancel()
				return tc.fail
			})
			if !executed {
				t.Fatal("operation never entered")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("late cancellation acknowledged as success: %v", err)
			}
			if tc.fail != nil && !errors.Is(err, tc.fail) {
				t.Fatalf("device failure lost after cancellation: %v", err)
			}
		})
	}
}
