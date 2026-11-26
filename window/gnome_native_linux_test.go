//go:build linux

package window

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/internal/dbusutil"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"github.com/nskaggs/perfuncted/v2/internal/gnomebridge"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Closing a session from several owners must not double-close its event stream
// or leave a native event subscription alive.
func TestGnomeNativeManagerConcurrentCloseReleasesSubscriptionOnce(t *testing.T) {
	source := make(chan gnomebridge.WindowEvent)
	var releases atomic.Int32
	m := &GnomeNativeManager{
		bridge:     &gnomebridge.Client{},
		events:     make(chan Event, 1),
		stopEvents: make(chan struct{}),
		eventsDone: make(chan struct{}),
	}
	go m.forwardWindowEvents(source, func() { releases.Add(1) })
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			failures <- m.Close()
		}()
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent close blocked waiting for active GNOME events")
	}
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatalf("close returned error: %v", err)
		}
	}
	if n := releases.Load(); n != 1 {
		t.Fatalf("subscription released %d times, want once", n)
	}
	if _, ok := <-m.WindowEvents(); ok {
		t.Fatal("event stream remains open after concurrent close")
	}
}

// A native event publisher must not stall while the window-events consumer
// is absent; its event channel is explicitly bounded and lossy.
func TestGnomeNativeEventForwarderNeverBlocksOnFullSubscriberQueue(t *testing.T) {
	source := make(chan gnomebridge.WindowEvent, 150)
	release := make(chan struct{})
	manager := &GnomeNativeManager{events: make(chan Event, 1), stopEvents: make(chan struct{}), eventsDone: make(chan struct{})}
	source <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowAddedEvent, Window: gnomebridge.WindowInfo{ID: "18", Title: "private document"}}
	for i := 0; i < 100; i++ {
		source <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowChangedEvent, Window: gnomebridge.WindowInfo{ID: "18"}}
	}
	source <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowEventKind("not-a-supported-event")}
	close(source)
	go manager.forwardWindowEvents(source, func() { close(release) })
	select {
	case <-manager.eventsDone:
	case <-time.After(time.Second):
		t.Fatal("slow subscriber blocked GNOME bridge dispatch")
	}
	select {
	case <-release:
	default:
		t.Fatal("forwarder did not cancel its GNOME event subscription")
	}
	events := []Event{}
	for event := range manager.WindowEvents() {
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Kind != WindowAddedEvent || events[0].ID != "18" || events[0].Window.Title != "private document" {
		t.Fatalf("bounded queue lost first event or published unsupported kind: %+v", events)
	}
}

func TestGnomeNativeEventForwarderStopsWhenUnsubscribedWithIdleSource(t *testing.T) {
	source := make(chan gnomebridge.WindowEvent)
	release := make(chan struct{})
	manager := &GnomeNativeManager{events: make(chan Event, 1), stopEvents: make(chan struct{}), eventsDone: make(chan struct{})}
	go manager.forwardWindowEvents(source, func() { close(release) })
	close(manager.stopEvents)
	select {
	case <-manager.eventsDone:
	case <-time.After(time.Second):
		t.Fatal("closing owner did not stop event forwarding")
	}
	select {
	case <-release:
	default:
		t.Fatal("event subscription not released when owner stopped")
	}
	if _, ok := <-manager.WindowEvents(); ok {
		t.Fatal("owner closed without closing its event stream")
	}
}

func TestGnomeNativeNilManagerIteratorFailsClosedWithoutEmittingWindow(t *testing.T) {
	var manager *GnomeNativeManager
	if manager.WindowEvents() != nil {
		t.Fatal("nil manager exposed live event source")
	}
	reports := 0
	for info, err := range manager.IterateWindows(context.Background()) {
		reports++
		if info.ID != 0 || !strings.Contains(err.Error(), "not initialised") {
			t.Fatalf("nil manager produced window=%+v err=%v", info, err)
		}
	}
	if reports != 1 {
		t.Fatalf("expected single terminal error, got %d", reports)
	}
}

// Window-added/changed events can omit their outer ID. Preserve the ID from
// the embedded window so downstream consumers can still reconcile by identity.
func TestGnomeNativeEventsRecoverMissingOuterWindowIdentity(t *testing.T) {
	src := make(chan gnomebridge.WindowEvent, 3)
	released := make(chan struct{})
	m := &GnomeNativeManager{events: make(chan Event, 3), stopEvents: make(chan struct{}), eventsDone: make(chan struct{})}
	src <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowAddedEvent, Window: gnomebridge.WindowInfo{ID: "211", Title: "first"}}
	src <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowChangedEvent, Window: gnomebridge.WindowInfo{ID: "212", Title: "changed"}}
	src <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowRemovedEvent, ID: "213"}
	close(src)
	go m.forwardWindowEvents(src, func() { close(released) })
	select {
	case <-m.eventsDone:
	case <-time.After(time.Second):
		t.Fatal("event forwarder stalled")
	}
	select {
	case <-released:
	default:
		t.Fatal("subscription release not invoked")
	}
	var events []Event
	for event := range m.events {
		events = append(events, event)
	}
	if len(events) != 3 || events[0].ID != "211" || events[0].Window.NativeID != "211" || events[1].ID != "212" || events[1].Window.NativeID != "212" || events[2].ID != "213" || events[2].Kind != WindowRemovedEvent {
		t.Fatalf("missing outer IDs incorrectly reconciled: %+v", events)
	}
}

