package accessibility

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"testing"
	"time"
)

// Coalescing may be called before its state has been allocated. An absent
// history cannot prove an event is a duplicate and must not be dereferenced.
func TestEventCoalescingWithoutCompleteHistoryNeverDropsEvent(t *testing.T) {
	now := time.Now()
	key := "prior"
	at := now
	event := Event{Kind: "object:state-changed", Timestamp: now, Value: "on"}
	for _, tc := range []struct {
		name string
		key  *string
		at   *time.Time
	}{
		{name: "missing key", at: &at},
		{name: "missing timestamp", key: &key},
		{name: "both missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if coalesceEvent(tc.key, tc.at, event) {
				t.Fatal("event with incomplete history was dropped")
			}
		})
	}
}

// A clock discontinuity or out-of-order delivery is not a duplicate inside
// a positive coalescing window. Suppressing it can discard the only current
// accessibility state transition for a watched control.
func TestEventCoalescingRequiresNonnegativeElapsedTime(t *testing.T) {
	now := time.Now()
	event := Event{Kind: "object:state-changed", Node: NodeID{BusName: "org.example.App", ObjectPath: "/button", Generation: 1}, Property: "focused", Timestamp: now}
	var key string
	var last time.Time
	if coalesceEvent(&key, &last, event) {
		t.Fatal("initial event suppressed")
	}
	earlier := event
	earlier.Timestamp = now.Add(-time.Second)
	if coalesceEvent(&key, &last, earlier) {
		t.Fatal("out-of-order event suppressed")
	}
	if !last.Equal(earlier.Timestamp) {
		t.Fatalf("out-of-order event did not reset reference time: %v", last)
	}
	duplicate := earlier
	duplicate.Timestamp = earlier.Timestamp.Add(eventCoalesceWindow / 2)
	if !coalesceEvent(&key, &last, duplicate) {
		t.Fatal("legitimate close duplicate not coalesced")
	}
	distinct := duplicate
	distinct.Property = "enabled"
	if coalesceEvent(&key, &last, distinct) {
		t.Fatal("distinct state property suppressed")
	}
}

// Two transitions of one property are not duplicates simply because they
// occur inside the coalescing window. For redacted values, the payload has
// deliberately been erased, so equality cannot establish duplication.
func TestATSPICoalescingNeverDropsDistinctOrRedactedStateTransitions(t *testing.T) {
	base := time.Now()
	node := NodeID{BusName: "org.example.App", ObjectPath: "/editor", Generation: 4}
	var key string
	var at time.Time
	first := Event{Kind: "object:property-change", Node: node, Property: "Name", Value: "Save", Timestamp: base}
	if coalesceEvent(&key, &at, first) {
		t.Fatal("first event suppressed")
	}
	duplicate := first
	duplicate.Timestamp = base.Add(time.Millisecond)
	if !coalesceEvent(&key, &at, duplicate) {
		t.Fatal("same-value duplicate not coalesced")
	}
	changed := first
	changed.Value = "Delete"
	changed.Timestamp = base.Add(2 * time.Millisecond)
	if coalesceEvent(&key, &at, changed) {
		t.Fatal("new value discarded as duplicate")
	}
	changed.Timestamp = base.Add(3 * time.Millisecond)
	if !coalesceEvent(&key, &at, changed) {
		t.Fatal("same new value not coalesced")
	}
	protected := first
	protected.Property = "CurrentValue"
	protected.Value = ""
	protected.Timestamp = base.Add(4 * time.Millisecond)
	if coalesceEvent(&key, &at, protected) {
		t.Fatal("first redacted event discarded")
	}
	protected.Timestamp = base.Add(5 * time.Millisecond)
	if coalesceEvent(&key, &at, protected) {
		t.Fatal("second redacted transition discarded with no way to prove duplication")
	}
	// Dropped duplicate timestamps must not prevent a later in-window state
	// change from being reported.
	after := changed
	after.Timestamp = base.Add(6 * time.Millisecond)
	if coalesceEvent(&key, &at, after) {
		t.Fatal("different property after secret transition discarded")
	}
}

func TestSubscribeEventMatchesHonorsCancellationBeforeRegistration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	connection, err := dbusutil.SessionBusAddressContext(context.Background(), startTestDBus(t))
	if err != nil {
		t.Fatalf("connect private accessibility bus: %v", err)
	}
	defer connection.Close()

	matches, err := subscribeEventMatches(ctx, connection)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("subscribeEventMatches error = %v, want context cancellation", err)
	}
	if len(matches) != 0 {
		t.Fatalf("canceled subscription retained %d event matches", len(matches))
	}
}

