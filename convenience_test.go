package perfuncted

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
)

func convenienceSession(t *testing.T, backend input.Inputter) *Session {
	t.Helper()
	preserveOpeners(t)
	openInput = func(context.Context, env.Runtime, int32, int32) (input.Inputter, error) { return backend, nil }
	s, err := OpenDefault()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func TestConvenienceBatchOrderAndPreflight(t *testing.T) {
	backend := &convenienceInput{}
	s := convenienceSession(t, backend)
	if err := s.Do(Type("{ctrl+s}+é"), Key("tab"), Hotkey("ctrl", "s")); err != nil {
		t.Fatal(err)
	}
	want := []string{"type-literal:{ctrl+s}+é", "type:{tab}", "type:{ctrl+s}"}
	if !reflect.DeepEqual(backend.Calls, want) {
		t.Fatalf("calls = %v", backend.Calls)
	}
	backend.Calls = nil
	if err := s.Do(Type("first"), Key("x}{enter")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid key: %v", err)
	}
	if len(backend.Calls) != 0 {
		t.Fatal("preflight dispatched input")
	}
}
func TestConvenienceBatchFailure(t *testing.T) {
	cause := errors.New("private value")
	backend := &convenienceInput{Err: cause}
	s := convenienceSession(t, backend)
	err := s.Do(Type("first"), Type("second"))
	var stepErr *StepError
	if !errors.Is(err, cause) || !errors.As(err, &stepErr) || stepErr.Index != 0 || len(backend.Calls) != 1 {
		t.Fatalf("error %v, calls %v", err, backend.Calls)
	}
	if strings.Contains(err.Error(), "private value") {
		t.Fatal("step error exposed backend details")
	}
}
func TestConvenienceContextAndCapabilities(t *testing.T) {
	backend := &convenienceInput{}
	s := convenienceSession(t, backend)
	if !s.Capability(CapabilityInput).Required || s.Capability(CapabilityWindows).Requested {
		t.Fatal("unexpected capability policy")
	}
	for _, status := range s.Capabilities() {
		if status.Capability != CapabilityInput && status.Requested {
			t.Fatalf("unexpected capability request: %s", status.Capability)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.WithContext(ctx).Do(Type("secret")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(backend.Calls) != 0 {
		t.Fatal("canceled batch dispatched")
	}
	if err := s.WithContext(nil).Do(); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
}
func TestOpenDefaultOptionalInput(t *testing.T) {
	preserveOpeners(t)
	openInput = func(context.Context, env.Runtime, int32, int32) (input.Inputter, error) {
		return nil, errors.New("unavailable")
	}
	s, err := OpenDefault(Optional(CapabilityInput))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Capability(CapabilityInput).Required || s.Has(CapabilityInput) {
		t.Fatal("optional input policy lost")
	}
}

type convenienceInput struct {
	closeCountingInput
	Calls []string
	Err   error
}

func (b *convenienceInput) Type(_ context.Context, s string) error {
	b.Calls = append(b.Calls, "type:"+s)
	return b.Err
}
func (b *convenienceInput) TypeLiteral(_ context.Context, s string) error {
	b.Calls = append(b.Calls, "type-literal:"+s)
	return b.Err
}

func TestConvenienceCloseCancelsInFlightAndStopsBatch(t *testing.T) {
	backend := &blockingConvenienceInput{started: make(chan struct{})}
	s := convenienceSession(t, backend)
	done := make(chan error, 1)
	go func() { done <- s.Do(Type("first"), Type("second")) }()
	<-backend.started
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("close result: %v", err)
	}
	if backend.count != 1 {
		t.Fatalf("input calls = %d", backend.count)
	}
}

type blockingConvenienceInput struct {
	closeCountingInput
	started chan struct{}
	count   int
}

func (b *blockingConvenienceInput) TypeLiteral(ctx context.Context, _ string) error {
	b.count++
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestConvenienceMalformedSequencePreflight(t *testing.T) {
	backend := &convenienceInput{}
	s := convenienceSession(t, backend)
	if err := s.Do(Type("first"), TypeKeys("{unfinished")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if len(backend.Calls) != 0 {
		t.Fatal("invalid sequence dispatched input")
	}
}

func TestOpenDefaultPortalDenialDoesNotFallback(t *testing.T) {
	preserveOpeners(t)
	previous := openRemoteDesktopInput
	t.Cleanup(func() { openRemoteDesktopInput = previous })
	fallback := 0
	openInput = func(context.Context, env.Runtime, int32, int32) (input.Inputter, error) {
		fallback++
		return nil, errors.New("unexpected fallback")
	}
	openRemoteDesktopInput = func(context.Context, env.Runtime, input.RemoteDesktopOptions) (input.Inputter, error) {
		return nil, input.ErrRemoteDesktopDenied
	}
	_, err := OpenDefault(WithRemoteDesktopInput(input.RemoteDesktopOptions{}))
	if !errors.Is(err, input.ErrRemoteDesktopDenied) || fallback != 0 {
		t.Fatalf("denial = %v, fallback calls = %d", err, fallback)
	}
}

func TestConvenienceRejectsModifierOnlyChord(t *testing.T) {
	backend := &convenienceInput{}
	s := convenienceSession(t, backend)
	if err := s.Do(Type("first"), Hotkey("ctrl", "shift")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if len(backend.Calls) != 0 {
		t.Fatal("invalid chord dispatched input")
	}
}

func TestConvenienceStopsAfterLateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &cancelConvenienceInput{cancel: cancel}
	s := convenienceSession(t, backend)
	err := s.WithContext(ctx).Do(Type("first"), Type("second"))
	if !errors.Is(err, context.Canceled) || backend.count != 1 {
		t.Fatalf("cancellation = %v, calls = %d", err, backend.count)
	}
}

type cancelConvenienceInput struct {
	closeCountingInput
	cancel context.CancelFunc
	count  int
}

func (b *cancelConvenienceInput) TypeLiteral(context.Context, string) error {
	b.count++
	b.cancel()
	return nil
}

func TestConvenienceViewBoundsAndNil(t *testing.T) {
	var session *Session
	var view *SessionView
	if err := session.Do(); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil session: %v", err)
	}
	if err := view.Do(); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil view: %v", err)
	}
	backend := &blockingConvenienceInput{started: make(chan struct{})}
	s := convenienceSession(t, backend)
	if err := s.Do(); err != nil {
		t.Fatal(err)
	}
	err := s.WithTimeout(time.Millisecond).Do(Type("first"), Type("second"))
	if !errors.Is(err, context.DeadlineExceeded) || backend.count > 1 {
		t.Fatalf("timeout: %v calls: %d", err, backend.count)
	}
}

func TestConvenienceUnknownKeyPreflight(t *testing.T) {
	backend := &convenienceInput{}
	s := convenienceSession(t, backend)
	err := s.Do(Type("first"), Key("this_key_does_not_exist"))
	if !errors.Is(err, ErrInvalidArgument) || len(backend.Calls) != 0 {
		t.Fatalf("unknown key: %v; calls: %d", err, len(backend.Calls))
	}
}

func TestConvenienceAcceptsCommonKeys(t *testing.T) {
	backend := &convenienceInput{}
	s := convenienceSession(t, backend)
	for _, name := range []string{"backspace", "home", "control_l"} {
		if err := s.Do(Key(name)); err != nil {
			t.Fatalf("key %q: %v", name, err)
		}
	}
	if len(backend.Calls) != 3 {
		t.Fatalf("input calls = %d", len(backend.Calls))
	}
}

func TestConvenienceReusedStepsAndIndependentViews(t *testing.T) {
	s := NewSessionForTesting(nil, &closeCountingInput{}, nil, nil, nil)
	defer s.Close()
	step := Type("value")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 2)
	go func() { done <- s.WithContext(canceled).Do(step) }() //nolint:contextcheck // The view carries the canceled context.
	go func() { done <- s.WithContext(context.Background()).Do(step, step) }()
	first, second := <-done, <-done
	if !(errors.Is(first, context.Canceled) && second == nil || errors.Is(second, context.Canceled) && first == nil) {
		t.Fatalf("views: %v, %v", first, second)
	}
}

func TestConvenienceMissingInputPreflight(t *testing.T) {
	s := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer s.Close()
	err := s.Do(Delay(0), Click(1, 2))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing input: %v", err)
	}
}

func TestConvenienceDelayCancellationStopsInput(t *testing.T) {
	backend := &convenienceInput{}
	s := convenienceSession(t, backend)
	err := s.WithTimeout(time.Millisecond).Do(Delay(time.Hour), Type("must not be sent"))
	if !errors.Is(err, context.DeadlineExceeded) || len(backend.Calls) != 0 {
		t.Fatalf("delay cancellation: %v; calls: %d", err, len(backend.Calls))
	}
}

type keyboardOnlyConvenienceInput struct{ convenienceInput }

func (*keyboardOnlyConvenienceInput) SupportedOperations() []string { return []string{"keyboard"} }

func TestConvenienceUnsupportedClickPreflight(t *testing.T) {
	backend := &keyboardOnlyConvenienceInput{}
	s := convenienceSession(t, backend)
	err := s.Do(Type("first"), Click(1, 2))
	if !errors.Is(err, ErrUnsupported) || len(backend.Calls) != 0 {
		t.Fatalf("unsupported click: %v; calls: %d", err, len(backend.Calls))
	}
}

func TestConvenienceViewCancelsInFlight(t *testing.T) {
	backend := &blockingConvenienceInput{started: make(chan struct{})}
	s := convenienceSession(t, backend)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.WithContext(ctx).Do(Type("first"), Type("second")) }() //nolint:contextcheck // The view carries the caller context.
	<-backend.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancellation: %v", err)
	}
	if backend.count != 1 {
		t.Fatalf("input calls = %d", backend.count)
	}
}

