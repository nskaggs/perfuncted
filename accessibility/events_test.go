package accessibility

import (
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"reflect"
	"sync"
	"sync/atomic"
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

type ambiguousRegistrationRegistry struct {
	mu                  sync.Mutex
	first               sync.Once
	entered             chan struct{}
	release             chan struct{}
	firstFinished       chan struct{}
	deregisterAttempted chan struct{}
	registered, removed []string
}

func (r *ambiguousRegistrationRegistry) RegisterEvent(name string, _ []string, _ string) *dbus.Error {
	first := false
	r.first.Do(func() {
		first = true
		close(r.entered)
	})

	r.mu.Lock()
	defer r.mu.Unlock()
	if first {
		defer close(r.firstFinished)
		<-r.release
	}
	r.registered = append(r.registered, name)
	return nil
}

func (r *ambiguousRegistrationRegistry) DeregisterEvent(name string) *dbus.Error {
	select {
	case r.deregisterAttempted <- struct{}{}:
	default:
	}
	r.mu.Lock()
	r.removed = append(r.removed, name)
	r.mu.Unlock()
	return nil
}

func TestCanceledEventRegistrationCleansAmbiguousProviderSuccess(t *testing.T) {
	address := startTestDBus(t)
	service, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(registryName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own test AT-SPI registry name: reply %d, err %v", reply, err)
	}
	registry := &ambiguousRegistrationRegistry{
		entered:             make(chan struct{}),
		release:             make(chan struct{}),
		firstFinished:       make(chan struct{}),
		deregisterAttempted: make(chan struct{}, 1),
	}
	if err := service.Export(registry, registryPath, registryName); err != nil {
		t.Fatal(err)
	}
	client, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	backend := &dbusBackend{generation: 1, access: client}
	t.Cleanup(func() { _ = backend.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		stream, eventErr := backend.Events(ctx, EventOptions{})
		if stream != nil {
			eventErr = errors.New("canceled registration returned a live event stream")
		}
		result <- eventErr
	}()

	select {
	case <-registry.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("registry did not receive RegisterEvent")
	}
	cancel()

	var setupErr error
	select {
	case <-registry.deregisterAttempted:
		close(registry.release)
	case setupErr = <-result:
		close(registry.release)
	case <-time.After(2 * time.Second):
		close(registry.release)
		t.Fatal("canceled event setup did not finish or begin cleanup")
	}
	if setupErr == nil {
		select {
		case setupErr = <-result:
		case <-time.After(2 * time.Second):
			t.Fatal("event setup did not finish after provider registration was released")
		}
	}
	select {
	case <-registry.firstFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("provider registration did not finish")
	}
	if !errors.Is(setupErr, context.Canceled) {
		t.Errorf("canceled event setup error = %v, want context.Canceled", setupErr)
	}
	registry.mu.Lock()
	registered := append([]string(nil), registry.registered...)
	removed := append([]string(nil), registry.removed...)
	registry.mu.Unlock()
	if len(registered) != 1 || registered[0] != "object:property-change" {
		t.Errorf("provider registrations = %v, want only the in-flight family", registered)
	}
	if len(removed) != len(registered) || (len(removed) == 1 && removed[0] != registered[0]) {
		t.Errorf("provider deregistrations = %v for registrations %v", removed, registered)
	}
}

// Simulates the remote registry committing a RegisterEvent request at the
// same moment its caller cancels. A successful reply must not authorize
// further registrations or make the whole setup appear successful.
type cancelOnSuccessfulEventRegistry struct {
	cancel  context.CancelFunc
	invoked []string
}

func (r *cancelOnSuccessfulEventRegistry) CallWithContext(_ context.Context, _ string, _ dbus.Flags, args ...any) *dbus.Call {
	family, _ := args[0].(string)
	r.invoked = append(r.invoked, family)
	if len(r.invoked) == 1 {
		r.cancel()
	}
	return &dbus.Call{}
}
func TestEventRegistrationRejectsLateSuccessfulReplyAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry := &cancelOnSuccessfulEventRegistry{cancel: cancel}
	candidates := []string{"object:property-change", "window:create", "focus:focus"}
	registered, err := registerEventFamilies(ctx, registry, ":1.synthetic", candidates)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("late canceled provider acknowledgement reported setup success: %v", err)
	}
	if !reflect.DeepEqual(registry.invoked, candidates[:1]) {
		t.Fatalf("canceled setup registered additional provider families: %v", registry.invoked)
	}
	if !reflect.DeepEqual(registered, candidates[:1]) {
		t.Fatalf("ambiguous successful registration not tracked for teardown: %v", registered)
	}
}

