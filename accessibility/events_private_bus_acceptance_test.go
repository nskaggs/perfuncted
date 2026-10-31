package accessibility

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

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
	if err := service.Export(reg, registryPath, registryName); err != nil {
		t.Fatal(err)
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
