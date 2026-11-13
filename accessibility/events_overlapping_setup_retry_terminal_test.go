package accessibility

import (
	"context"
	"errors"
	"testing"
)

// A subscription waiting on a finished setup owned by another caller must
// not spin indefinitely or acknowledge an event stream that was never
// installed. The retry allowance is bounded to avoid wedging new subscribers.
func TestATSPIEventOverlappingSetupHasBoundedRetryAndPreservesFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		priorErr  error
		cancelled bool
		want      error
	}{
		{"canceled starter exhausts retry allowance", context.Canceled, true, context.Canceled},
		{"provider failure is not retried", ErrUnsupported, false, ErrUnsupported},
		{"failure after canceled setup remains visible", ErrUnsupported, true, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, b, registry := startSyntheticEventBus(t, nil)
			done := make(chan struct{})
			close(done)
			b.eventStarting = &eventStart{done: done, err: tc.priorErr, callerCanceled: tc.cancelled}
			err := b.startEvents(context.Background(), true)
			if !errors.Is(err, tc.want) || b.eventCancel != nil || len(b.eventRegistered) != 0 {
				t.Fatalf("finished overlapping setup incorrectly started a stream: err=%v want=%v cancel=%v registrations=%v", err, tc.want, b.eventCancel, b.eventRegistered)
			}
			registry.mu.Lock()
			calls := len(registry.registered)
			registry.mu.Unlock()
			if calls != 0 {
				t.Fatalf("prior setup error triggered %d fresh registrations", calls)
			}
		})
	}
}
