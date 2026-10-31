package input

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// cancelAfterEISWaitContext cancels after the selected setup message has been
// removed from the EIS stream and validated by eiClient.next.
type cancelAfterEISWaitContext struct {
	context.Context
	cancel    context.CancelFunc
	waits     atomic.Uint32
	errCalls  atomic.Uint32
	cancelErr uint32
	cancelAt  uint32
	cancelled atomic.Bool
}

func (c *cancelAfterEISWaitContext) Done() <-chan struct{} {
	c.waits.Add(1)
	return c.Context.Done()
}

func (c *cancelAfterEISWaitContext) Err() error {
	err := c.Context.Err()
	errCall := c.errCalls.Add(1)
	triggered := (c.cancelAt != 0 && c.waits.Load() == c.cancelAt) || (c.cancelErr != 0 && errCall == c.cancelErr)
	if triggered && err == nil && c.cancelled.CompareAndSwap(false, true) {
		c.cancel()
	}
	return err
}

func TestEISenderDoesNotOutliveCancellationAfterFinalReadinessEvent(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cancelAtWait uint32
		cancelAtErr  uint32
		wantStarted  bool
	}{
		{name: "after final readiness event", cancelAtWait: 19},
		{name: "before device activation", cancelAtErr: 39},
		{name: "during device activation", cancelAtErr: 40, wantStarted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientConn, serverConn := unixConnectionPair(t)
			t.Cleanup(func() { _ = serverConn.Close() })
			base, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			// The synthetic peer sends 19 setup events, ending with the successful
			// synchronization callback after all input devices have resumed.
			ctx := &cancelAfterEISWaitContext{
				Context: base, cancel: cancel, cancelAt: tc.cancelAtWait, cancelErr: tc.cancelAtErr,
			}
			peerDone := make(chan error, 1)
			go func() { peerDone <- serveSyntheticAuthorizedEISPeer(serverConn) }()

			sender, err := setupEISender(ctx, clientConn)
			_ = clientConn.Close()
			if got := ctx.waits.Load(); got != 19 || !ctx.cancelled.Load() {
				t.Fatalf("setup canceled after wait %d (triggered=%v), want final readiness wait 19", got, ctx.cancelled.Load())
			}
			select {
			case peerErr := <-peerDone:
				if tc.wantStarted {
					if peerErr != nil {
						t.Fatalf("synthetic EIS peer did not observe device activation: %v", peerErr)
					}
				} else if !errors.Is(peerErr, io.EOF) {
					t.Fatalf("device activation occurred or peer failed early: %v", peerErr)
				}
			case <-time.After(time.Second):
				t.Fatal("synthetic EIS peer did not finish after setup cancellation")
			}
			if sender != nil {
				_ = sender.client.close()
				t.Fatalf("EIS setup returned a live sender after caller cancellation: %v", err)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("EIS setup error = %v, want caller cancellation", err)
			}
		})
	}
}
