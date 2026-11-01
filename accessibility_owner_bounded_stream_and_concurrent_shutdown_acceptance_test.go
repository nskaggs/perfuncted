package perfuncted

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

type blockedOwnerCloseBackend struct {
	*ownerLifecycleBackend
	entered      chan struct{}
	release      chan struct{}
	closeFailure error
}

func (b *blockedOwnerCloseBackend) Close() error {
	close(b.entered)
	<-b.release
	_ = b.ownerLifecycleBackend.Close()
	return b.closeFailure
}

// Concurrent shutdown callers must share the same terminal result, wait for
// all held generation leases, and never call the provider's Close twice.
func TestAccessibilityOwnerConcurrentCloseWaitsAndPropagatesSingleProviderFailure(t *testing.T) {
	failure := errors.New("synthetic provider close refusal")
	backend := &blockedOwnerCloseBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(4, "events"), entered: make(chan struct{}), release: make(chan struct{}), closeFailure: failure}
	owner := newAccessibilityBackendOwner(nil)
	owner.install(backend)
	first := make(chan error, 1)
	go func() { first <- owner.Close() }()
	select {
	case <-backend.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("provider teardown did not start")
	}
	if owner.hasBackend() {
		t.Fatal("closing owner still offered backend to new callers")
	}
	if _, err := owner.acquire(); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("closing owner allowed new lease: %v", err)
	}
	second := make(chan error, 1)
	go func() { second <- owner.Close() }()
	close(backend.release)
	for _, finished := range []<-chan error{first, second} {
		select {
		case err := <-finished:
			if !errors.Is(err, failure) {
				t.Fatalf("shared close did not propagate provider failure: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent owner close stranded waiting caller")
		}
	}
	if backend.closeCalls.Load() != 1 {
		t.Fatalf("provider close count=%d, want one", backend.closeCalls.Load())
	}
}

// Attackers can request millions of event buffer slots. Persistent streams
// must enforce the bounded capacity, and cancellation must retire the stream
// without requiring any event traffic from a provider.
func TestAccessibilityPersistentEventStreamBufferLimitAndCancellationCleanup(t *testing.T) {
	for _, tc := range []struct {
		name       string
		size, want int
	}{
		{"default", 0, 64},
		{"oversized", 1 << 20, 4096},
		{"minimum", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := newOwnerLifecycleBackend(10, "events")
			owner := newAccessibilityBackendOwner(nil)
			owner.install(backend)
			defer owner.Close()
			ctx, cancel := context.WithCancel(context.Background())
			stream, err := owner.eventsAcrossReopens(ctx, accessibility.EventOptions{Buffer: tc.size})
			if err != nil || stream == nil || cap(stream) != tc.want {
				cancel()
				t.Fatalf("buffer cap=%d want=%d err=%v", cap(stream), tc.want, err)
			}
			cancel()
			select {
			case _, ok := <-stream:
				if ok {
					t.Fatal("canceled stream still forwarded events")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("canceled persistent stream never closed")
			}
			owner.mu.Lock()
			active := len(owner.eventSubs)
			owner.mu.Unlock()
			if active != 0 {
				t.Fatalf("canceled stream retained %d subscriptions", active)
			}
		})
	}
}

func TestAccessibilityOwnerEventLimitBoundsProviderDiagnosticsAndClearsAfterRecovery(t *testing.T) {
	owner := newAccessibilityBackendOwner(nil)
	sub := &accessibilityEventSubscription{owner: owner, out: make(chan accessibility.Event), done: make(chan struct{})}
	owner.eventSubs[sub] = struct{}{}
	owner.setEventLimit(sub, errors.New(strings.Repeat("private-error-", 100)))
	owner.mu.Lock()
	message := owner.eventLimits[sub]
	owner.mu.Unlock()
	if !strings.HasPrefix(message, "accessibility events unavailable: ") || len(message) > len("accessibility events unavailable: ")+256 {
		t.Fatalf("provider error diagnostics were not bounded: len=%d", len(message))
	}
	owner.setEventLimit(sub, nil)
	owner.mu.Lock()
	_, stillLimited := owner.eventLimits[sub]
	owner.mu.Unlock()
	if stillLimited {
		t.Fatal("recovered provider retained stale error diagnostics")
	}
}
