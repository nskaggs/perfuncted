package perfuncted

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

func TestAccessibilityOwnerInstallAndEventAdmissionFailClosed(t *testing.T) {
	var absent *accessibilityBackendOwner
	if _, err := absent.eventsAcrossReopens(nil, accessibility.EventOptions{}); err == nil {
		t.Fatal("nil context was accepted for a wait-preserving event stream")
	}
	if _, err := absent.eventsAcrossReopens(context.Background(), accessibility.EventOptions{}); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("nil owner event stream error = %v", err)
	}
	if _, err := absent.Events(nil, accessibility.EventOptions{}); err == nil {
		t.Fatal("nil context was accepted for a single-generation event stream")
	}
	if _, err := absent.Events(context.Background(), accessibility.EventOptions{}); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("nil owner event source error = %v", err)
	}

	orphan := newOwnerLifecycleBackend(200, "events")
	absent.install(orphan)
	awaitOwnerSignal(t, orphan.closeDone, "backend rejected by absent owner")
	if orphan.closeCalls.Load() != 1 {
		t.Fatalf("backend rejected by absent owner closed %d times", orphan.closeCalls.Load())
	}
	var typedNil *ownerLifecycleBackend
	newAccessibilityBackendOwner(nil).install(typedNil)

	owner := newAccessibilityBackendOwner(nil)
	first := newOwnerLifecycleBackend(201, "events")
	rejected := newOwnerLifecycleBackend(202, "events")
	owner.install(first)
	owner.install(rejected)
	awaitOwnerSignal(t, rejected.closeDone, "duplicate backend rejected by owner")
	if rejected.closeCalls.Load() != 1 {
		t.Fatalf("duplicate backend closed %d times", rejected.closeCalls.Load())
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	late := newOwnerLifecycleBackend(203, "events")
	owner.install(late)
	awaitOwnerSignal(t, late.closeDone, "backend rejected by closed owner")
	if first.closeCalls.Load() != 1 || late.closeCalls.Load() != 1 {
		t.Fatalf("backend closes after owner lifecycle: installed=%d late=%d", first.closeCalls.Load(), late.closeCalls.Load())
	}
}

func TestAccessibilityReopenRetireFailureLimitsEveryLiveStream(t *testing.T) {
	old := newOwnerLifecycleBackend(210, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	fresh := newOwnerLifecycleBackend(211, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	old.retireErr = errors.New(strings.Repeat("provider retirement failed ", 16))
	fresh.eventEntered = make(chan struct{})
	freshGate := make(chan struct{})
	fresh.eventGate = freshGate
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	var releaseOnce sync.Once
	releaseFreshEvents := func() { releaseOnce.Do(func() { close(freshGate) }) }
	t.Cleanup(func() {
		releaseFreshEvents()
		_ = session.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	owner := accessibilityOwnerForTest(t, session)
	streams := make([]<-chan accessibility.Event, 2)
	for i := range streams {
		stream, err := owner.eventsAcrossReopens(ctx, accessibility.EventOptions{Buffer: 4})
		if err != nil {
			t.Fatalf("register event stream %d: %v", i, err)
		}
		streams[i] = stream
		fixture := awaitOwnerSignal(t, old.eventReady, "initial event stream")
		if fixture.events == nil {
			t.Fatal("initial event source returned no stream")
		}
	}

	reopen := make(chan error, 1)
	go func() { reopen <- session.Accessibility.Reopen(ctx) }()
	awaitOwnerSignal(t, fresh.eventEntered, "fresh event source held during reopen")
	limits := 0
	for _, diagnostic := range session.Capability(CapabilityAccessibility).Diagnostics {
		if strings.HasPrefix(diagnostic, "accessibility events unavailable:") {
			limits++
			if len(diagnostic) > len("accessibility events unavailable: ")+256 {
				t.Fatalf("event failure diagnostic was not bounded: %d bytes", len(diagnostic))
			}
		}
	}
	if limits != len(streams) {
		t.Fatalf("retirement failure limited %d/%d live streams", limits, len(streams))
	}

	releaseFreshEvents()
	if err := awaitOwnerSignal(t, reopen, "reopen after nonfatal event-retirement error"); err != nil {
		t.Fatalf("reopen with recovered event transport: %v", err)
	}
	assertAccessibilityEventLimit(t, session, false)

	for range streams {
		fixture := awaitOwnerSignal(t, fresh.eventReady, "fresh event stream")
		if fixture.events == nil {
			t.Fatalf("fresh event binding failed: %+v", fixture)
		}
		fixture.events <- accessibility.Event{Kind: "recovered"}
	}
	for _, stream := range streams {
		select {
		case event, ok := <-stream:
			if !ok || event.Kind != "recovered" {
				t.Fatalf("rebound event stream delivered %+v, open=%t", event, ok)
			}
		case <-ctx.Done():
			t.Fatalf("rebound event did not reach a subscriber: %v", ctx.Err())
		}
	}
}