type deadlineConvenienceInput struct {
	closeCountingInput
	deadline time.Time
	ok       bool
}

func (b *deadlineConvenienceInput) TypeLiteral(ctx context.Context, _ string) error {
	b.deadline, b.ok = ctx.Deadline()
	return nil
}

func TestConvenienceViewPassesDeadlineToBackend(t *testing.T) {
	backend := &deadlineConvenienceInput{}
	s := convenienceSession(t, backend)
	before := time.Now()
	if err := s.WithTimeout(time.Second).Do(Type("first")); err != nil {
		t.Fatal(err)
	}
	if !backend.ok || backend.deadline.Before(before) || backend.deadline.After(time.Now().Add(time.Second)) {
		t.Fatal("backend did not receive view deadline")
	}
}

func TestConvenienceOversizedKeyPreflightDoesNotDispatch(t *testing.T) {
	backend := &convenienceInput{}
	s := convenienceSession(t, backend)
	err := s.Do(Type("earlier valid input"), TypeKeys("{"+strings.Repeat("x", 2*1024*1024)+"}"))
	if !errors.Is(err, ErrInvalidArgument) || len(backend.Calls) != 0 {
		t.Fatalf("oversized key: %v, calls = %d", err, len(backend.Calls))
	}
}

func TestConvenienceOversizedPortableKeyAndChordDoNotDispatch(t *testing.T) {
	backend := &convenienceInput{}
	s := convenienceSession(t, backend)
	for _, invalid := range []Step{
		Key(strings.Repeat("a", 1<<20)),
		Hotkey("ctrl", strings.Repeat("b", 1<<20)),
		Hotkey("ctrl", "alt", "shift", "super", "ctrl", "s"),
	} {
		if err := s.Do(Type("must not send"), invalid); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("oversized key or chord was accepted: %v", err)
		}
		if len(backend.Calls) != 0 {
			t.Fatalf("invalid key dispatched: %d input calls", len(backend.Calls))
		}
	}
}
