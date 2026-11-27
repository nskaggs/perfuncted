package perfuncted

import (
	"context"
	"errors"
	"image"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/v2/input"
)

// This is an opt-in controlled Xvfb acceptance test, not proof of native
// physical input or cross-focus delivery by human-operated devices.
func TestInputObservationSessionOnControlledX11(t *testing.T) {
	if os.Getenv("PF_X11_OBSERVATION_INTEGRATION") != "1" {
		t.Skip("requires isolated Xvfb")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	session, err := openSession(ctx, Require(CapabilityInputObservation))
	if err != nil {
		t.Fatal(err)
	}
	sub, err := session.inputObservation.Listen(ctx, input.ObservationFilter{Combo: "F8", Transition: "both"})
	if err != nil {
		_ = session.Close()
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(sub.Err(), input.ErrObservationRevoked) {
		t.Fatalf("session did not revoke owned listener: %v", sub.Err())
	}
	select {
	case _, ok := <-sub.Events():
		if ok {
			t.Fatal("delivered event after Session.Close")
		}
	case <-time.After(time.Second):
		t.Fatal("listener channel remained open after Session.Close")
	}
}

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
func setupObservationTestSession(backend input.Observer) *sessionCore {
	ctx, cancel := context.WithCancel(context.Background())
	s := &sessionCore{
		ctx: ctx, cancel: cancel,
		capabilities: map[Capability]CapabilityStatus{
			CapabilityInputObservation: {Capability: CapabilityInputObservation, Available: true, Operations: []string{"listen-hotkey", "listen-button"}},
		},
	}
	s.observationAdmission = s.bundleBase(CapabilityInputObservation)
	s.inputObservation = newObservationState(&s.observationAdmission, backend)
	s.cacheResolvedBundleOperationsForTest()
	return s
}
func (s *sessionCore) cacheResolvedBundleOperationsForTest() {
	s.observationAdmission.cacheOperations(s.capabilities[CapabilityInputObservation].Operations)
}

type fakeStateObservationBackend struct {
	*fakeObservationBackend
	keyQuery    func(context.Context, string) (input.StateObservation, error)
	lockQuery   func(context.Context, string) (input.StateObservation, error)
	buttonQuery func(context.Context, int) (input.StateObservation, error)
}

func (f *fakeStateObservationBackend) SupportedOperations() []string {
	return []string{"query-key-state", "query-lock-state", "query-button-state"}
}

func (f *fakeStateObservationBackend) KeyState(ctx context.Context, name string) (input.StateObservation, error) {
	return f.keyQuery(ctx, name)
}

func (f *fakeStateObservationBackend) LockState(ctx context.Context, name string) (input.StateObservation, error) {
	return f.lockQuery(ctx, name)
}

func (f *fakeStateObservationBackend) MouseButtonState(ctx context.Context, button int) (input.StateObservation, error) {
	return f.buttonQuery(ctx, button)
}

func setupStateObservationTestSession(backend input.Observer) *sessionCore {
	session := setupObservationTestSession(backend)
	ops := []string{"query-key-state", "query-lock-state", "query-button-state"}
	session.capabilities[CapabilityInputObservation] = CapabilityStatus{
		Capability: CapabilityInputObservation, Available: true, Operations: ops,
	}
	session.observationAdmission.cacheOperations(ops)
	return session
}

func TestInputObservationSessionLifecycle(t *testing.T) {
	backend := &fakeObservationBackend{}
	session := setupObservationTestSession(backend)
	sub, err := session.inputObservation.Listen(context.Background(), input.ObservationFilter{Combo: "F8"})
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
	if _, err := session.inputObservation.Listen(context.Background(), input.ObservationFilter{Button: 1}); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session listen: %v", err)
	}
}
func TestInputObservationBundleValidationAndErrors(t *testing.T) {
	backend := &fakeObservationBackend{}
	session := setupObservationTestSession(backend)
	defer session.Close()
	if _, err := session.inputObservation.Listen(nil, input.ObservationFilter{Combo: "F8"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.inputObservation.Listen(canceled, input.ObservationFilter{Combo: "F8"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	backend.err = input.ErrObservationUnsupported
	if _, err := session.inputObservation.Listen(context.Background(), input.ObservationFilter{Combo: "F8"}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	backend.err = nil
	ctx, stop := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
	defer stop()
	sub, err := session.inputObservation.Listen(ctx, input.ObservationFilter{Button: 1})
	if err != nil || sub == nil {
		t.Fatal(err)
	}
	_ = sub.Close()
	session.capabilities[CapabilityInputObservation] = CapabilityStatus{Capability: CapabilityInputObservation, Operations: []string{"listen-hotkey"}}
	session.observationAdmission.cacheOperations([]string{"listen-hotkey"})
	if _, err := session.inputObservation.Listen(context.Background(), input.ObservationFilter{Button: 1}); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	if err := (*inputObservationState)(nil).Close(); err != nil {
		t.Fatal(err)
	}
}

// These cases exercise cancellation and session closure during the backend's
// registration response, not merely after a listener has been returned.
func TestInputObservationBundleAdmissionRaces(t *testing.T) {
	backend := &fakeObservationBackend{}
	session := setupObservationTestSession(backend)
	var nilBundle InputObservationBundle
	if _, err := nilBundle.WithContext(context.Background()).Listen(input.ObservationFilter{Combo: "F8"}); err == nil {
		t.Fatal("nil facade was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	backend.onListen = cancel
	if _, err := session.inputObservation.Listen(ctx, input.ObservationFilter{Combo: "F8"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if backend.sub == nil || !backend.sub.closed {
		t.Fatal("canceled registration leaked returned subscription")
	}
	backend.onListen = func() { _ = session.Close() }
	if _, err := session.inputObservation.Listen(context.Background(), input.ObservationFilter{Combo: "F8"}); !errors.Is(err, ErrSessionClosed) {
		t.Fatal(err)
	}
	if backend.sub == nil || !backend.sub.closed {
		t.Fatal("session closed during registration leaked subscription")
	}
}

func TestInputObservationStateValidationAndResults(t *testing.T) {
	var calls int
	backend := &fakeStateObservationBackend{
		fakeObservationBackend: &fakeObservationBackend{},
		keyQuery: func(context.Context, string) (input.StateObservation, error) {
			calls++
			return input.StateObservation{Kind: "key", Name: "shift", State: input.StatePressed, Provenance: "unknown"}, nil
		},
		lockQuery: func(context.Context, string) (input.StateObservation, error) {
			calls++
			return input.StateObservation{Kind: "lock", Name: "capslock", State: input.StateEnabled, Provenance: "unknown"}, nil
		},
		buttonQuery: func(context.Context, int) (input.StateObservation, error) {
			calls++
			return input.StateObservation{Kind: "button", Name: "1", State: input.StateReleased, Provenance: "unknown"}, nil
		},
	}
	session := setupStateObservationTestSession(backend)
	defer session.Close()

	if _, err := session.inputObservation.KeyState(context.Background(), "invalid"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid key query: %v", err)
	}
	if _, err := session.inputObservation.LockState(context.Background(), "compose"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid lock query: %v", err)
	}
	if _, err := session.inputObservation.MouseButtonState(context.Background(), 6); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid button query: %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid queries reached backend %d times", calls)
	}
	if got, err := session.inputObservation.KeyState(context.Background(), "shift"); err != nil || got.State != input.StatePressed || got.Provenance != "unknown" {
		t.Fatalf("key state = %+v, %v", got, err)
	}
	if got, err := session.inputObservation.LockState(context.Background(), "capslock"); err != nil || got.State != input.StateEnabled {
		t.Fatalf("lock state = %+v, %v", got, err)
	}
	if got, err := session.inputObservation.MouseButtonState(context.Background(), 1); err != nil || got.State != input.StateReleased {
		t.Fatalf("button state = %+v, %v", got, err)
	}
	if calls != 3 {
		t.Fatalf("valid queries reached backend %d times, want 3", calls)
	}
}

func TestInputObservationStateUnsupportedAndCancellation(t *testing.T) {
	plain := &fakeObservationBackend{}
	session := setupStateObservationTestSession(plain)
	if _, err := session.inputObservation.KeyState(context.Background(), "shift"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("backend without StateReader: %v", err)
	}
	_ = session.Close()

	started := make(chan struct{})
	backend := &fakeStateObservationBackend{
		fakeObservationBackend: &fakeObservationBackend{},
		keyQuery: func(ctx context.Context, _ string) (input.StateObservation, error) {
			close(started)
			<-ctx.Done()
			return input.StateObservation{}, ctx.Err()
		},
		lockQuery:   func(context.Context, string) (input.StateObservation, error) { return input.StateObservation{}, nil },
		buttonQuery: func(context.Context, int) (input.StateObservation, error) { return input.StateObservation{}, nil },
	}
	session = setupStateObservationTestSession(backend)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := session.inputObservation.KeyState(ctx, "shift")
		result <- err
	}()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled state query: %v", err)
	}
	_ = session.Close()
}

func TestInputObservationStateLateResultAfterSessionClose(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	backend := &fakeStateObservationBackend{
		fakeObservationBackend: &fakeObservationBackend{},
		keyQuery: func(context.Context, string) (input.StateObservation, error) {
			close(started)
			<-release
			return input.StateObservation{Kind: "key", State: input.StatePressed}, nil
		},
		lockQuery:   func(context.Context, string) (input.StateObservation, error) { return input.StateObservation{}, nil },
		buttonQuery: func(context.Context, int) (input.StateObservation, error) { return input.StateObservation{}, nil },
	}
	session := setupStateObservationTestSession(backend)
	result := make(chan error, 1)
	go func() {
		_, err := session.inputObservation.KeyState(context.Background(), "shift")
		result <- err
	}()
	<-started
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrSessionClosed) && !errors.Is(err, context.Canceled) {
		t.Fatalf("late state result after close: %v", err)
	}
}

type authorityLossPointerRead struct {
	*closeCountingInput
	after         func()
	locationCalls int
	spaceCalls    int
}

func (b *authorityLossPointerRead) PointerLocation(context.Context) (int, int, error) {
	b.locationCalls++
	if b.after != nil {
		b.after()
	}
	return 314, 159, nil
}
func (b *authorityLossPointerRead) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	b.spaceCalls++
	if b.after != nil {
		b.after()
	}
	return input.CoordinateSpaceInfo{Kind: input.CoordinateSpacePhysical, ScaleX: 2, ScaleY: 1.5}, nil
}

// A provider may acknowledge private pointer metadata after authorization ends.
// The facade must refuse both the late result and pre-canceled dispatch.
func TestInputPointerReadsRequireAuthorityAtDispatchAndReturn(t *testing.T) {
	for _, operation := range []string{"position", "coordinate-space"} {
		for _, mode := range []string{"pre-cancel", "late-cancel", "close"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				source := &authorityLossPointerRead{closeCountingInput: &closeCountingInput{}}
				session := newSessionForTesting(nil, source, nil, nil, nil)
				t.Cleanup(func() { _ = session.Close() })
				expected := context.Canceled
				if mode == "pre-cancel" {
					cancel()
				} else if mode == "late-cancel" {
					source.after = cancel
				} else {
					expected = ErrSessionClosed
					source.after = func() { _ = session.Close() }
				}
				var err error
				if operation == "position" {
					x, y, e := session.input.PointerLocation(ctx)
					err = e
					if x != 0 || y != 0 {
						t.Errorf("leaked position %d,%d", x, y)
					}
				} else {
					info, e := session.input.PointerCoordinateSpace(ctx)
					err = e
					if info != (input.CoordinateSpaceInfo{}) {
						t.Errorf("leaked coordinate space %+v", info)
					}
				}
				calls := source.locationCalls + source.spaceCalls
				wantCalls := 1
				if mode == "pre-cancel" {
					wantCalls = 0
				}
				if !errors.Is(err, expected) || calls != wantCalls {
					t.Errorf("err=%v, calls=%d want err %v calls %d", err, calls, expected, wantCalls)
				}
			})
		}
	}
}

type motionDeliveryBackend struct {
	input.Inputter
	events    []string
	failureAt int
	flushes   int
}

func (b *motionDeliveryBackend) MouseMove(context.Context, int, int) error {
	b.events = append(b.events, "move")
	return nil
}
func (b *motionDeliveryBackend) MouseDown(context.Context, int) error {
	b.events = append(b.events, "down")
	return nil
}
func (b *motionDeliveryBackend) MouseUp(context.Context, int) error {
	b.events = append(b.events, "up")
	return nil
}
func (b *motionDeliveryBackend) Close() error { return nil }
func (b *motionDeliveryBackend) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	return input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}, nil
}
func (b *motionDeliveryBackend) FlushMotionRequests(context.Context) error {
	b.events = append(b.events, "flush")
	b.flushes++
	if b.failureAt == b.flushes {
		return errors.New("delivery acknowledgement failed")
	}
	return nil
}

func TestMotionDeliveryAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		drag     bool
		failure  int
		wantTail []string
	}{
		{"move", false, 0, []string{"move", "flush"}},
		{"move-failed", false, 1, []string{"move", "flush"}},
		{"drag", true, 0, []string{"move", "flush", "up", "flush"}},
		{"drag-endpoint-failed", true, 1, []string{"move", "flush", "up"}},
		{"drag-release-failed", true, 2, []string{"move", "flush", "up", "flush"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &motionDeliveryBackend{failureAt: tc.failure}
			s := newSessionForTesting(nil, backend, nil, nil, nil)
			defer s.Close()
			start, end := image.Pt(10, 20), image.Pt(40, 50)
			opts := input.MotionOptions{Duration: 20 * time.Millisecond}
			var err error
			if tc.drag {
				err = s.input.DragAndDropSmooth(context.Background(), start, end, opts)
			} else {
				err = s.input.MouseMoveSmooth(context.Background(), &start, end, opts)
			}
			if (err != nil) != (tc.failure != 0) {
				t.Fatalf("delivery error: %v", err)
			}
			if len(backend.events) < len(tc.wantTail) || !slices.Equal(backend.events[len(backend.events)-len(tc.wantTail):], tc.wantTail) {
				t.Fatalf("request order %v, want tail %v", backend.events, tc.wantTail)
			}
			if tc.drag && slices.Index(backend.events, "down") != 1 {
				t.Fatalf("drag press order: %v", backend.events)
			}
		})
	}
}

