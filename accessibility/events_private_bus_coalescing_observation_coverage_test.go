package accessibility

import (
	"context"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// Duplicate notification delivery may be coalesced, but each individual
// physical signal must still invalidate cached observations. The next
// distinct event must report the dropped notification to every subscriber.
func TestATSPIPrivateBusDuplicateEventsInvalidateEveryObservationAndAccountForDrop(t *testing.T) {
	service, backend, _ := startSyntheticEventBus(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t.Cleanup(func() { _ = backend.Close() })
	stream, err := backend.Events(ctx, EventOptions{Buffer: 4})
	if err != nil {
		t.Fatal(err)
	}
	path := dbus.ObjectPath("/org/a11y/atspi/accessible/editor")
	first := func(property, value string) {
		t.Helper()
		if err := service.Emit(path, "org.a11y.atspi.Event.Object.PropertyChange", property, value); err != nil {
			t.Fatal(err)
		}
	}
	first("Name", "Editor")
	expectPrivateBusEvent(t, stream, "Editor", 0)
	backend.mu.RLock()
	base := backend.observationRevision
	backend.mu.RUnlock()
	first("Name", "Editor")
	waitForPrivateBusObservationRevision(t, backend, base+1)
	first("Name", "Editor two")
	expectPrivateBusEvent(t, stream, "Editor two", 1)
	backend.mu.RLock()
	revisions := backend.observationRevision
	history := len(backend.observationHistory)
	backend.mu.RUnlock()
	if revisions < base+2 || history < 3 {
		t.Fatalf("coalescing suppressed authoritative transitions: revisions=%d base=%d history=%d", revisions, base, history)
	}
}

func expectPrivateBusEvent(t *testing.T, stream <-chan Event, value string, dropped uint64) {
	t.Helper()
	select {
	case ev, ok := <-stream:
		if !ok || ev.Property != "Name" || ev.Value != value || ev.Dropped != dropped {
			t.Fatalf("incorrect event value=%q dropped=%d: %+v open=%v", value, dropped, ev, ok)
		}
	case <-time.After(time.Second):
		t.Fatalf("event value=%q not delivered", value)
	}
}

func waitForPrivateBusObservationRevision(t *testing.T, b *dbusBackend, want uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.RLock()
		got := b.observationRevision
		b.mu.RUnlock()
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("observation revision not advanced: want=%d got=%d", want, got)
		}
		time.Sleep(time.Millisecond)
	}
}
