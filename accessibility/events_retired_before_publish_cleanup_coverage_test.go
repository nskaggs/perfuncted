package accessibility

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Event setup that finishes without a live subscriber must compensate every
// provider registration and leave no dispatcher state published.
func TestEventSetupWithoutSubscriberCleansProviderRegistrations(t *testing.T) {
	_, backend, registry := startSyntheticEventBus(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := backend.startEvents(ctx, false); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("event setup without subscribers returned %v, want ErrDisconnected", err)
	}

	registry.mu.Lock()
	registered := append([]string(nil), registry.registered...)
	removed := append([]string(nil), registry.removed...)
	registry.mu.Unlock()
	if len(registered) == 0 || len(removed) != len(registered) {
		t.Fatalf("event setup cleanup registered %d families and removed %d", len(registered), len(removed))
	}
	remaining := make(map[string]int, len(registered))
	for _, family := range registered {
		remaining[family]++
	}
	for _, family := range removed {
		remaining[family]--
	}
	for family, count := range remaining {
		if count != 0 {
			t.Fatalf("provider family %q retained registration count %d", family, count)
		}
	}

	backend.eventsMu.Lock()
	defer backend.eventsMu.Unlock()
	if backend.eventStarting != nil || backend.eventCancel != nil || backend.eventDone != nil || backend.eventAccess != nil || len(backend.eventRegistered) != 0 {
		t.Fatalf("unpublished event dispatcher left live state: starting=%v cancel=%v done=%v access=%v registered=%v", backend.eventStarting, backend.eventCancel != nil, backend.eventDone != nil, backend.eventAccess != nil, backend.eventRegistered)
	}
}
