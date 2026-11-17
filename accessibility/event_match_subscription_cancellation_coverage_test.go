package accessibility

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

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
