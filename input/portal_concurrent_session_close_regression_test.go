package input

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// Multiple callers may retire a portal session concurrently after desktop
// authorization is revoked. Teardown must send one session Close and publish
// the same completed result to every waiter without racing or hanging.
func TestPortalSessionConcurrentCloseSendsOneAuthorizedClose(t *testing.T) {
	client, provider := startSyntheticPortalBus(t)
	p := &portalSession{
		conn:          client,
		path:          dbus.ObjectPath(syntheticSessionPath),
		resourcesDone: make(chan struct{}),
	}
	if err := p.subscribeClosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	const callers = 16
	var wg sync.WaitGroup
	result := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result <- p.close()
		}()
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent portal teardown blocked")
	}
	close(result)
	for err := range result {
		if err != nil {
			t.Fatalf("portal teardown failed: %v", err)
		}
	}
	provider.mu.Lock()
	closes := provider.closedSessions
	provider.mu.Unlock()
	if closes != 1 {
		t.Fatalf("session Close dispatched %d times, want one", closes)
	}
}
