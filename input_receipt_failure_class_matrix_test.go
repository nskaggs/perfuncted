package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/input"
)

func TestInputReceiptClassifiesAdversarialFailureAndDispatch(t *testing.T) {
	backendErr := errors.New("synthetic backend transport interrupted after dispatch")
	cases := []struct {
		name            string
		failure         error
		dispatch, class string
	}{
		{name: "success", dispatch: "accepted"},
		{name: "caller canceled", failure: context.Canceled, dispatch: "unknown", class: "canceled"},
		{name: "deadline exceeded", failure: context.DeadlineExceeded, dispatch: "unknown", class: "deadline-exceeded"},
		{name: "invalid argument", failure: ErrInvalidArgument, dispatch: "not-sent", class: "rejected-before-dispatch"},
		{name: "unsupported capability", failure: ErrUnsupported, dispatch: "not-sent", class: "rejected-before-dispatch"},
		{name: "input backend unsupported", failure: input.ErrNotSupported, dispatch: "not-sent", class: "rejected-before-dispatch"},
		{name: "transport unknown", failure: backendErr, dispatch: "unknown", class: "operation-failed"},
		{name: "wrapped cancellation", failure: errors.Join(backendErr, context.Canceled), dispatch: "unknown", class: "canceled"},
		{name: "wrapped deadline", failure: errors.Join(backendErr, context.DeadlineExceeded), dispatch: "unknown", class: "deadline-exceeded"},
		{name: "wrapped invalid argument", failure: errors.Join(backendErr, ErrInvalidArgument), dispatch: "not-sent", class: "rejected-before-dispatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bundle := &InputBundle{}
			started := time.Now().Add(-time.Second)
			returned := bundle.recordInputReceipt("key-down", started, tc.failure)
			if !errors.Is(returned, tc.failure) && !(returned == nil && tc.failure == nil) {
				t.Fatalf("receipt changed original error: got=%v want=%v", returned, tc.failure)
			}
			receipts := bundle.recentInputReceipts()
			if len(receipts) != 1 {
				t.Fatalf("receipts=%+v", receipts)
			}
			got := receipts[0]
			if got.Dispatch != tc.dispatch || got.FailureClass != tc.class || got.Operation != "key-down" || got.Sequence != 1 {
				t.Fatalf("receipt=%+v want dispatch=%q class=%q", got, tc.dispatch, tc.class)
			}
			if got.StartedAt.After(got.CompletedAt) || got.StartedAt.IsZero() || got.CompletedAt.IsZero() {
				t.Fatalf("invalid timestamps: %+v", got)
			}
		})
	}
}
