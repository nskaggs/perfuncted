package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Closing XTEST must cancel both the active transport operation and a second
// operation queued at its serialization gate. The queued operation must never
// reach the device, even if the first operation releases the gate during Close.
func TestXTestCloseCancelsQueuedOperationWithoutDispatch(t *testing.T) {
	b := &XTestBackend{}
	entered := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- b.withOperation(context.Background(), func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first XTEST operation did not acquire gate")
	}
	secondDone := make(chan error, 1)
	secondStarted := make(chan struct{})
	dispatched := make(chan struct{}, 1)
	go func() {
		close(secondStarted)
		secondDone <- b.withOperation(context.Background(), func(context.Context) error { dispatched <- struct{}{}; return nil })
	}()
	<-secondStarted
	closeDone := make(chan error, 1)
	go func() { closeDone <- b.Close() }()
	for name, ch := range map[string]<-chan error{"active": firstDone, "queued": secondDone} {
		select {
		case err := <-ch:
			if !errors.Is(err, context.Canceled) && !(name == "queued" && errors.Is(err, errXTestBackendClosed)) {
				t.Errorf("%s operation completed after close: %v", name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s operation did not drain during close", name)
		}
	}
	select {
	case <-dispatched:
		t.Fatal("queued operation dispatched to closed XTEST device")
	default:
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close failed to drain queued operations")
	}
	if err := b.withOperation(context.Background(), func(context.Context) error { t.Error("post-close request dispatched"); return nil }); !errors.Is(err, errXTestBackendClosed) {
		t.Fatalf("post-close admission: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("idempotent close failed: %v", err)
	}
}