func TestATSPIMalformedLifecycleSignalsInvalidateHandlesWithoutAffectingLiveDesktop(t *testing.T) {
	for _, tc := range []struct {
		name, signal string
		body         []any
	}{
		{"cache removal malformed", cacheIface + ":RemoveAccessible", []any{struct{}{}}},
		{"cache removal spoofed bus", cacheIface + ":RemoveAccessible", []any{[]any{"org.spoofed.App", dbus.ObjectPath("/node")}}},
		{"children removal missing ref", "org.a11y.atspi.Event.Object:ChildrenChanged", []any{"remove", int32(0), int32(0), "invalid", map[string]dbus.Variant{}}},
		{"children removal wrong bus", "org.a11y.atspi.Event.Object:ChildrenChanged", []any{"remove", int32(0), int32(0), []any{"org.spoofed.App", dbus.ObjectPath("/node")}, map[string]dbus.Variant{}}},
		{"defunct unknown state", "org.a11y.atspi.Event.Object:StateChanged", []any{"defunct", "untrusted", int32(0), dbus.MakeVariant(""), map[string]dbus.Variant{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: 7, cacheItems: map[NodeID]cacheItem{{BusName: "org.example.App", ObjectPath: "/node", Generation: 7, Incarnation: 1}: {Name: "live"}}, cacheApps: map[string]bool{"org.example.App": true}}
			sig := &dbus.Signal{Name: tc.signal, Sender: "org.example.App", Path: dbus.ObjectPath("/node"), Body: tc.body}
			b.transitionObjectHandlesLocked(sig)
			if b.generation != 8 || b.cacheItems != nil || b.cacheApps != nil {
				t.Fatalf("malformed signal %s published stale nodes: gen=%d items=%v apps=%v", tc.name, b.generation, b.cacheItems, b.cacheApps)
			}
		})
	}
}
func TestATSPINonRemovalSignalsCannotRevokeOtherNodes(t *testing.T) {
	for _, tc := range []struct {
		name, signal string
		body         []any
	}{
		{"unrelated signal", "org.a11y.atspi.Event.Object:PropertyChange", []any{"Name"}},
		{"children added", "org.a11y.atspi.Event.Object:ChildrenChanged", []any{"add", int32(1), int32(0), "not-a-ref", map[string]dbus.Variant{}}},
		{"defunct false", "org.a11y.atspi.Event.Object:StateChanged", []any{"defunct", int32(0), int32(0), dbus.MakeVariant(""), map[string]dbus.Variant{}}},
		{"other state", "org.a11y.atspi.Event.Object:StateChanged", []any{"enabled", int32(1), int32(0), dbus.MakeVariant(""), map[string]dbus.Variant{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: 7, cacheItems: map[NodeID]cacheItem{{BusName: "org.example.App", ObjectPath: "/node", Generation: 7, Incarnation: 1}: {Name: "live"}}}
			b.transitionObjectHandlesLocked(&dbus.Signal{Name: tc.signal, Sender: "org.example.App", Path: dbus.ObjectPath("/node"), Body: tc.body})
			if b.generation != 7 || len(b.cacheItems) != 1 {
				t.Fatalf("non-removal event %s revoked valid node", tc.name)
			}
		})
	}
}
func TestATSPIDefunctStateWireVariants(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state any
		want  bool
		valid bool
	}{
		{"signed true", int32(1), true, true}, {"unsigned true", uint32(1), true, true},
		{"boolean true", true, true, true}, {"signed false", int32(0), false, true},
		{"unsigned false", uint32(0), false, true}, {"boolean false", false, false, true},
		{"string refused", "1", false, false}, {"nil refused", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled, known := stateEnabled([]any{"defunct", tc.state, int32(0), dbus.MakeVariant(""), map[string]dbus.Variant{}})
			if enabled != tc.want || known != tc.valid {
				t.Fatalf("defunct-state %T decoded %v/%v, want %v/%v", tc.state, enabled, known, tc.want, tc.valid)
			}
		})
	}
	for _, bad := range [][]any{nil, {}, {"defunct", int32(1)}, {"defunct", int32(1), "wrong", dbus.MakeVariant(""), map[string]dbus.Variant{}}, {"defunct", int32(1), int32(0), "wrong", map[string]dbus.Variant{}}, {"defunct", int32(1), int32(0), dbus.MakeVariant(""), "wrong"}} {
		if enabled, known := stateEnabled(bad); enabled || known {
			t.Fatalf("malformed state %v accepted", bad)
		}
	}
}
func TestATSPIObservationHistoryBoundedEvictionPreservesChangeOrder(t *testing.T) {
	b := &dbusBackend{generation: 3}
	for i := uint64(1); i <= observationHistoryLimit+5; i++ {
		sig := &dbus.Signal{Name: "org.a11y.atspi.Event.Object:PropertyChange", Sender: "org.example.App", Path: dbus.ObjectPath("/node")}
		event := Event{Kind: "object:property-change", Node: NodeID{BusName: "org.example.App", ObjectPath: "/node", Generation: 3}}
		b.recordObservationLocked(sig, event, i)
	}
	if len(b.observationHistory) != observationHistoryLimit || b.observationFloor != 5 {
		t.Fatalf("history overflow retained=%d floor=%d, want %d floor 5", len(b.observationHistory), b.observationFloor, observationHistoryLimit)
	}
	if first, last := b.observationHistory[0].revision, b.observationHistory[len(b.observationHistory)-1].revision; first != 6 || last != observationHistoryLimit+5 {
		t.Fatalf("history tail out of order: first=%d last=%d", first, last)
	}
	if !b.observationHistory[0].node.valid() {
		t.Fatal("event lost authoritative node identity")
	}
}
func TestATSPIEventRefDecoderRejectsSpoofedOrIncorrectlyTypedReferences(t *testing.T) {
	for _, tc := range []struct {
		name  string
		v     any
		valid bool
	}{
		{"object ref", objectRef{BusName: "org.example.App", ObjectPath: "/node"}, true},
		{"cache ref", cacheObjectRef{BusName: "org.example.App", ObjectPath: "/node"}, true},
		{"variant wrapped", dbus.MakeVariant([]any{"org.example.App", dbus.ObjectPath("/node")}), true},
		{"array path string", []any{"org.example.App", "/node"}, true},
		{"malformed array", []any{"org.example.App"}, false},
		{"wrong bus type", []any{42, dbus.ObjectPath("/node")}, false},
		{"wrong path type", []any{"org.example.App", 123}, false},
		{"untrusted whitespace bus", []any{"org. example.App", dbus.ObjectPath("/node")}, false},
		{"invalid path", []any{"org.example.App", "not-an-absolute-path"}, false},
		{"unexpected type", struct{}{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, ok := cacheObjectRefFromValue(tc.v)
			if ok != tc.valid {
				t.Fatalf("reference %T accepted=%v want %v ref=%+v", tc.v, ok, tc.valid, ref)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		value any
		valid bool
	}{
		{"null parent", []any{"", nullObjectPath}, true},
		{"foreign parent", []any{"org.foreign.App", dbus.ObjectPath("/parent")}, true},
		{"invalid null", []any{"", dbus.ObjectPath("/non-null")}, false},
	} {
		t.Run(fmt.Sprintf("parent-%s", tc.name), func(t *testing.T) {
			ref, ok := cacheParentRefFromValue(tc.value)
			if ok != tc.valid {
				t.Fatalf("parent decode=%+v %v want %v", ref, ok, tc.valid)
			}
		})
	}
}

func TestEventEntryPointsRejectMissingLifetimeAndConnection(t *testing.T) {
	var backend *dbusBackend
	if _, err := backend.Events(nil, EventOptions{}); err == nil {
		t.Fatal("nil event context accepted")
	}
	if _, err := backend.Events(context.Background(), EventOptions{}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil backend event subscription error = %v, want disconnected", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&dbusBackend{}).Events(canceled, EventOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled subscription error = %v, want canceled", err)
	}
	if err := (&dbusBackend{}).startEvents(nil, false); err == nil {
		t.Fatal("nil dispatcher setup context accepted")
	}
	if err := (&dbusBackend{}).startEvents(context.Background(), false); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("disconnected dispatcher setup error = %v, want disconnected", err)
	}
	if backend.eventConnection() != nil || backend.connectedAccess(nil) {
		t.Fatal("nil backend exposed a D-Bus event connection")
	}
}

func TestEventAdmissionRequiresPublishedCompletionSignals(t *testing.T) {
	for _, backend := range []*dbusBackend{
		{eventCancel: func() {}},
		{eventStarting: &eventStart{}},
	} {
		if _, _, _, err := backend.reserveEventSubscriber(make(chan Event, 1)); err == nil {
			t.Fatal("event run without its completion signal admitted a subscriber")
		}
	}
}

type gatedErrorContext struct {
	context.Context
	checked chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (c *gatedErrorContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() {
		close(c.checked)
		<-c.release
	})
	return err
}

func TestEventStartWaitObservesCancellationAfterInitialCheck(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	ctx := &gatedErrorContext{Context: base, checked: make(chan struct{}), release: release}
	state := &eventStart{done: make(chan struct{})}
	type result struct {
		retry bool
		err   error
	}
	completed := make(chan result, 1)
	go func() {
		retry, err := waitForEventStart(ctx, state, nil, 0)
		completed <- result{retry: retry, err: err}
	}()
	select {
	case <-ctx.checked:
	case <-time.After(time.Second):
		t.Fatal("event setup wait did not perform its cancellation precheck")
	}
	cancel()
	close(release)
	select {
	case result := <-completed:
		if result.retry || !errors.Is(result.err, context.Canceled) {
			t.Fatalf("event setup wait returned retry=%v error=%v, want canceled", result.retry, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("event setup wait ignored cancellation after its precheck")
	}
}

func TestEventSubscriberWatchOnlyOwnsCancelableContextsAndCurrentRun(t *testing.T) {
	for _, ctx := range []context.Context{nil, context.Background()} {
		backend := &dbusBackend{subscribers: map[uint64]*eventSubscriber{1: {out: make(chan Event, 1)}}}
		backend.watchSubscriber(ctx, 1)
		if len(backend.subscribers) != 1 {
			t.Fatal("watcher removed a subscriber without a cancelable lifetime")
		}
	}

	for _, tc := range []struct {
		name  string
		setup func(*dbusBackend, chan struct{})
	}{
		{name: "dispatcher", setup: func(b *dbusBackend, stopped chan struct{}) { b.eventCancel = func() { close(stopped) } }},
		{name: "setup", setup: func(b *dbusBackend, stopped chan struct{}) { b.eventStartStop = func() { close(stopped) } }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := make(chan Event, 1)
			stopped := make(chan struct{})
			backend := &dbusBackend{subscribers: map[uint64]*eventSubscriber{7: {out: out}}}
			tc.setup(backend, stopped)
			ctx, cancel := context.WithCancel(context.Background())
			watcherDone := make(chan struct{})
			go func() {
				backend.watchSubscriber(ctx, 7)
				close(watcherDone)
			}()
			cancel()
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("last subscriber cancellation did not stop its event run")
			}
			select {
			case <-watcherDone:
			case <-time.After(time.Second):
				t.Fatal("subscriber watcher did not finish after cancellation")
			}
			if len(backend.subscribers) != 0 {
				t.Fatal("canceled subscriber remained registered")
			}
			if _, open := <-out; open {
				t.Fatal("canceled subscriber stream remained open")
			}
		})
	}
}

func TestCacheWireDecoderRejectsMalformedPayloadShapes(t *testing.T) {
	valid := []any{
		[]any{"org.example.App", dbus.ObjectPath("/node")},
		[]any{"org.example.App", dbus.ObjectPath("/app")},
		[]any{"org.example.App", dbus.ObjectPath("/parent")},
		int32(0), int32(1), []string{"org.a11y.atspi.Accessible"}, "node", uint32(43), "", []uint32{},
	}
	nilItem := (*cacheItem)(nil)
	validItem := cacheItem{Object: cacheObjectRef{BusName: "org.example.App", ObjectPath: "/node"}, Application: cacheObjectRef{BusName: "org.example.App", ObjectPath: "/app"}, Parent: cacheObjectRef{BusName: "org.example.App", ObjectPath: "/parent"}}
	inconsistentItem := validItem
	inconsistentItem.Application.BusName = "org.other.App"
	malformedTyped := append([]any(nil), valid...)
	malformedTyped[3] = "not-an-index"
	malformedReference := append([]any(nil), valid...)
	malformedReference[0] = []any{"org.example.App", "relative/path"}
	for _, tc := range []struct {
		name string
		body []any
	}{
		{name: "missing body"},
		{name: "nil cache item pointer", body: []any{nilItem}},
		{name: "short wire tuple", body: []any{[]any{"org.example.App"}}},
		{name: "malformed wire field", body: []any{malformedTyped}},
		{name: "malformed object reference", body: []any{malformedReference}},
		{name: "inconsistent value object", body: []any{inconsistentItem}},
		{name: "unsupported payload", body: []any{struct{}{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if item, ok := cacheItemFromSignal(tc.body); ok {
				t.Fatalf("malformed cache payload accepted as %+v", item)
			}
		})
	}
	if item, ok := cacheItemFromSignal([]any{&validItem}); !ok || item.Object.ObjectPath != "/node" {
		t.Fatalf("valid pointer cache payload decoded as %+v, %v", item, ok)
	}
}

func TestObservationRemovalDecodersRejectMalformedWireFields(t *testing.T) {
	validChild := []any{"org.example.App", dbus.ObjectPath("/child")}
	for _, body := range [][]any{
		nil,
		{"remove"},
		{"remove", "wrong", int32(0), validChild, map[string]dbus.Variant{}},
		{"remove", int32(0), "wrong", validChild, map[string]dbus.Variant{}},
		{"remove", int32(0), int32(0), validChild, "wrong"},
		{"remove", int32(0), int32(0), "wrong", map[string]dbus.Variant{}},
	} {
		if _, ok := childrenChangedObject(body); ok {
			t.Fatalf("malformed child-removal payload accepted: %#v", body)
		}
	}
}

func TestObservationHistoryMarksMalformedAndForeignRemovalSignalsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		sig  *dbus.Signal
	}{
		{name: "nil signal"},
		{name: "malformed cache addition", sig: &dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: "org.example.App", Body: []any{"wrong"}}},
		{name: "foreign cache removal", sig: &dbus.Signal{Name: cacheIface + ":RemoveAccessible", Sender: "org.example.App", Body: []any{[]any{"org.spoofed.App", dbus.ObjectPath("/node")}}}},
		{name: "malformed child removal", sig: &dbus.Signal{Name: "org.a11y.atspi.Event.Object:ChildrenChanged", Sender: "org.example.App", Body: []any{"remove"}}},
		{name: "invalid default node", sig: &dbus.Signal{Name: "org.a11y.atspi.Event.Object:Focus", Sender: "org.example.App"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &dbusBackend{}
			backend.recordObservationLocked(tc.sig, Event{}, 1)
			if len(backend.observationHistory) != 1 || !backend.observationHistory[0].unknown {
				t.Fatalf("malformed observation was trusted: %+v", backend.observationHistory)
			}
		})
	}
}