type motionPrivateBackend struct {
	input.Inputter
	delay time.Duration
	moves int
}

func (b *motionPrivateBackend) MouseMove(context.Context, int, int) error {
	b.moves++
	if b.delay != 0 {
		time.Sleep(b.delay)
	}
	return nil
}
func (*motionPrivateBackend) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	return input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}, nil
}
func (*motionPrivateBackend) Close() error                                      { return nil }
func (*motionPrivateBackend) PointerLocation(context.Context) (int, int, error) { return 2, 3, nil }

type motionStepContext struct {
	context.Context
	calls     int
	trigger   int
	abort     bool
	onTrigger func()
}

func (c *motionStepContext) Err() error {
	c.calls++
	if c.calls == c.trigger {
		if c.onTrigger != nil {
			c.onTrigger()
		}
		if c.abort {
			return context.Canceled
		}
	}
	return nil
}
func (c *motionStepContext) Deadline() (time.Time, bool) { return time.Now().Add(time.Second), true }
func (c *motionStepContext) Done() <-chan struct{}       { return nil }

func TestMotionInternalAuthorityPerFrame(t *testing.T) {
	points := []motionPoint{{X: 0, Y: 0}, {X: 1, Y: 1}, {X: 2, Y: 2}}
	for _, tc := range []struct {
		name     string
		target   int
		close    bool
		duration time.Duration
	}{
		{"before-loop-cancel", 1, false, 0},
		{"before-loop-close", 1, true, 0},
		{"before-send-cancel", 2, false, 0},
		{"before-send-close", 2, true, 0},
		{"after-wait-cancel", 2, false, 80 * time.Millisecond},
		{"after-wait-close", 2, true, 80 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &motionPrivateBackend{}
			s := newSessionForTesting(nil, backend, nil, nil, nil)
			defer s.Close()
			ctx := &motionStepContext{Context: context.Background(), trigger: tc.target, abort: !tc.close}
			if tc.close {
				ctx.onTrigger = func() { _ = s.Close() }
			}
			if err := s.input.SendMotion(ctx, points, tc.duration, 0); err == nil {
				t.Fatalf("ignored authority loss: %+v", tc)
			}
		})
	}
	t.Run("skip-overdue", func(t *testing.T) {
		backend := &motionPrivateBackend{delay: 40 * time.Millisecond}
		s := newSessionForTesting(nil, backend, nil, nil, nil)
		defer s.Close()
		if err := s.input.SendMotion(context.Background(), points, 20*time.Millisecond, 0); err != nil {
			t.Fatal(err)
		}
		if backend.moves != 2 {
			t.Fatalf("overdue intermediate should be dropped, moves=%d", backend.moves)
		}
	})
	t.Run("cancel-during-wait", func(t *testing.T) {
		backend := &motionPrivateBackend{}
		s := newSessionForTesting(nil, backend, nil, nil, nil)
		defer s.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { time.Sleep(5 * time.Millisecond); cancel() }()
		err := s.input.SendMotion(ctx, points, 200*time.Millisecond, 1)
		if !errors.Is(err, context.Canceled) || backend.moves != 0 {
			t.Fatalf("wait cancellation err=%v moves=%d", err, backend.moves)
		}
	})
}

