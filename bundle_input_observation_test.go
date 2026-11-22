package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/input"
)

type fakeObservationBackend struct {
	onListen func()
	listened int
	closed   int
	err      error
	sub      *fakeObservationSub
}

func (f *fakeObservationBackend) Listen(ctx context.Context, filter input.ObservationFilter) (input.ObservationSubscription, error) {
	f.listened++
	if f.onListen != nil {
		f.onListen()
	}
	if f.err != nil {
		return nil, f.err
	}
	f.sub = &fakeObservationSub{events: make(chan input.ObservationEvent)}
	return f.sub, nil
}
func (f *fakeObservationBackend) SupportedOperations() []string {
	return []string{"listen-hotkey", "listen-button"}
}
func (f *fakeObservationBackend) Close() error {
	f.closed++
	if f.sub != nil {
		return f.sub.Close()
	}
	return nil
}

type fakeObservationSub struct {
	events chan input.ObservationEvent
	closed bool
}

func (s *fakeObservationSub) Events() <-chan input.ObservationEvent { return s.events }
func (s *fakeObservationSub) Err() error                            { return nil }
func (s *fakeObservationSub) Close() error {
	if !s.closed {
		close(s.events)
		s.closed = true
	}
	return nil
}
func setupObservationTestSession(backend input.Observer) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		ctx: ctx, cancel: cancel,
		capabilities: map[Capability]CapabilityStatus{
			CapabilityInputObservation: {Capability: CapabilityInputObservation, Available: true, Operations: []string{"listen-hotkey", "listen-button"}},
		},
	}
	s.InputObservation = &InputObservationBundle{bundleBase: s.bundleBase(CapabilityInputObservation), backend: backend}
	s.cacheResolvedBundleOperationsForTest()
	return s
}
func (s *Session) cacheResolvedBundleOperationsForTest() {
	s.InputObservation.bundleBase.cacheOperations(s.capabilities[CapabilityInputObservation].Operations)
}

func TestInputObservationSessionLifecycle(t *testing.T) {
	backend := &fakeObservationBackend{}
	session := setupObservationTestSession(backend)
	sub, err := session.InputObservation.Listen(context.Background(), input.ObservationFilter{Combo: "F8"})
	if err != nil || sub == nil || backend.listened != 1 {
		t.Fatalf("listen %v %+v", err, backend)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if backend.closed != 1 || !backend.sub.closed {
		t.Fatalf("Session.Close leaked observer: %+v", backend)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.InputObservation.Listen(context.Background(), input.ObservationFilter{Button: 1}); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session listen: %v", err)
	}
}
func TestInputObservationBundleValidationAndErrors(t *testing.T) {
	backend := &fakeObservationBackend{}
	session := setupObservationTestSession(backend)
	defer session.Close()
	if _, err := session.InputObservation.Listen(nil, input.ObservationFilter{Combo: "F8"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.InputObservation.Listen(canceled, input.ObservationFilter{Combo: "F8"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	backend.err = input.ErrObservationUnsupported
	if _, err := session.InputObservation.Listen(context.Background(), input.ObservationFilter{Combo: "F8"}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	backend.err = nil
	ctx, stop := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
	defer stop()
	sub, err := session.InputObservation.Listen(ctx, input.ObservationFilter{Button: 1})
	if err != nil || sub == nil {
		t.Fatal(err)
	}
	_ = sub.Close()
	session.capabilities[CapabilityInputObservation] = CapabilityStatus{Capability: CapabilityInputObservation, Operations: []string{"listen-hotkey"}}
	session.InputObservation.bundleBase.cacheOperations([]string{"listen-hotkey"})
	if _, err := session.InputObservation.Listen(context.Background(), input.ObservationFilter{Button: 1}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if err := (*InputObservationBundle)(nil).close(); err != nil {
		t.Fatal(err)
	}
}

// These cases exercise cancellation and session closure during the backend's
// registration response, not merely after a listener has been returned.
func TestInputObservationBundleAdmissionRaces(t *testing.T) {
	backend := &fakeObservationBackend{}
	session := setupObservationTestSession(backend)
	var nilBundle *InputObservationBundle
	if _, err := nilBundle.Listen(context.Background(), input.ObservationFilter{Combo: "F8"}); err == nil {
		t.Fatal("nil facade was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	backend.onListen = cancel
	if _, err := session.InputObservation.Listen(ctx, input.ObservationFilter{Combo: "F8"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if backend.sub == nil || !backend.sub.closed {
		t.Fatal("canceled registration leaked returned subscription")
	}
	backend.onListen = func() { _ = session.Close() }
	if _, err := session.InputObservation.Listen(context.Background(), input.ObservationFilter{Combo: "F8"}); !errors.Is(err, ErrSessionClosed) {
		t.Fatal(err)
	}
	if backend.sub == nil || !backend.sub.closed {
		t.Fatal("session closed during registration leaked subscription")
	}
}