// Connection close must invalidate every handle epoch, independent of the
// subscriber completion ordering or event dispatcher goroutine scheduling.
func TestSyntheticATSPIClosedConnectionAlwaysInvalidatesHandleGeneration(t *testing.T) {
	for iteration := 0; iteration < 24; iteration++ {
		t.Run("connection-close", func(t *testing.T) {
			_, backend, _ := startSyntheticEventBus(t, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			stream, err := backend.Events(ctx, EventOptions{})
			if err != nil {
				t.Fatal(err)
			}
			connection := backend.eventConnection()
			if connection == nil {
				t.Fatal("no accessibility bus connection")
			}
			if err := connection.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case _, open := <-stream:
				if open {
					t.Fatal("closed bus emitted synthetic event")
				}
			case <-time.After(time.Second):
				t.Fatal("stream did not terminate")
			}
			deadline := time.Now().Add(250 * time.Millisecond)
			for backend.Generation() == 1 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if generation := backend.Generation(); generation != 2 {
				backend.eventsMu.Lock()
				active := backend.eventDone != nil
				subscribers := len(backend.subscribers)
				retired := backend.eventsRetired
				backend.eventsMu.Unlock()
				backend.mu.RLock()
				disconnected := backend.disconnected
				backend.mu.RUnlock()
				t.Fatalf("closed connection retained generation=%d, connected=%v ctx=%v active=%v subscribers=%d retired=%v disconnected=%v", generation, connection.Connected(), connection.Context().Err(), active, subscribers, retired, disconnected)
			}
		})
	}
}