func TestMotionPrivateAdmissionAndPreflight(t *testing.T) {
	if err := input.ValidateMotionOptions(input.MotionOptions{Duration: -1}); err == nil {
		t.Fatal("invalid options allowed")
	}
	if err := input.ValidateMotionOptions(input.MotionOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		close bool
	}{
		{"cancel-before-drag", false}, {"close-before-drag", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &motionPrivateBackend{}
			s := newSessionForTesting(nil, backend, nil, nil, nil)
			defer s.Close()
			ctx := &motionStepContext{Context: context.Background(), trigger: 1, abort: !tc.close}
			if tc.close {
				ctx.onTrigger = func() { _ = s.Close() }
			}
			err := s.input.DragPoints(ctx, []motionPoint{{X: 1, Y: 1}, {X: 2, Y: 2}}, 0)
			if err == nil || backend.moves != 0 {
				t.Fatalf("admission err=%v moves=%d", err, backend.moves)
			}
		})
	}
	t.Run("move-closed", func(t *testing.T) {
		backend := &motionPrivateBackend{}
		s := newSessionForTesting(nil, backend, nil, nil, nil)
		_ = s.Close()
		err := s.input.MouseMoveSmooth(context.Background(), &image.Point{}, image.Pt(4, 4), input.MotionOptions{Duration: 20 * time.Millisecond})
		if !errors.Is(err, ErrSessionClosed) {
			t.Fatal(err)
		}
	})
	t.Run("move-canceled", func(t *testing.T) {
		backend := &motionPrivateBackend{}
		s := newSessionForTesting(nil, backend, nil, nil, nil)
		defer s.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := s.input.MouseMoveSmooth(ctx, &image.Point{}, image.Pt(4, 4), input.MotionOptions{Duration: 20 * time.Millisecond})
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	for _, tc := range []struct {
		name  string
		close bool
	}{
		{"cancel-after-path", false}, {"close-after-path", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &motionPrivateBackend{}
			s := newSessionForTesting(nil, backend, nil, nil, nil)
			defer s.Close()
			ctx := &motionStepContext{Context: context.Background(), trigger: 2, abort: !tc.close}
			if tc.close {
				ctx.onTrigger = func() { _ = s.Close() }
			}
			err := s.input.MouseMoveSmooth(ctx, &image.Point{}, image.Pt(4, 4), input.MotionOptions{Duration: 20 * time.Millisecond})
			if err == nil || backend.moves != 0 {
				t.Fatalf("authority err=%v moves=%d", err, backend.moves)
			}
		})
	}
}