type privateGNOMECore struct{}

func (privateGNOMECore) GetProtocolVersion() (uint32, *dbus.Error) {
	return gnomebridge.ProtocolVersion, nil
}
func (privateGNOMECore) GetExtensionVersion() (string, *dbus.Error) { return "1-test", nil }
func (privateGNOMECore) GetShellVersion() (string, *dbus.Error)     { return "51-test", nil }
func (privateGNOMECore) GetCapabilities() ([]string, *dbus.Error) {
	return []string{gnomebridge.CapabilityWindows}, nil
}

type privateGNOMEWindows struct{}

func (privateGNOMEWindows) ListWindows() ([]gnomebridge.WindowInfo, *dbus.Error) {
	return []gnomebridge.WindowInfo{{ID: "17", Title: "Private Editor", AppID: "org.test.Editor", Width: 600, Height: 400}}, nil
}
func (privateGNOMEWindows) GetActiveWindow() (gnomebridge.WindowInfo, *dbus.Error) {
	return gnomebridge.WindowInfo{ID: "17", Title: "Private Editor", Active: true}, nil
}
func (privateGNOMEWindows) GetWindow(id string) (gnomebridge.WindowInfo, *dbus.Error) {
	if id != "17" {
		return gnomebridge.WindowInfo{}, dbus.NewError("io.github.nskaggs.perfuncted.Gnome1.Error.NotFound", []any{"missing window"})
	}
	return gnomebridge.WindowInfo{ID: id, Title: "Private Editor"}, nil
}

func TestGnomeNativeManagerPrivateBusListingAndSubscription(t *testing.T) { //nolint:gocyclo // One isolated D-Bus lifecycle verifies setup, reads, events and shutdown.
	_, address, _ := startSyntheticGNOMEEvalBus(t)
	conn, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if reply, nameErr := conn.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue); nameErr != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim private bridge name: %v %v", reply, nameErr)
	}
	path := dbus.ObjectPath(gnomebridge.ObjectPath)
	if exportErr := conn.Export(privateGNOMECore{}, path, gnomebridge.CoreInterface); exportErr != nil {
		t.Fatal(exportErr)
	}
	if exportErr := conn.Export(privateGNOMEWindows{}, path, gnomebridge.WindowsInterface); exportErr != nil {
		t.Fatal(exportErr)
	}
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + address})
	startup, stop := context.WithTimeout(context.Background(), 2*time.Second)
	manager, err := NewGnomeNativeManagerForRuntimeContext(startup, rt)
	stop() // Startup context must not determine subscription lifetime.
	if err != nil {
		t.Fatalf("GNOME manager denied valid bridge: %v", err)
	}
	defer manager.Close()
	list, err := manager.List(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != 17 || list[0].Title != "Private Editor" {
		t.Fatalf("list %+v %v", list, err)
	}
	count := 0
	for win, err := range manager.IterateWindows(context.Background()) {
		if err != nil || win.ID != 17 {
			t.Fatalf("iterator %+v %v", win, err)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("iterator count %d", count)
	}
	title, err := manager.ActiveTitle(context.Background())
	if err != nil || title != "Private Editor" {
		t.Fatalf("active title %q %v", title, err)
	}
	got, err := manager.InfoByID(context.Background(), "17")
	if err != nil || got.ID != 17 {
		t.Fatalf("window ID %+v %v", got, err)
	}
	if _, err := manager.InfoByID(context.Background(), "absent"); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("missing window: %v", err)
	}
	if err := conn.Emit(path, gnomebridge.WindowsInterface+".WindowRemoved", "17"); err != nil {
		t.Fatal(err)
	}
	select {
	case event, ok := <-manager.WindowEvents():
		if !ok || event.Kind != WindowRemovedEvent || event.ID != "17" {
			t.Fatalf("native event after canceled setup %+v available=%t", event, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("native event stream stopped with setup context")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-manager.WindowEvents(); ok {
		t.Fatal("event stream still open after close")
	}
	if windows, err := manager.List(context.Background()); err == nil || len(windows) != 0 || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed listing %+v %v", windows, err)
	}
}

func TestToWindowInfoPreservesNativeFields(t *testing.T) {
	got := toWindowInfo(gnomebridge.WindowInfo{
		ID: "17", Title: "Terminal", AppID: "org.gnome.Terminal", Class: "Terminal",
		PID: 42, X: 1, Y: 2, Width: 800, Height: 600,
		Active: true, Minimized: false, Maximized: true, Fullscreen: true,
	})
	if got.ID != 17 || got.StableID() != "17" || got.Title != "Terminal" || got.AppID != "org.gnome.Terminal" ||
		got.Class != "Terminal" || got.PID != 42 || got.X != 1 || got.Y != 2 || got.W != 800 || got.H != 600 ||
		!got.Active || got.Minimized || !got.Maximized || !got.Fullscreen {
		t.Fatalf("toWindowInfo = %#v", got)
	}
}

func TestGnomeNativeManagerNilReceiverReturnsErrors(t *testing.T) {
	var manager *GnomeNativeManager
	if _, err := manager.List(context.Background()); err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Fatalf("List error = %v, want initialization error", err)
	}
	if _, err := manager.ActiveTitle(context.Background()); err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Fatalf("ActiveTitle error = %v, want initialization error", err)
	}
	if err := manager.Sync(context.Background()); err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Fatalf("Sync error = %v, want initialization error", err)
	}
	if _, err := manager.InfoByID(context.Background(), "1"); err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Fatalf("InfoByID error = %v, want initialization error", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("nil Close error = %v, want nil", err)
	}
}