// Cancellation arriving after the first lifetime check but just as a shared
// setup finishes must never allow a stale dispatcher to be used by a caller.
type cancellationAtCompletedEventSetup struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancellationAtCompletedEventSetup) Err() error {
	c.checks++
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestEventWaitCompletionCannotOutraceCallerCancellation(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	lifetime := &cancellationAtCompletedEventSetup{Context: base, cancel: cancel}
	done := make(chan struct{})
	close(done)
	retry, err := waitForEventStart(lifetime, &eventStart{done: done}, nil, 0)
	if retry || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller accepted a completed dispatcher: retry=%v err=%v", retry, err)
	}
	if lifetime.checks != 2 {
		t.Fatalf("event setup checked caller lifetime %d times, want 2", lifetime.checks)
	}
}

// A disconnected backend without an event connection must not spawn a watcher
// or advance generation state merely because its lifecycle is inspected.
func TestEventDisconnectWatcherRequiresConnectedBackend(t *testing.T) {
	var missing *dbusBackend
	missing.watchDisconnect()
	missing.markDisconnected()
	empty := &dbusBackend{generation: 12, observationRevision: 7}
	empty.watchDisconnect()
	if empty.generation != 12 || empty.observationRevision != 7 || empty.disconnected {
		t.Fatalf("watching absent transport mutated backend state: %+v", empty)
	}
	empty.markDisconnected()
	if !empty.disconnected || empty.generation != 13 || empty.observationRevision != 8 {
		t.Fatalf("disconnect did not advance generation exactly once: generation=%d observation=%d disconnected=%v", empty.generation, empty.observationRevision, empty.disconnected)
	}
	empty.markDisconnected()
	if empty.generation != 13 || empty.observationRevision != 8 {
		t.Fatal("repeated disconnect advanced retired generation")
	}
}

