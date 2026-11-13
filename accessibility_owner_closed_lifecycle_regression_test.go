package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// Closing the owner revokes both direct and generation-pinned admission. A
// subsequent invalidation or reopen must not touch the retired backend.
func TestAccessibilityOwnerClosedGenerationRefusesNewWorkAndInvalidation(t *testing.T) {
	backend := newOwnerLifecycleBackend(41, "applications")
	owner := newAccessibilityBackendOwner(nil)
	owner.install(backend)
	lease, err := owner.acquire()
	if err != nil {
		t.Fatal(err)
	}
	gen := lease.gen
	lease.release()
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.acquire(); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("closed owner admitted a new lease: %v", err)
	}
	if _, err := owner.acquireGeneration(gen); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("closed owner admitted a retired generation: %v", err)
	}
	owner.Invalidate(accessibility.NodeID{BusName: "org.example.Forged"})
	if got := backend.generation.Load(); got != 41 {
		t.Fatalf("closed owner forwarded invalidation: generation=%d", got)
	}
	if err := owner.reopen(context.Background()); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("closed owner reopened a retired generation: %v", err)
	}
	if backend.closeCalls.Load() != 1 {
		t.Fatalf("backend closed %d times, want once", backend.closeCalls.Load())
	}
	if err := owner.Close(); err != nil || backend.closeCalls.Load() != 1 {
		t.Fatalf("repeated close changed teardown: err=%v closes=%d", err, backend.closeCalls.Load())
	}
}