func TestMotionLocationCapabilityRevokedBetweenChecks(t *testing.T) {
	backend := &motionPrivateBackend{}
	s := newSessionForTesting(nil, backend, nil, nil, nil)
	defer s.Close()
	ctx := &motionStepContext{Context: context.Background(), trigger: 1}
	ctx.onTrigger = func() { _ = s.Close() }
	err := s.input.MouseMoveSmooth(ctx, nil, image.Pt(4, 4), input.MotionOptions{Duration: 20 * time.Millisecond})
	if !errors.Is(err, ErrSessionClosed) || backend.moves != 0 {
		t.Fatalf("location admission err=%v moves=%d", err, backend.moves)
	}
}

// Verify the coordinate-space metadata does not silently grant location-read
// authority when only explicit-start movement is permitted.
func TestMotionNoPointerLocationPermission(t *testing.T) {
	backend := &motionPrivateBackend{}
	session := newSessionForTesting(nil, backend, nil, nil, nil)
	defer session.Close()
	operations := session.inputAdmission.resolvedOperations[:0]
	for _, name := range session.inputAdmission.resolvedOperations {
		if name != "pointer-location" {
			operations = append(operations, name)
		}
	}
	session.inputAdmission.resolvedOperations = operations
	err := session.input.MouseMoveSmooth(context.Background(), nil, image.Pt(4, 4), input.MotionOptions{Duration: 20 * time.Millisecond})
	if !errors.Is(err, ErrUnsupported) || backend.moves != 0 {
		t.Fatalf("location authority err=%v moves=%d", err, backend.moves)
	}
}