func TestRegisterEventFamiliesReportsCancellationForEmptyCandidateSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	registered, err := registerEventFamilies(ctx, nil, "", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("registerEventFamilies error = %v, want context cancellation", err)
	}
	if len(registered) != 0 {
		t.Fatalf("canceled empty registration returned event families %v", registered)
	}
}

func TestEventDispatcherDropsSignalAfterBackendDisconnect(t *testing.T) {
	connection, err := dbusutil.SessionBusAddressContext(context.Background(), startTestDBus(t))
	if err != nil {
		t.Fatalf("connect private accessibility bus: %v", err)
	}
	defer connection.Close()

	signals := make(chan *dbus.Signal)
	done := make(chan struct{})
	subscriber := &eventSubscriber{out: make(chan Event, 1), done: make(chan struct{})}
	backend := &dbusBackend{
		disconnected: true,
		subscribers:  map[uint64]*eventSubscriber{1: subscriber},
		eventCancel:  func() {},
		eventDone:    done,
	}
	dispatchDone := make(chan struct{})
	go func() {
		backend.runEventDispatcher(context.Background(), connection, signals, nil, nil, nil, done)
		close(dispatchDone)
	}()
	signals <- &dbus.Signal{
		Name:   "org.a11y.atspi.Event.Object.PropertyChange",
		Sender: "org.synthetic.Editor",
		Path:   "/org/a11y/atspi/accessible/editor",
		Body:   []any{"Name", "stale"},
	}
	close(signals)
	select {
	case <-dispatchDone:
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not retire the closed signal stream")
	}
	if _, ok := <-subscriber.out; ok {
		t.Fatal("disconnected dispatcher published a stale event")
	}
}

// A provider event that cannot identify its source must not authorize a
// snapshot captured before that event, even if its visible payload appears
// benign. Fail closed before the result can enter the reusable cache.
func TestUnknownEventSourcePreventsStaleSnapshotPublication(t *testing.T) {
	root := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/window", Generation: 7}
	snapshot := Snapshot{Root: Node{ID: root, Role: "window"}, Nodes: []Node{{ID: root, Role: "window"}}, Generation: 7, CapturedAt: time.Now()}
	for _, tc := range []struct {
		name   string
		signal *dbus.Signal
		event  Event
	}{
		{"missing signal", nil, Event{Kind: "object:property-change", Node: root}},
		{"missing sender", &dbus.Signal{Name: "org.a11y.atspi.Event.Object:PropertyChange", Path: "/window"}, Event{Kind: "object:property-change"}},
		{"malformed cache addition", &dbus.Signal{Name: "org.a11y.atspi.Cache:AddAccessible", Sender: "org.synthetic.Editor", Body: []any{"invalid cache item"}}, Event{Kind: "cache:add"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: 7, observationRevision: 1}
			b.recordObservationLocked(tc.signal, tc.event, 2)
			b.observationRevision = 2
			if len(b.observationHistory) != 1 || !b.observationHistory[0].unknown || b.observationHistory[0].revision != 2 {
				t.Fatalf("unidentified provider event was not recorded as an unknown observation: %+v", b.observationHistory)
			}
			if err := b.observationChangesError(1, root, snapshot, true); !errors.Is(err, ErrObservationChanged) {
				t.Fatalf("unidentified event did not invalidate in-flight observation: %v", err)
			}
			if err := b.publishSnapshot("stale", 7, 1, root, true, snapshot); !errors.Is(err, ErrObservationChanged) || len(b.cache) != 0 {
				t.Fatalf("stale snapshot became reusable: %v cache=%+v", err, b.cache)
			}
		})
	}
}

// Providers use both bare and qualified property names; protecting only the
// exact word "Value" allows alternate current-value properties to disclose
// editor contents in diagnostic and event streams.
func TestATSPIEventValueAliasesNeverPublishSensitivePayload(t *testing.T) {
	for _, property := range []string{"Value", " CurrentValue ", "Accessible-Value", "accessible:value", "Value:Changed", "Text", "PasswordHint", "ProtectedText"} {
		t.Run(property, func(t *testing.T) {
			sig := &dbus.Signal{Sender: "org.example.App", Path: "/editor", Name: "org.a11y.atspi.Event.Object:PropertyChange", Body: []any{property, "private-entry-secret"}}
			if ev := signalEvent(sig); ev.Value != "" {
				t.Fatalf("sensitive %q event exposed %q", property, ev.Value)
			}
		})
	}
	for _, property := range []string{"Name", "Description", "Enabled"} {
		sig := &dbus.Signal{Sender: "org.example.App", Path: "/editor", Name: "org.a11y.atspi.Event.Object:PropertyChange", Body: []any{property, "public-control-label"}}
		if ev := signalEvent(sig); ev.Value != "public-control-label" {
			t.Fatalf("public %q event value unexpectedly redacted", property)
		}
	}
}