type gateSecondEventContextCheck struct {
	context.Context
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (c *gateSecondEventContextCheck) Err() error {
	if c.calls.Add(1) == 2 {
		close(c.entered)
		<-c.release
	}
	return c.Context.Err()
}

// A private bus can disappear between the successful connectivity probe and
// dispatcher admission. The second check must reject that old connection
// without registering a dispatcher or publishing its startup state.
func TestEventStartRejectsConnectionLostBetweenConnectivityAndAdmission(t *testing.T) {
	backend := &dbusBackend{access: &dbus.Conn{}}
	ctx := &gateSecondEventContextCheck{
		Context: context.Background(),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() { result <- backend.startEvents(ctx, false) }()
	select {
	case <-ctx.entered:
	case <-time.After(3 * time.Second):
		close(ctx.release)
		t.Fatal("event setup did not reach second connection admission check")
	}
	backend.mu.Lock()
	backend.access = nil
	backend.mu.Unlock()
	close(ctx.release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrDisconnected) {
			t.Fatalf("lost bus returned %v, want disconnected", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("event setup hung after private bus vanished")
	}
	backend.eventsMu.Lock()
	defer backend.eventsMu.Unlock()
	if backend.eventStarting != nil || backend.eventAccess != nil || backend.eventCancel != nil || backend.eventStartStop != nil {
		t.Fatalf("vanished bus left event dispatcher state: starting=%v access=%v cancel=%v stop=%v", backend.eventStarting, backend.eventAccess, backend.eventCancel != nil, backend.eventStartStop != nil)
	}
}

type observedDoneContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestDispatcherCleanupStopsSubscriberWatcherWithoutCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := make(chan struct{})
	subscriberDone := make(chan struct{})
	runDone := make(chan struct{})
	out := make(chan Event, 1)
	backend := &dbusBackend{
		subscribers: map[uint64]*eventSubscriber{
			1: {out: out, done: subscriberDone},
		},
		eventDone: runDone,
	}
	watcherCtx := &observedDoneContext{Context: ctx, observed: observed}
	watcherDone := make(chan struct{})
	go func() {
		backend.watchSubscriber(watcherCtx, 1)
		close(watcherDone)
	}()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("subscriber watcher did not enter its context wait")
	}

	backend.beginEventDispatcherCleanup(runDone)
	select {
	case <-watcherDone:
	case <-time.After(time.Second):
		t.Fatal("dispatcher cleanup left subscriber watcher waiting on caller cancellation")
	}
	select {
	case _, open := <-out:
		if open {
			t.Fatal("dispatcher cleanup left subscriber stream open")
		}
	default:
		t.Fatal("dispatcher cleanup left subscriber stream open")
	}
	select {
	case <-subscriberDone:
	default:
		t.Fatal("subscriber lifetime signal remained open after dispatcher cleanup")
	}
}

// Both the subscriber-lifetime context and D-Bus connection may be done when
// the dispatcher wakes. Selection order must not determine handle validity.
func TestDisconnectedEventDispatcherInvalidatesHandlesWithBothClosuresReady(t *testing.T) {
	address := startTestDBus(t)
	for attempt := 0; attempt < 48; attempt++ {
		conn, err := dbusutil.ConnectContext(context.Background(), address)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		if conn.Context().Err() == nil {
			t.Fatal("connection still live after Close")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		backend := &dbusBackend{generation: 1, access: conn}
		done := make(chan struct{})
		backend.runEventDispatcher(ctx, conn, make(chan *dbus.Signal), nil, nil, nil, done)
		if backend.Generation() != 2 {
			t.Fatalf("iteration %d: bus disconnect lost to canceled dispatcher context; generation=%d", attempt, backend.Generation())
		}
		if !backend.disconnected {
			t.Fatalf("iteration %d: disconnected state was not recorded", attempt)
		}
	}
}

// An already-running dispatcher must never acknowledge a newly canceled
// event subscription. In particular, the optimization that reuses the active
// event transport must still respect the new caller's context.
func TestATSPIEventDispatcherReuseRejectsCanceledCaller(t *testing.T) {
	b, _ := newPrivateCacheBackend(t, nil)
	b.eventCancel = func() {}
	b.eventRegistered = append([]string(nil), semanticObservationEventFamilies[:]...)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, requireSemantic := range []bool{false, true} {
		if err := b.startEvents(ctx, requireSemantic); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled caller reused event dispatcher (semantic=%t): %v", requireSemantic, err)
		}
	}
	if err := b.startEvents(context.Background(), false); err != nil {
		t.Fatalf("healthy event subscriber denied existing dispatcher: %v", err)
	}
	if err := b.startEvents(context.Background(), true); err != nil {
		t.Fatalf("semantic subscriber denied complete dispatcher: %v", err)
	}
	// An existing notification-only dispatcher cannot claim semantic event
	// coverage that the provider never registered.
	b.eventRegistered = []string{"object:property-change"}
	if err := b.startEvents(context.Background(), true); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("incomplete event dispatcher claimed semantic coverage: %v", err)
	}
	if err := b.startEvents(context.Background(), false); err != nil {
		t.Fatalf("notification-only subscription rejected: %v", err)
	}
}

// This wrapper exposes exactly the first context read so a real cancellation
// can race with a blocked backend connection check, without sleeps or polls.
type observedEventContext struct {
	context.Context
	readOnce  sync.Once
	firstRead chan struct{}
}

func (ctx *observedEventContext) Err() error {
	ctx.readOnce.Do(func() { close(ctx.firstRead) })
	return ctx.Context.Err()
}

func TestATSPIEventDispatcherReuseNotAcknowledgedWhenCallerCancelsDuringAdmission(t *testing.T) {
	b, _ := newPrivateCacheBackend(t, nil)
	b.eventCancel = func() {}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &observedEventContext{Context: base, firstRead: make(chan struct{})}
	b.mu.Lock() // connected() must observe the transport while the lock is held.
	result := make(chan error, 1)
	go func() { result <- b.startEvents(ctx, false) }()
	select {
	case <-ctx.firstRead:
	case <-time.After(time.Second):
		b.mu.Unlock()
		t.Fatal("event admission did not read caller context")
	}
	cancel()
	b.mu.Unlock()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled caller acquired an already-running event stream: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("event admission failed to terminate after cancellation")
	}
}

// A subscription waiting on a finished setup owned by another caller must
// not spin indefinitely or acknowledge an event stream that was never
// installed. The retry allowance is bounded to avoid wedging new subscribers.
func TestATSPIEventOverlappingSetupHasBoundedRetryAndPreservesFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		priorErr  error
		cancelled bool
		want      error
	}{
		{"canceled starter exhausts retry allowance", context.Canceled, true, context.Canceled},
		{"provider failure is not retried", ErrUnsupported, false, ErrUnsupported},
		{"failure after canceled setup remains visible", ErrUnsupported, true, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, b, registry := startSyntheticEventBus(t, nil)
			done := make(chan struct{})
			close(done)
			b.eventStarting = &eventStart{done: done, err: tc.priorErr, callerCanceled: tc.cancelled}
			err := b.startEvents(context.Background(), true)
			if !errors.Is(err, tc.want) || b.eventCancel != nil || len(b.eventRegistered) != 0 {
				t.Fatalf("finished overlapping setup incorrectly started a stream: err=%v want=%v cancel=%v registrations=%v", err, tc.want, b.eventCancel, b.eventRegistered)
			}
			registry.mu.Lock()
			calls := len(registry.registered)
			registry.mu.Unlock()
			if calls != 0 {
				t.Fatalf("prior setup error triggered %d fresh registrations", calls)
			}
		})
	}
}

type syntheticEventRegistry struct {
	mu         sync.Mutex
	registered []string
	removed    []string
	reject     map[string]bool
}

