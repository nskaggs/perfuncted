package accessibility

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

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
