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