func TestGnomeNativeManagerCloseHandlesPartialConstruction(t *testing.T) {
	manager := &GnomeNativeManager{bridge: &gnomebridge.Client{}}
	if err := manager.Close(); err != nil {
		t.Fatalf("partial Close error = %v, want nil", err)
	}
}

func TestGnomeNativeManagerCloseEndsLiveEventStream(t *testing.T) {
	bridgeEvents := make(chan gnomebridge.WindowEvent)
	released := make(chan struct{})
	manager := &GnomeNativeManager{
		bridge:     &gnomebridge.Client{},
		events:     make(chan Event, 4),
		stopEvents: make(chan struct{}),
		eventsDone: make(chan struct{}),
	}
	go manager.forwardWindowEvents(bridgeEvents, func() { close(released) })

	select {
	case bridgeEvents <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowAddedEvent, Window: gnomebridge.WindowInfo{ID: "17"}}:
	case <-time.After(5 * time.Second):
		t.Fatal("bridge stream not accepted while the manager is live")
	}
	select {
	case event, ok := <-manager.WindowEvents():
		if !ok {
			t.Fatal("event stream closed while the bridge stream is still open")
		}
		if event.Kind != WindowAddedEvent || event.ID != "17" {
			t.Fatalf("forwarded event = %+v, want window-added for 17", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event forwarded while the bridge stream is still open")
	}

	// The bridge stream is deliberately still open: the manager owns the end
	// of its own event stream and must not depend on the bridge going away.
	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish while the bridge event stream stayed open")
	}

	select {
	case <-released:
	default:
		t.Fatal("bridge subscription was not released")
	}
	if _, ok := <-manager.WindowEvents(); ok {
		t.Fatal("event stream still open after Close")
	}
}

func TestToWindowInfoRetainsOpaqueID(t *testing.T) {
	got := toWindowInfo(gnomebridge.WindowInfo{ID: "opaque-id"})
	if got.ID != 0 || got.StableID() != "opaque-id" {
		t.Fatalf("toWindowInfo opaque ID = %#v", got)
	}
}

func TestForwardWindowEventsConvertsAllNativeKinds(t *testing.T) {
	bridgeEvents := make(chan gnomebridge.WindowEvent, 4)
	m := &GnomeNativeManager{
		events:     make(chan Event, 4),
		stopEvents: make(chan struct{}),
		eventsDone: make(chan struct{}),
	}
	go m.forwardWindowEvents(bridgeEvents, func() {})
	bridgeEvents <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowAddedEvent, Window: gnomebridge.WindowInfo{ID: "17", Title: "Terminal"}}
	bridgeEvents <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowChangedEvent, Window: gnomebridge.WindowInfo{ID: "17", Title: "Shell"}}
	bridgeEvents <- gnomebridge.WindowEvent{Kind: gnomebridge.WindowRemovedEvent, ID: "17"}
	bridgeEvents <- gnomebridge.WindowEvent{Kind: gnomebridge.FocusChangedEvent, ID: "17"}
	close(bridgeEvents)

	var got []Event
	for event := range m.events {
		got = append(got, event)
	}
	want := []Event{
		{Kind: WindowAddedEvent, ID: "17", Window: Info{ID: 17, NativeID: "17", Title: "Terminal"}},
		{Kind: WindowChangedEvent, ID: "17", Window: Info{ID: 17, NativeID: "17", Title: "Shell"}},
		{Kind: WindowRemovedEvent, ID: "17"},
		{Kind: FocusChangedEvent, ID: "17"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("forwarded events = %#v, want %#v", got, want)
	}
}
