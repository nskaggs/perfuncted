package accessibility

import (
	"context"
	"errors"
	"testing"
)

func TestEventStartWaitingCannotAcknowledgeAlreadyCanceledCaller(t *testing.T) {
	for i := 0; i < 128; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		state := &eventStart{done: make(chan struct{})}
		close(state.done)
		cancel()
		retry, err := waitForEventStart(ctx, state, nil, 0)
		if retry || !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d completed stale event setup for canceled subscriber: retry=%v err=%v", i, retry, err)
		}
	}
}
func TestEventStartRetryOnlyForCanceledSetupAndHealthyWaiter(t *testing.T) {
	lost := errors.New("the AT-SPI event registry refused all subscriptions")
	disconnected := ErrDisconnected
	for _, tc := range []struct {
		name              string
		error, connection error
		callerCanceled    bool
		attempts          int
		retry             bool
		want              error
	}{
		{"successful setup", nil, nil, false, 0, false, nil},
		{"provider denied", lost, nil, false, 0, false, lost},
		{"retry healthy waiter after canceled starter", context.Canceled, nil, true, 0, true, nil},
		{"retries exhausted", context.Canceled, nil, true, eventStartRetryLimit, false, context.Canceled},
		{"connection lost during canceled setup", context.Canceled, disconnected, true, 0, false, disconnected},
		{"real provider failure cannot retry", lost, nil, true, 0, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &eventStart{done: make(chan struct{}), err: tc.error, callerCanceled: tc.callerCanceled}
			close(state.done)
			retry, err := waitForEventStart(context.Background(), state, tc.connection, tc.attempts)
			if retry != tc.retry || !errors.Is(err, tc.want) {
				t.Fatalf("incorrect event setup retry=%v err=%v want retry=%v err=%v", retry, err, tc.retry, tc.want)
			}
		})
	}
}
