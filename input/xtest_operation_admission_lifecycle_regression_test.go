package input

import (
	"context"
	"errors"
	"testing"
)

// Direct operation admission must reject nil and retired XTEST transports,
// preserve caller cancellation, and release each accepted operation exactly
// once. No such path may authorize device activity after shutdown.
func TestXTestOperationAdmissionHonorsCancellationAndShutdown(t *testing.T) {
	var absent *XTestBackend
	if ctx, finish, err := absent.beginOperation(context.Background()); err == nil || ctx != nil || finish != nil {
		t.Fatalf("nil backend admitted operation: ctx=%v finishNil=%t err=%v", ctx, finish == nil, err)
	}
	b := &XTestBackend{}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if ctx, finish, err := b.beginOperation(canceled); !errors.Is(err, context.Canceled) || ctx != nil || finish != nil {
		t.Fatalf("canceled operation admitted: ctx=%v finishNil=%t err=%v", ctx, finish == nil, err)
	}
	active, finish, err := b.beginOperation(context.Background())
	if err != nil || active == nil || finish == nil {
		t.Fatalf("live operation admission failed: ctx=%v err=%v", active, err)
	}
	finish()
	if err := b.Close(); err != nil {
		t.Fatalf("backend close: %v", err)
	}
	if ctx, finish, err := b.beginOperation(context.Background()); !errors.Is(err, errXTestBackendClosed) || ctx != nil || finish != nil {
		t.Fatalf("retired backend admitted operation: ctx=%v finishNil=%t err=%v", ctx, finish == nil, err)
	}
	if err := b.withOperation(context.Background(), func(context.Context) error { t.Error("post-close transport dispatched"); return nil }); !errors.Is(err, errXTestBackendClosed) {
		t.Fatalf("retired operation result: %v", err)
	}
}