func (r *syntheticEventRegistry) RegisterEvent(name string, _ []string, _ string) *dbus.Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reject[name] {
		return dbus.MakeFailedError(errors.New("event family unavailable"))
	}
	r.registered = append(r.registered, name)
	return nil
}
func (r *syntheticEventRegistry) DeregisterEvent(name string) *dbus.Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, name)
	return nil
}
func startSyntheticEventBus(t *testing.T, reject map[string]bool) (*dbus.Conn, *dbusBackend, *syntheticEventRegistry) {
	t.Helper()
	addr := startTestDBus(t)
	service, err := dbusutil.ConnectContext(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(registryName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request test registry name: %v %v", reply, err)
	}
	reg := &syntheticEventRegistry{reject: reject}
	if lintShadow49 := service.Export(reg, registryPath, registryName); lintShadow49 != nil {
		t.Fatal(lintShadow49)
	}
	client, err := dbusutil.ConnectContext(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return service, &dbusBackend{generation: 1, access: client}, reg
}
func TestAccessibilityPrivateBusEventSubscriptionLifecycle(t *testing.T) {
	service, b, registry := startSyntheticEventBus(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := b.Events(ctx, EventOptions{RequireSemanticCoverage: true, Buffer: 4})
	if err != nil {
		t.Fatalf("subscription did not register complete AT-SPI coverage: %v", err)
	}
	registry.mu.Lock()
	registered := append([]string(nil), registry.registered...)
	registry.mu.Unlock()
	if err := validateSemanticEventCoverage(registered); err != nil {
		t.Fatalf("missing synthetic family: %v", err)
	}
	source := dbus.ObjectPath("/org/a11y/atspi/accessible/test")
	for _, tc := range []struct{ property, value string }{
		{"Name", "Editor"}, {"Value", "never-display-this"}, {"Description", "short description"},
	} {
		if err := service.Emit(source, "org.a11y.atspi.Event.Object.PropertyChange", tc.property, tc.value); err != nil {
			t.Fatal(err)
		}
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream terminated during valid subscription")
			}
			if ev.Property != tc.property || ev.Node.ObjectPath != string(source) {
				t.Fatalf("incorrect event delivery: %+v", ev)
			}
			if tc.property == "Value" && ev.Value != "" {
				t.Fatalf("private value leaked: %+v", ev)
			}
			if tc.property != "Value" && ev.Value != tc.value {
				t.Fatalf("lost ordinary event data: %+v", ev)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("event %q not forwarded", tc.property)
		}
	}
	cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("event stream remained open after caller canceled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event stream did not close")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		registry.mu.Lock()
		count := len(registry.removed)
		registry.mu.Unlock()
		if count == len(registered) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscription cleanup removed %d/%d families", count, len(registered))
		}
		time.Sleep(time.Millisecond)
	}
}
func TestAccessibilityPrivateBusSemanticSubscriptionRejectsPartialRegistry(t *testing.T) {
	_, b, registry := startSyntheticEventBus(t, map[string]bool{"object:children-changed": true})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := b.Events(ctx, EventOptions{RequireSemanticCoverage: true})
	if stream != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("partial AT-SPI coverage was accepted: channel=%v err=%v", stream, err)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if len(registry.registered) == 0 || len(registry.removed) != len(registry.registered) {
		t.Fatalf("failed setup did not deregister: registered=%v removed=%v", registry.registered, registry.removed)
	}
}

