package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A working session bus that has not yet published the AT-SPI address must
// preserve the expiration of the caller's wait. An intermediate empty reply
// is useful diagnostic context but must not replace DeadlineExceeded.
func TestAccessibilityAddressTimeoutPreservesDeadlineAfterEmptyReplies(t *testing.T) {
	provider := &syntheticAccessibilityAddress{responses: []string{""}}
	addr := startSessionAccessibilityBus(t, provider)
	infra := &sessionInfra{dbusAddr: addr, timeouts: TimeoutPolicy{Poll: time.Millisecond}}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	address, err := infra.accessibilityBusAddress(ctx)
	if address != "" {
		t.Fatalf("unexpected address %q", address)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AT-SPI lookup lost deadline after empty successful bus replies: %v", err)
	}
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls == 0 {
		t.Fatal("synthetic AT-SPI provider was never queried")
	}
}
