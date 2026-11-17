package accessibility

import (
	"context"
	"errors"
	"testing"
)

// A provider may apply an AT-SPI mutation before its D-Bus reply reaches the
// caller. The backend must report cancellation and preserve dispatch uncertainty.
func TestSetValueReportsCancellationAfterRealProviderAppliesValue(t *testing.T) {
	backend, id, provider := newOptionalATSPINode(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider.mu.Lock()
	provider.cancelMutation = cancel
	provider.mu.Unlock()

	err := backend.SetValue(ctx, id, 42.75)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SetValue acknowledged late provider reply: %v", err)
	}
	provider.mu.RLock()
	got := provider.values[valueIface+".CurrentValue"]
	lastMutation := provider.lastMutation
	provider.mu.RUnlock()
	if got != 42.75 || lastMutation != "set-value" {
		t.Fatalf("provider mutation was not observed before cancellation: value=%v mutation=%q", got, lastMutation)
	}
}

func TestInvokeActionReportsUnknownAfterRealProviderCancelsReply(t *testing.T) {
	backend, id, provider := newOptionalATSPINode(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider.mu.Lock()
	provider.cancelAction = cancel
	provider.mu.Unlock()

	action, err := backend.InvokeActionByName(ctx, id, "press")
	var dispatch *ActionInvocationError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &dispatch) || dispatch.Dispatch != DispatchUnknown {
		t.Fatalf("canceled provider action result=%+v err=%v, want unknown dispatch and context cancellation", action, err)
	}
	if action != (Action{}) {
		t.Fatalf("canceled action exposed provider metadata: %+v", action)
	}
	provider.mu.RLock()
	count, last := provider.actionCount, provider.lastAction
	provider.mu.RUnlock()
	if count != 1 || last != 0 {
		t.Fatalf("provider action was not dispatched exactly once: count=%d index=%d", count, last)
	}
}
