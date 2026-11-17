package perfuncted

import (
	"sync"
	"testing"
)

type gatedDiagnosticsBackend struct {
	*ownerLifecycleBackend
	entered chan struct{}
	release <-chan struct{}
}

func (b *gatedDiagnosticsBackend) Diagnostics() []string {
	close(b.entered)
	<-b.release
	return []string{"fresh diagnostics"}
}

func TestAccessibilityOwnerDoesNotPublishRefreshAfterClose(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	owner := accessibilityOwnerForTest(t, session)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	backend := &gatedDiagnosticsBackend{
		ownerLifecycleBackend: newOwnerLifecycleBackend(1, "applications"),
		entered:               make(chan struct{}),
		release:               release,
	}
	owner.install(backend)
	t.Cleanup(func() {
		unblock()
		_ = session.Close()
	})

	session.capabilitiesMu.Lock()
	session.capabilities[CapabilityAccessibility] = CapabilityStatus{
		Capability: CapabilityAccessibility,
		Available:  false,
		Backend:    "published-before-close",
	}
	session.capabilitiesMu.Unlock()

	owner.mu.Lock()
	gen := owner.current
	owner.mu.Unlock()
	refreshDone := make(chan struct{})
	go func() {
		owner.refreshCapabilities()
		close(refreshDone)
	}()
	awaitOwnerSignal(t, backend.entered, "capability diagnostics")

	closeDone := make(chan error, 1)
	go func() { closeDone <- owner.Close() }()
	awaitOwnerSignal(t, gen.retiredCh, "backend generation retirement")
	unblock()
	awaitOwnerSignal(t, refreshDone, "capability refresh completion")
	if err := awaitOwnerSignal(t, closeDone, "accessibility owner close"); err != nil {
		t.Fatalf("owner.Close: %v", err)
	}
	if !backend.closed.Load() || backend.closeCalls.Load() != 1 {
		t.Fatalf("backend close state = (%v, %d calls), want (closed, 1 call)", backend.closed.Load(), backend.closeCalls.Load())
	}

	status := session.Capability(CapabilityAccessibility)
	if status.Available || status.Backend != "published-before-close" || len(status.Diagnostics) != 0 {
		t.Fatalf("capability after close = %+v, want pre-close status unchanged", status)
	}
}