// Context and Session authority can be lost after non-mutating preflight and
// before the first event. Exercise all context-read boundaries rather than
// timing cancellation using wall clocks.
func TestMotionPreflightHandoffAuthority(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		for trigger := 1; trigger <= 35; trigger++ {
			backend := &motionPrivateBackend{}
			session := newSessionForTesting(nil, backend, nil, nil, nil)
			ctx := &motionStepContext{Context: context.Background(), trigger: trigger, abort: !closeSession}
			if closeSession {
				ctx.onTrigger = func() { _ = session.Close() }
			}
			_ = session.input.MouseMoveSmooth(ctx, &image.Point{}, image.Pt(5, 5), input.MotionOptions{Duration: 20 * time.Millisecond})
			_ = session.Close()
		}
	}
}

type observationFixture struct {
	*closeCountingInput
	result input.PointerObservation
	after  func()
	err    error
}

func (f *observationFixture) ObservePointer(context.Context) (input.PointerObservation, error) {
	if f.after != nil {
		f.after()
	}
	return f.result, f.err
}
func TestObservePointerContract(t *testing.T) {
	for _, mode := range []string{"origin", "negative", "unsupported", "invalid-scale", "cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &observationFixture{closeCountingInput: &closeCountingInput{}, result: input.PointerObservation{Provider: "synthetic", ObservedAt: time.Now(), CoordinateSpace: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}}
			s := newSessionForTesting(nil, f, nil, nil, nil)
			defer s.Close()
			var want error
			switch mode {
			case "negative":
				f.result.X = -17
			case "unsupported":
				f.err = input.ErrNotSupported
				want = ErrUnsupported
			case "invalid-scale":
				f.result.CoordinateSpace.ScaleX = 0
			case "cancel":
				f.after = cancel
				want = context.Canceled
			case "close":
				f.after = func() { _ = s.Close() }
				want = ErrSessionClosed
			}
			got, err := s.input.ObservePointer(ctx)
			if want != nil || mode == "invalid-scale" {
				if err == nil || (want != nil && !errors.Is(err, want)) || got != (input.PointerObservation{}) {
					t.Fatalf("got %+v, %v; want %v", got, err, want)
				}
			} else if err != nil || got != f.result {
				t.Fatalf("got %+v, %v; want %+v", got, err, f.result)
			}
		})
	}
}

