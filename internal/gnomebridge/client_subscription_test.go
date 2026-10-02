//go:build linux

package gnomebridge

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// fakeSubscription stands in for the bus connection so the signal
// subscription lifecycle can be driven without a session bus.
type fakeSubscription struct {
	mu          sync.Mutex
	addCtx      context.Context //nolint:containedctx // the fake records the observed handshake context so the test can assert its deadline.
	addCalls    int
	removeCalls int
	closed      bool
	signal      chan<- *dbus.Signal
}

func (f *fakeSubscription) AddMatchSignalContext(ctx context.Context, _ ...dbus.MatchOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCtx = ctx
	f.addCalls++
	return nil
}

func (f *fakeSubscription) RemoveMatchSignalContext(_ context.Context, _ ...dbus.MatchOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeCalls++
	return nil
}

func (f *fakeSubscription) Signal(ch chan<- *dbus.Signal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signal = ch
}

func (f *fakeSubscription) RemoveSignal(_ chan<- *dbus.Signal) {}

func (f *fakeSubscription) SupportsUnixFDs() bool { return true }

func (f *fakeSubscription) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeSubscription) deliver(t *testing.T, signal *dbus.Signal) {
	t.Helper()
	f.mu.Lock()
	ch := f.signal
	f.mu.Unlock()
	if ch == nil {
		t.Fatal("no signal channel registered")
	}
	select {
	case ch <- signal:
	case <-time.After(5 * time.Second):
		t.Fatal("signal channel not accepted")
	}
}

func (f *fakeSubscription) counts() (add, remove int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addCalls, f.removeCalls
}

func (f *fakeSubscription) handshakeContext() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addCtx
}

func newSubscriptionClient(sub signalSubscription) *Client {
	return &Client{conn: sub, closeDone: make(chan struct{})}
}

func windowAddedSignal(id, title string) *dbus.Signal {
	return &dbus.Signal{
		Path: dbus.ObjectPath(ObjectPath),
		Name: WindowsInterface + ".WindowAdded",
		Body: []any{WindowInfo{ID: id, Title: title}},
	}
}

func focusChangedSignal(id string) *dbus.Signal {
	return &dbus.Signal{
		Path: dbus.ObjectPath(ObjectPath),
		Name: WindowsInterface + ".FocusChanged",
		Body: []any{id},
	}
}

// Open hands capability constructors a startup-policy deadline context and
// cancels it as soon as capability setup returns. A subscription established
// with that context must keep serving the session it belongs to.
func TestSubscribeWindowEventsSurvivesSetupContextCancellation(t *testing.T) {
	sub := &fakeSubscription{}
	client := newSubscriptionClient(sub)

	setupCtx, cancelSetup := context.WithTimeout(context.Background(), time.Hour)
	events, cancelSub, err := client.SubscribeWindowEvents(setupCtx)
	if err != nil {
		t.Fatalf("SubscribeWindowEvents: %v", err)
	}
	defer cancelSub()

	// Capability setup finishes and releases its context.
	cancelSetup()
	if err := setupCtx.Err(); err == nil {
		t.Fatal("setup context is still live")
	}

	sub.deliver(t, windowAddedSignal("17", "Terminal"))
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("window event stream closed when the setup context was cancelled")
		}
		if event.Kind != WindowAddedEvent || event.ID != "17" || event.Window.Title != "Terminal" {
			t.Fatalf("event = %+v, want window-added for 17/Terminal", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no window event after the setup context was cancelled")
	}

	sub.deliver(t, focusChangedSignal("17"))
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("window event stream closed when the setup context was cancelled")
		}
		if event.Kind != FocusChangedEvent || event.ID != "17" {
			t.Fatalf("event = %+v, want focus-changed for 17", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no focus event after the setup context was cancelled")
	}
}

// The registration handshake must stay bounded by the caller's startup policy
// even though it no longer governs the stream.
func TestSubscribeWindowEventsRegistrationHonorsCallerDeadline(t *testing.T) {
	sub := &fakeSubscription{}
	client := newSubscriptionClient(sub)

	setupCtx, cancelSetup := context.WithTimeout(context.Background(), time.Hour)
	defer cancelSetup()
	if _, cancelSub, err := client.SubscribeWindowEvents(setupCtx); err != nil {
		t.Fatalf("SubscribeWindowEvents: %v", err)
	} else {
		defer cancelSub()
	}

	handshake := sub.handshakeContext()
	if handshake == nil {
		t.Fatal("registration handshake did not run")
	}
	deadline, ok := handshake.Deadline()
	if !ok {
		t.Fatal("registration handshake has no deadline")
	}
	want, _ := setupCtx.Deadline()
	if !deadline.Equal(want) {
		t.Fatalf("handshake deadline = %v, want the caller startup deadline %v", deadline, want)
	}
}

// A caller without a deadline still gets a finite registration ceiling.
func TestSubscribeWindowEventsRegistrationBoundsUndeadlinedCaller(t *testing.T) {
	sub := &fakeSubscription{}
	client := newSubscriptionClient(sub)

	_, cancelSub, err := client.SubscribeWindowEvents(context.Background())
	if err != nil {
		t.Fatalf("SubscribeWindowEvents: %v", err)
	}
	defer cancelSub()

	handshake := sub.handshakeContext()
	if handshake == nil {
		t.Fatal("registration handshake did not run")
	}
	if _, ok := handshake.Deadline(); !ok {
		t.Fatal("registration handshake has no deadline")
	}
}

// Cancelling the subscription ends the stream and releases the match rule.
func TestSubscribeWindowEventsCancelClosesStreamAndRemovesMatch(t *testing.T) {
	sub := &fakeSubscription{}
	client := newSubscriptionClient(sub)

	events, cancelSub, err := client.SubscribeWindowEvents(context.Background())
	if err != nil {
		t.Fatalf("SubscribeWindowEvents: %v", err)
	}

	cancelSub()
	cancelSub() // idempotent
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("window event stream still open after cancel")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("window event stream not closed after cancel")
	}

	add, remove := sub.counts()
	if add != 1 || remove != 1 {
		t.Fatalf("match registrations = add %d remove %d, want 1/1", add, remove)
	}
}

// The client owns its subscriptions: closing it ends every stream even though
// the subscriber never cancels.
func TestSubscribeWindowEventsClientCloseClosesStream(t *testing.T) {
	sub := &fakeSubscription{}
	client := newSubscriptionClient(sub)

	events, cancelSub, err := client.SubscribeWindowEvents(context.Background())
	if err != nil {
		t.Fatalf("SubscribeWindowEvents: %v", err)
	}
	defer cancelSub()

	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("window event stream still open after client close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("window event stream not closed after client close")
	}
}

// A closed client cannot start a subscription at all.
func TestSubscribeWindowEventsRejectsClosedClient(t *testing.T) {
	sub := &fakeSubscription{}
	client := newSubscriptionClient(sub)
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if events, cancelSub, err := client.SubscribeWindowEvents(context.Background()); err == nil {
		cancelSub()
		_ = events
		t.Fatal("SubscribeWindowEvents succeeded on a closed client")
	}
}