func TestAccessibilityPrivateBusMultipleSubscribersDoNotRestartOrStealEvents(t *testing.T) {
	service, b, registry := startSyntheticEventBus(t, nil)
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	first, err := b.Events(ctx1, EventOptions{Buffer: 2})
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.Events(ctx2, EventOptions{Buffer: 2})
	if err != nil {
		t.Fatal(err)
	}
	registry.mu.Lock()
	registrations := len(registry.registered)
	registry.mu.Unlock()
	if registrations != 10 {
		t.Fatalf("shared subscriptions issued %d registrations, want one set", registrations)
	}
	path := dbus.ObjectPath("/org/a11y/atspi/accessible/editor")
	if err := service.Emit(path, "org.a11y.atspi.Event.Focus.Focus", "focused", int32(1)); err != nil {
		t.Fatal(err)
	}
	for _, stream := range []<-chan Event{first, second} {
		select {
		case event := <-stream:
			if event.Property != "focused" {
				t.Fatalf("wrong event delivery: %+v", event)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("subscriber did not receive focus")
		}
	}
	cancel1()
	select {
	case _, ok := <-first:
		if ok {
			t.Fatal("canceled subscriber remained live")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first subscriber failed to stop")
	}
	if err := service.Emit(path, "org.a11y.atspi.Event.Focus.Focus", "selection", int32(0)); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-second:
		if event.Property != "selection" {
			t.Fatalf("remaining subscriber missed event %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("last subscriber lost stream")
	}
	registry.mu.Lock()
	if len(registry.registered) != registrations || len(registry.removed) != 0 {
		t.Fatalf("one subscriber cancellation restarted physical subscription: added=%v removed=%v", registry.registered, registry.removed)
	}
	registry.mu.Unlock()
	cancel2()
	select {
	case _, ok := <-second:
		if ok {
			t.Fatal("last canceled stream still active")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("last stream not released")
	}
}

func TestAccessibilityPrivateBusDisconnectInvalidatesHandlesAndStopsEvents(t *testing.T) {
	_, b, _ := startSyntheticEventBus(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := b.Events(ctx, EventOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.access.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("closed bus delivered an event")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bus disconnection did not terminate stream")
	}
	deadline := time.Now().Add(2 * time.Second)
	for b.Generation() == 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if b.Generation() != 2 {
		t.Fatalf("bus disconnection failed to invalidate AT-SPI handle epoch: %d", b.Generation())
	}
	if _, err := b.Events(context.Background(), EventOptions{}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("disconnected bus accepted new subscriber: %v", err)
	}
}

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

// The event dispatcher never publishes a stale object identity after closure,
// and denial of an event must preserve the last authoritative generation.
func TestEventTransitionRejectsClosedOrDisconnectedBackend(t *testing.T) {
	event := Event{Kind: "object:property-change", Node: NodeID{BusName: "org.test.Application", ObjectPath: "/item", Generation: 1}}
	for _, tc := range []struct {
		name    string
		backend *dbusBackend
	}{
		{"nil", nil},
		{"closed", &dbusBackend{closed: true, generation: 7}},
		{"disconnected", &dbusBackend{disconnected: true, generation: 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, active := tc.backend.prepareEventTransition(nil, event)
			if active {
				t.Fatal("closed event provider authorized input")
			}
			if tc.backend != nil && out.Node.Generation != tc.backend.generation {
				t.Fatalf("stale handle generation %d != %d", out.Node.Generation, tc.backend.generation)
			}
		})
	}
}

func TestUntrustedDestroySignalInvalidatesAllCachedHandles(t *testing.T) {
	for _, sig := range []*dbus.Signal{
		{Name: "org.a11y.atspi.Event.Window:Destroy", Sender: "org.test.Application", Path: ""},
		{Name: "org.a11y.atspi.Event.Window:Destroy", Sender: "", Path: "/node"},
	} {
		b := &dbusBackend{generation: 3, cacheItems: map[NodeID]cacheItem{{BusName: "org.test.Application", ObjectPath: "/other", Generation: 3}: {Name: "protected"}}, cacheApps: map[string]bool{"org.test.Application": true}}
		b.transitionObjectHandlesLocked(sig)
		if b.generation != 4 || b.cacheItems != nil || b.cacheApps != nil {
			t.Fatalf("unknown destroyed object left handles fresh: generation=%d items=%v apps=%v", b.generation, b.cacheItems, b.cacheApps)
		}
	}
}

func TestEventDeliveryTracksBackpressureWithoutCrossSubscriberLeaks(t *testing.T) {
	var nilBackend *dbusBackend
	nilBackend.deliverEvent(Event{Kind: "ignored"})
	slow := &eventSubscriber{out: make(chan Event, 1)}
	fast := &eventSubscriber{out: make(chan Event, 3)}
	b := &dbusBackend{subscribers: map[uint64]*eventSubscriber{1: slow, 2: fast}}
	b.deliverEvent(Event{Kind: "initial"})
	b.deliverEvent(Event{Kind: "second"})
	if slow.dropped != 1 || fast.dropped != 0 {
		t.Fatalf("incorrect overflow accounting slow=%d fast=%d", slow.dropped, fast.dropped)
	}
	first := <-slow.out
	if first.Kind != "initial" || first.Dropped != 0 {
		t.Fatalf("first event corrupted: %+v", first)
	}
	b.deliverEvent(Event{Kind: "after release"})
	last := <-slow.out
	if last.Kind != "after release" || last.Dropped != 1 || slow.dropped != 0 {
		t.Fatalf("lost events not reported after backpressure: %+v remaining=%d", last, slow.dropped)
	}
	for i, want := range []string{"initial", "second", "after release"} {
		got := <-fast.out
		if got.Kind != want || got.Dropped != 0 {
			t.Fatalf("fast subscriber event %d: %+v", i, got)
		}
	}
}

func TestEventSubscriberReservationWaitsForPreviousDispatcherCleanup(t *testing.T) {
	done := make(chan struct{})
	b := &dbusBackend{eventCancel: func() {}, eventDone: done}
	_, start, wait, err := b.reserveEventSubscriber(make(chan Event, 1))
	if err != nil || start || wait != done || len(b.subscribers) != 0 {
		t.Fatalf("overlapping dispatcher admitted: start=%v wait=%v err=%v", start, wait, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitErr := waitForEventRun(ctx, done); !errors.Is(waitErr, context.Canceled) {
		t.Fatalf("canceled wait accepted dispatcher: %v", waitErr)
	}
	b.finishEventDispatcher(done)
	if waitErr := waitForEventRun(context.Background(), done); waitErr != nil {
		t.Fatalf("completed dispatcher wait: %v", waitErr)
	}
	id, start, wait, err := b.reserveEventSubscriber(make(chan Event, 1))
	if err != nil || !start || wait != nil || id == 0 || len(b.subscribers) != 1 {
		t.Fatalf("dispatcher not restarted after cleanup: id=%d start=%v wait=%v err=%v", id, start, wait, err)
	}
}

func TestEventShutdownCancelsSetupAndActiveDispatcherWithoutConnection(t *testing.T) {
	var nilBackend *dbusBackend
	nilBackend.stopEvents(nil)
	for _, tc := range []struct {
		name  string
		setup bool
	}{
		{"start", true}, {"dispatcher", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canceled := make(chan struct{})
			done := make(chan struct{})
			close(done)
			b := &dbusBackend{}
			if tc.setup {
				b.eventStarting = &eventStart{done: done}
				b.eventStartStop = func() { close(canceled) }
			} else {
				b.eventDone = done
				b.eventCancel = func() { close(canceled) }
			}
			b.stopEvents(nil)
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("event owner shutdown did not cancel")
			}
		})
	}
}

func TestEventRegistrationPreCanceledNeverCallsRegistryAndNullCleanupIsSafe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	registry := &countingEventRegistrar{}
	registered, err := registerEventFamilies(ctx, registry, "org.test.Client", []string{"object:property-change"})
	if len(registered) != 0 || !errors.Is(err, context.Canceled) || registry.calls != 0 {
		t.Fatalf("pre-canceled registry used: registered=%v err=%v calls=%d", registered, err, registry.calls)
	}
	deregisterEvents(context.Background(), nil, []string{"object:property-change"})
	removeEventMatches(context.Background(), nil, nil)
}

type countingEventRegistrar struct{ calls int }

func (r *countingEventRegistrar) CallWithContext(_ context.Context, _ string, _ dbus.Flags, _ ...any) *dbus.Call {
	r.calls++
	return &dbus.Call{}
}

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

// Retired dispatchers must never restart registration, even if the underlying
// AT-SPI connection remains live during teardown.
func TestATSPIEventRetirementCannotRestartConnectedDispatcher(t *testing.T) {
	_, b, registry := startSyntheticEventBus(t, nil)
	b.eventsMu.Lock()
	b.eventsRetired = true
	b.eventsMu.Unlock()
	if err := b.startEvents(context.Background(), true); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("retired dispatcher restarted against a live bus: %v", err)
	}
	if events, err := b.Events(context.Background(), EventOptions{}); events != nil || !errors.Is(err, ErrDisconnected) {
		t.Fatalf("retired dispatcher admitted subscriber: events=%v err=%v", events, err)
	}
	registry.mu.Lock()
	registrations := len(registry.registered)
	registry.mu.Unlock()
	if registrations != 0 {
		t.Fatalf("retired dispatcher registered %d event families", registrations)
	}
}

// A waiting subscriber can join only after the previous dispatcher publishes
// completion and clears its run identity. A fresh run then owns the stream.
func TestATSPIEventSubscriberRestartsAfterPriorDispatcherCompletion(t *testing.T) {
	_, b, registry := startSyntheticEventBus(t, nil)
	oldDone := make(chan struct{})
	b.eventsMu.Lock()
	b.eventCancel = func() {}
	b.eventDone = oldDone
	b.eventsMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notified := &notifyEventWaitContext{Context: ctx, entered: make(chan struct{})}
	type answer struct {
		events <-chan Event
		err    error
	}
	result := make(chan answer, 1)
	go func() { events, err := b.Events(notified, EventOptions{Buffer: 2}); result <- answer{events, err} }()
	select {
	case <-notified.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not wait on old dispatcher")
	}
	b.eventsMu.Lock()
	before := len(b.subscribers)
	b.eventsMu.Unlock()
	if before != 0 {
		t.Fatalf("waiting subscriber registered before cleanup: %d", before)
	}
	b.finishEventDispatcher(oldDone)
	select {
	case r := <-result:
		if r.err != nil || r.events == nil {
			t.Fatalf("subscriber not started after old run completed: events=%v err=%v", r.events, r.err)
		}
		registry.mu.Lock()
		registered := len(registry.registered)
		registry.mu.Unlock()
		if registered == 0 {
			t.Fatal("new subscription did not register event families")
		}
		cancel()
		select {
		case _, ok := <-r.events:
			if ok {
				t.Fatal("cancelled new subscription remained live")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("new subscription did not close")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not restart after old run completed")
	}
}

func TestEventStartWaitingCannotAcknowledgeAlreadyCanceledCaller(t *testing.T) {
	for i := 0; i < 128; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		state := &eventStart{done: make(chan struct{})}
		close(state.done)
		cancel()
		retry, err := waitForEventStart(ctx, state, nil, 0)
		if retry || !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d completed stale event setup for canceled subscriber: retry=%v err=%v", i, retry, err)
		}
	}
}
func TestEventStartRetryOnlyForCanceledSetupAndHealthyWaiter(t *testing.T) {
	lost := errors.New("the AT-SPI event registry refused all subscriptions")
	disconnected := ErrDisconnected
	for _, tc := range []struct {
		name              string
		error, connection error
		callerCanceled    bool
		attempts          int
		retry             bool
		want              error
	}{
		{"successful setup", nil, nil, false, 0, false, nil},
		{"provider denied", lost, nil, false, 0, false, lost},
		{"retry healthy waiter after canceled starter", context.Canceled, nil, true, 0, true, nil},
		{"retries exhausted", context.Canceled, nil, true, eventStartRetryLimit, false, context.Canceled},
		{"connection lost during canceled setup", context.Canceled, disconnected, true, 0, false, disconnected},
		{"real provider failure cannot retry", lost, nil, true, 0, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &eventStart{done: make(chan struct{}), err: tc.error, callerCanceled: tc.callerCanceled}
			close(state.done)
			retry, err := waitForEventStart(context.Background(), state, tc.connection, tc.attempts)
			if retry != tc.retry || !errors.Is(err, tc.want) {
				t.Fatalf("incorrect event setup retry=%v err=%v want retry=%v err=%v", retry, err, tc.retry, tc.want)
			}
		})
	}
}

func TestStopEventsReturnsWhenSetupOrDispatcherOwnerStalls(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup bool
	}{
		{name: "setup owner", setup: true},
		{name: "dispatcher owner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canceled := make(chan struct{})
			ownerDone := make(chan struct{})
			backend := &dbusBackend{}
			if tc.setup {
				backend.eventStarting = &eventStart{done: ownerDone}
				backend.eventStartStop = func() { close(canceled) }
			} else {
				backend.eventDone = ownerDone
				backend.eventCancel = func() { close(canceled) }
			}

			returned := make(chan struct{})
			go func() {
				backend.stopEvents(nil)
				close(returned)
			}()
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("shutdown did not cancel the stalled event owner")
			}
			select {
			case <-returned:
			case <-time.After(2 * eventCleanupTimeout):
				t.Fatal("shutdown remained blocked after the cleanup deadline")
			}
			select {
			case <-ownerDone:
				t.Fatal("stalled event owner completed unexpectedly")
			default:
			}
			close(ownerDone)
		})
	}
}

type notifyEventWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *notifyEventWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

// Subscriber admission must not race a departing dispatcher: a caller that
// cancels while waiting for the old run must be rejected, without installing
// a subscriber, restarting registration, or stealing the next healthy run.
func TestATSPIPrivateBusSubscriberCancelWhilePreviousDispatcherCleansUp(t *testing.T) {
	_, b, registry := startSyntheticEventBus(t, nil)
	oldDone := make(chan struct{})
	b.eventCancel = func() {}
	b.eventDone = oldDone
	base, cancel := context.WithCancel(context.Background())
	ctx := &notifyEventWaitContext{Context: base, entered: make(chan struct{})}
	type answer struct {
		stream <-chan Event
		err    error
	}
	result := make(chan answer, 1)
	go func() { stream, err := b.Events(ctx, EventOptions{Buffer: 2}); result <- answer{stream, err} }()
	select {
	case <-ctx.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not reach prior-dispatcher cleanup wait")
	}
	cancel()
	select {
	case got := <-result:
		if got.stream != nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled pending subscription accepted: channel=%v err=%v", got.stream, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled subscriber remained blocked during dispatcher cleanup")
	}
	b.eventsMu.Lock()
	queued := len(b.subscribers)
	b.eventsMu.Unlock()
	registry.mu.Lock()
	registered := len(registry.registered)
	registry.mu.Unlock()
	if queued != 0 || registered != 0 {
		t.Fatalf("canceled pending subscriber started provider: subscribers=%d registrations=%d", queued, registered)
	}
	b.finishEventDispatcher(oldDone)
	healthyCtx, healthyCancel := context.WithCancel(context.Background())
	defer healthyCancel()
	stream, err := b.Events(healthyCtx, EventOptions{Buffer: 2})
	if err != nil || stream == nil {
		t.Fatalf("healthy subscriber cannot start after cleanup: stream=%v err=%v", stream, err)
	}
	healthyCancel()
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("cancelled healthy subscription remained open")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("healthy subscription did not shut down")
	}
}