func benchmarkInputReceiptBundle() *inputState {
	s := &sessionCore{
		ctx: context.Background(),
		capabilities: map[Capability]CapabilityStatus{
			CapabilityInput: {
				Capability:  CapabilityInput,
				Available:   true,
				Backend:     "*input.WlVirtualBackend",
				Operations:  []string{"keyboard", "click", "pointer", "scroll", "sync", "pointer-location", "pointer-coordinate-space", "drag"},
				Diagnostics: []string{"wayland", "screen", "keyboard", "pointer"},
			},
		},
	}
	base := s.bundleBase(CapabilityInput)
	return newInputState(&base, nil)
}

func BenchmarkSyntheticInputReceiptRecordingWithSession(b *testing.B) {
	input := benchmarkInputReceiptBundle()
	start := time.Now()
	for i := 0; i < inputReceiptHistoryLimit; i++ {
		_ = input.RecordInputReceipt("type", start, nil)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := input.RecordInputReceipt("type", start, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSyntheticInputReceiptDiagnosticSnapshot(b *testing.B) {
	input := benchmarkInputReceiptBundle()
	start := time.Now()
	for i := 0; i < inputReceiptHistoryLimit; i++ {
		_ = input.RecordInputReceipt("type", start, nil)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		receipts := input.RecentInputReceipts()
		if len(receipts) != inputReceiptHistoryLimit || receipts[0].Mechanism != "*input.WlVirtualBackend" {
			b.Fatal("receipt snapshot lost backend identity")
		}
	}
}

// A diagnostic snapshot must preserve order, backend identity and isolation,
// even though normal input actions should not have to resolve that identity.
func TestInputReceiptMechanismOnDiagnosticSnapshot(t *testing.T) {
	input := benchmarkInputReceiptBundle()
	start := time.Now()
	for i := 0; i < inputReceiptHistoryLimit+7; i++ {
		_ = input.RecordInputReceipt("type", start, nil)
	}
	receipts := input.RecentInputReceipts()
	if len(receipts) != inputReceiptHistoryLimit {
		t.Fatalf("count=%d", len(receipts))
	}
	for i, receipt := range receipts {
		wantSeq := uint64(i + 8)
		if receipt.Sequence != wantSeq || receipt.Mechanism != "*input.WlVirtualBackend" {
			t.Fatalf("receipt[%d]=%+v want seq=%d and backend", i, receipt, wantSeq)
		}
	}
	receipts[0].Mechanism = "mutated"
	if input.RecentInputReceipts()[0].Mechanism == "mutated" {
		t.Fatal("diagnostic receipt snapshot aliases retained history")
	}
}

type scopedObservationBackendProbe struct {
	fakeObservationBackend
	ctx   context.Context
	block bool
}

func (b *scopedObservationBackendProbe) Listen(ctx context.Context, filter input.ObservationFilter) (input.ObservationSubscription, error) {
	b.ctx = ctx
	if b.block {
		<-ctx.Done()
	}
	return b.fakeObservationBackend.Listen(ctx, filter)
}

func TestCallerObservationSetupDeadlineAndLifetime(t *testing.T) {
	for _, block := range []bool{true, false} {
		t.Run(map[bool]string{true: "timed-out-setup", false: "established-subscription"}[block], func(t *testing.T) {
			b := &scopedObservationBackendProbe{block: block}
			s := wrapSession(setupObservationTestSession(b))
			defer s.Close()
			sub, err := s.InputObservation().WithTimeout(30 * time.Millisecond).Listen(input.ObservationFilter{Combo: "F8"})
			if block {
				if sub != nil || !errors.Is(err, context.DeadlineExceeded) || b.sub == nil || !b.sub.closed {
					t.Fatalf("setup result %v %v backend=%+v", sub, err, b.sub)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-b.ctx.Done():
				t.Fatal("established subscription expired")
			case <-time.After(60 * time.Millisecond):
			}
			if err := sub.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-b.ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("close did not release scoped context")
			}
			if !b.sub.closed {
				t.Fatal("provider subscription leaked")
			}
		})
	}
}

type plan6MalformedObserver struct {
	fakeObservationBackend
	result  input.ObservationSubscription
	failure error
}

func (b *plan6MalformedObserver) Listen(context.Context, input.ObservationFilter) (input.ObservationSubscription, error) {
	return b.result, b.failure
}

type plan6MalformedSub struct {
	events chan input.ObservationEvent
	closed int
}

func (s *plan6MalformedSub) Events() <-chan input.ObservationEvent { return s.events }
func (s *plan6MalformedSub) Err() error                            { return nil }
func (s *plan6MalformedSub) Close() error                          { s.closed++; return nil }

func TestPlan6MalformedObservationResults(t *testing.T) {
	var typedNil *plan6MalformedSub
	failure := errors.New("setup failed")
	for _, tc := range []struct {
		name   string
		sub    input.ObservationSubscription
		err    error
		want   error
		closed int
	}{
		{"nil", nil, nil, ErrUnavailable, 0},
		{"typed nil", typedNil, nil, ErrUnavailable, 0},
		{"nil events", &plan6MalformedSub{}, nil, ErrUnavailable, 1},
		{"result plus error", &plan6MalformedSub{events: make(chan input.ObservationEvent)}, failure, failure, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &plan6MalformedObserver{result: tc.sub, failure: tc.err}
			s := wrapSession(setupObservationTestSession(b))
			defer s.Close()
			result, err := s.InputObservation().Listen(input.ObservationFilter{Combo: "F8"})
			if result != nil || !errors.Is(err, tc.want) {
				t.Fatalf("result=%v err=%v", result, err)
			}
			if sub, ok := tc.sub.(*plan6MalformedSub); ok && sub != nil && sub.closed != tc.closed {
				t.Fatalf("closed=%d want=%d", sub.closed, tc.closed)
			}
		})
	}
}

type plan6TerminalSub struct {
	plan6MalformedSub
	done chan struct{}
}

func (s *plan6TerminalSub) Done() <-chan struct{} { return s.done }

func TestPlan6ObservationTerminalReleaseAndRepeatedClose(t *testing.T) {
	for _, natural := range []bool{true, false} {
		t.Run(map[bool]string{true: "provider termination", false: "explicit close"}[natural], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sub := &plan6TerminalSub{plan6MalformedSub: plan6MalformedSub{events: make(chan input.ObservationEvent, 1)}, done: make(chan struct{})}
			wrapped := newScopedObservation(sub, streamSetup{ctx: ctx, release: cancel})
			// A terminal watcher must not compete with the event consumer.
			sub.events <- input.ObservationEvent{}
			if natural {
				close(sub.done)
			} else {
				if err := wrapped.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("scope not released")
			}
			select {
			case <-wrapped.Events():
			default:
				t.Fatal("terminal watcher consumed the event")
			}
			for range 2 {
				if err := wrapped.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if sub.closed != 1 {
				t.Fatalf("provider closed %d times", sub.closed)
			}
		})
	}
}
