package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"github.com/nskaggs/perfuncted/v2/window"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func BenchmarkConvenienceBatch(b *testing.B) {
	s := newSessionForTesting(nil, &closeCountingInput{}, nil, nil, nil)
	defer s.Close()
	steps := make([]Step, 10)
	for i := range steps {
		steps[i] = Type("example")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Do(steps...); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConvenienceDirect(b *testing.B) {
	s := newSessionForTesting(nil, &closeCountingInput{}, nil, nil, nil)
	defer s.Close()
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 10; j++ {
			if err := s.input.TypeLiteral(ctx, "example"); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func TestConvenienceCoverageOpenAndViewCopies(t *testing.T) {
	sentinel := errors.New("reject requested option")
	if _, err := openDefault(func(*openConfig) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("option error: %v", err)
	}
	s := convenienceSession(t, &convenienceInput{})
	var nilView *Session
	if nilView.WithContext(context.Background()).core != nil {
		t.Fatal("nil receiver acquired a session")
	}
	if nilView.WithTimeout(time.Second).core != nil {
		t.Fatal("nil receiver acquired a session")
	}
	parent := s.WithContext(context.Background()).WithTimeout(time.Second)
	copyWithContext := parent.WithContext(context.Background())
	copyWithTimeout := parent.WithTimeout(2 * time.Second)
	if parent.scope.timeout != time.Second || copyWithContext.scope.timeout != time.Second ||
		copyWithTimeout.scope.timeout != 2*time.Second || parent.core != s || copyWithTimeout.core != s {
		t.Fatal("immutable views changed parent or lost owner")
	}
	for _, err := range []error{s.Type("literal"), s.Key("tab"), parent.Type("view"), parent.Key("enter")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.WithTimeout(-1).Do(Type("private")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative timeout: %v", err)
	}
	if err := s.WithContext(nil).WithTimeout(time.Second).Do(Type("private")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil derived context: %v", err)
	}
}

func TestConvenienceCoverageStepErrors(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("private"), "operation failed"},
		{ErrInvalidArgument, "invalid argument"},
		{ErrUnavailable, "capability unavailable"},
		{ErrUnsupported, "operation unsupported"},
		{ErrSessionClosed, "session closed"},
		{ErrWindowFocusUnverified, "window focus unverified"},
		{ErrWindowIdentityChanged, "selected window changed"},
		{context.Canceled, "canceled"},
		{context.DeadlineExceeded, "deadline exceeded"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			wrapped := &StepError{Index: 1, Operation: "type-literal", Err: tc.err, MayHaveDispatched: true}
			msg := wrapped.Error()
			if !strings.Contains(msg, tc.want) || !strings.Contains(msg, "step 2") ||
				!strings.Contains(msg, "may have been dispatched") || !errors.Is(wrapped, tc.err) {
				t.Fatalf("message=%q unwrapped=%v", msg, wrapped.Unwrap())
			}
		})
	}
}

func TestConvenienceCoverageStepPreflight(t *testing.T) {
	ctx := context.Background()
	good := []Step{Type(""), TypeKeys("{enter}"), Key("tab"), Hotkey("ctrl", "s"), Click(1, 2), Delay(0)}
	for _, step := range good {
		if err := step.validate(ctx); err != nil {
			t.Fatalf("valid step %s: %v", step.kind, err)
		}
	}
	bad := []Step{
		{}, TypeKeys("{unfinished"), Key("no-such-key"), Hotkey("ctrl"), Hotkey("ctrl", "unknown"),
		Hotkey("not-a-modifier", "s"), Hotkey(strings.Repeat("ctrl", 70), "s"),
		Delay(-time.Second),
	}
	for _, step := range bad {
		if err := step.validate(ctx); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid step %s returned %v", step.kind, err)
		}
	}
	// The parser must not proceed after a cancellation, even with valid keys.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := validateHotkey(canceled, "ctrl+s"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("canceled parsing: %v", err)
	}
}

func TestConvenienceCoverageRunStepKindsAndDelay(t *testing.T) {
	s := convenienceSession(t, &convenienceInput{})
	ctx := context.Background()
	for _, step := range []Step{
		Type("literal"), TypeKeys("{tab}"), Key("enter"), Hotkey("ctrl", "s"),
		Click(1, 2), Delay(3 * time.Millisecond),
	} {
		if err := s.runStep(ctx, step); err != nil {
			t.Fatalf("run %s: %v", step.kind, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.runStep(canceled, Delay(time.Second)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellable delay: %v", err)
	}
}

func TestConvenienceCoveragePreflightAndDirectGuards(t *testing.T) {
	s := convenienceSession(t, &convenienceInput{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.preflightSteps(ctx, []Step{Type("text")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preflight: %v", err)
	}
	if err := s.runStepsChecked(ctx, []Step{Type("text")}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled execution: %v", err)
	}
	if err := s.runStepsChecked(context.Background(), []Step{Type("text")},
		func(context.Context) error { return ErrWindowFocusUnverified }); !errors.Is(err, ErrWindowFocusUnverified) {
		t.Fatalf("guard not applied: %v", err)
	}
	if err := s.preflightSteps(context.Background(), []Step{Type("text")}); err != nil {
		t.Fatalf("normal preflight: %v", err)
	}
}

type cancelOnNthErrContext struct {
	context.Context
	n     atomic.Int32
	index int32
}

func (c *cancelOnNthErrContext) Err() error {
	if c.n.Add(1) >= c.index {
		return context.Canceled
	}
	return nil
}

// These assertions exercise the authority boundary between validation and
// input, where a caller can cancel or a session can close.
func TestConvenienceCoverageIntraPreflightRevocation(t *testing.T) {
	s := convenienceSession(t, &convenienceInput{})
	ctx := &cancelOnNthErrContext{Context: context.Background(), index: 2}
	if err := s.preflightSteps(ctx, []Step{Type("private")}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel between validation and dispatch: %v", err)
	}
	original := s.input
	s.input = nil
	err := s.preflightSteps(context.Background(), []Step{Type("private")})
	s.input = original
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing Input facade: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.runStepsChecked(context.Background(), []Step{Type("private")}, nil); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed execution: %v", err)
	}
}

func convenienceSession(t *testing.T, backend input.Inputter) *sessionCore {
	t.Helper()
	preserveOpeners(t)
	openInput = func(context.Context, env.Runtime, int32, int32) (input.Inputter, error) { return backend, nil }
	s, err := openDefault()
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
	s, err := openDefault(Optional(CapabilityInput))
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
	_, err := openDefault(WithRemoteDesktopInput(input.RemoteDesktopOptions{}))
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
	var session *sessionCore
	var view *Session
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
	s := newSessionForTesting(nil, &closeCountingInput{}, nil, nil, nil)
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
	s := newSessionForTesting(nil, nil, nil, nil, nil)
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

type scriptedFocusManager struct {
	*focusTestWindowManager
	infoReads   atomic.Int32
	failAt      int32
	switchAt    int32
	activateErr error
}

func (m *scriptedFocusManager) InfoByID(ctx context.Context, id string) (window.Info, error) {
	read := m.infoReads.Add(1)
	if read == m.failAt {
		return window.Info{}, ErrInvalidArgument
	}
	if read == m.switchAt {
		m.mu.Lock()
		for i := range m.windows {
			if m.windows[i].StableID() == id {
				m.windows[i].PID++
			}
		}
		m.mu.Unlock()
	}
	return m.focusTestWindowManager.InfoByID(ctx, id)
}

func (m *scriptedFocusManager) ActivateByID(ctx context.Context, id string) error {
	if m.activateErr != nil {
		return m.activateErr
	}
	return m.focusTestWindowManager.ActivateByID(ctx, id)
}

func scriptedWindowSession(t *testing.T) (*sessionCore, *scriptedFocusManager, *windowInputSpy) {
	t.Helper()
	manager := &scriptedFocusManager{focusTestWindowManager: &focusTestWindowManager{
		handleWindowManager: newHandleWindowManager(window.Info{
			NativeID: "1", Title: "Editor", PID: 120, AppID: "org.example.Editor",
		}),
	}}
	backend := &windowInputSpy{}
	s := newSessionForTesting(nil, backend, manager, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	return s, manager, backend
}

func TestWindowTargetCoverageEarlyReturnPaths(t *testing.T) {
	t.Run("nil view", func(t *testing.T) {
		target := &WindowTarget{}
		if err := target.Do(Type("x")); !errors.Is(err, ErrNilSession) {
			t.Fatal(err)
		}
	})
	t.Run("missing capability", func(t *testing.T) {
		s, _, backend := scriptedWindowSession(t)
		original := s.windows.backend()
		s.windows.install(nil)
		t.Cleanup(func() { s.windows.install(original) })
		if err := s.Window("Editor").Do(Type("text")); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("missing window backend: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("input sent without window capability")
		}
	})
	t.Run("missing title", func(t *testing.T) {
		s, _, backend := scriptedWindowSession(t)
		if err := s.Window("Not this window").Do(Type("text")); !errors.Is(err, ErrWindowNotFound) {
			t.Fatalf("window not found: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("input dispatched for missing title")
		}
	})
	t.Run("empty batch", func(t *testing.T) {
		s, manager, _ := scriptedWindowSession(t)
		if err := s.Window("Editor").Do(); err != nil {
			t.Fatal(err)
		}
		if len(manager.actions) != 0 {
			t.Fatal("empty batch activated window")
		}
	})
	t.Run("initial info failure", func(t *testing.T) {
		s, manager, backend := scriptedWindowSession(t)
		manager.failAt = 1
		if err := s.Window("Editor").Do(Type("text")); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("initial window info error: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("dispatched after initial info error")
		}
	})
	t.Run("activation failure", func(t *testing.T) {
		s, manager, backend := scriptedWindowSession(t)
		manager.activateErr = ErrUnsupported
		if err := s.Window("Editor").Do(Type("text")); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("activation error: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("dispatched after activation error")
		}
	})
	t.Run("wait observer error", func(t *testing.T) {
		s, manager, backend := scriptedWindowSession(t)
		manager.failAt = 2
		if err := s.Window("Editor").Do(Type("text")); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("wait observation error: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("dispatched after unreadable focus observation")
		}
	})
	t.Run("focus identity changes during activation", func(t *testing.T) {
		s, manager, backend := scriptedWindowSession(t)
		manager.switchAt = 2
		if err := s.Window("Editor").Do(Type("text")); !errors.Is(err, ErrWindowIdentityChanged) {
			t.Fatalf("identity changed during wait: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("dispatched to replaced window")
		}
	})
	t.Run("guard observation fails", func(t *testing.T) {
		s, manager, backend := scriptedWindowSession(t)
		manager.failAt = 3
		if err := s.Window("Editor").Do(Type("text")); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("guard info error: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("dispatched after pre-input observation failure")
		}
	})
}

type cancelWaitWindowManager struct {
	*focusTestWindowManager
	reads  int
	cancel context.CancelFunc
}

func (m *cancelWaitWindowManager) InfoByID(ctx context.Context, id string) (window.Info, error) {
	info, err := m.focusTestWindowManager.InfoByID(ctx, id)
	m.reads++
	if m.reads == 2 {
		m.cancel()
	}
	return info, err
}

// Explicit caller cancellation during the focus observation must propagate
// unchanged; it is not an activation timeout and must never send a keystroke.
func TestWindowTargetCoverageCanceledDuringFocusWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &cancelWaitWindowManager{focusTestWindowManager: &focusTestWindowManager{
		handleWindowManager: newHandleWindowManager(window.Info{
			NativeID: "1", Title: "Editor", PID: 12, AppID: "org.example.Editor",
		}),
	}, cancel: cancel}
	backend := &windowInputSpy{}
	s := newSessionForTesting(nil, backend, manager, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	err := s.WithContext(ctx).Window("Editor").Do(Type("private"))
	if !errors.Is(err, context.Canceled) || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("canceled focus wait = %v, input = %v", err, backend.callsSnapshot())
	}
}

// Activation can be accepted but never observed. A shorter internal focus
// deadline, rather than caller cancellation, must return the focus-specific
// uncertainty signal and must not inject any input.
func TestWindowTargetCoverageInternalFocusTimeout(t *testing.T) {
	manager := newHandleWindowManager(window.Info{
		NativeID: "1", Title: "Editor", PID: 42, AppID: "org.example.Editor",
	})
	backend := &windowInputSpy{}
	session := newSessionForTesting(nil, backend, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	session.config.Timeouts = TimeoutPolicy{Short: 15 * time.Millisecond, Poll: time.Millisecond}
	err := session.Window("Editor").Do(Type("private"))
	if !errors.Is(err, ErrWindowFocusUnverified) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("focus timeout: %v", err)
	}
	if len(backend.callsSnapshot()) != 0 {
		t.Fatal("input dispatched despite unobserved focus")
	}
}

// focusTestWindowManager models a compositor that observes focus only after
// ActivateByID, rather than treating the activation request as proof of focus.
type focusTestWindowManager struct {
	*handleWindowManager
}

func (m *focusTestWindowManager) ActivateByID(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actions = append(m.actions, "activate:"+id)
	for i := range m.windows {
		m.windows[i].Active = m.windows[i].StableID() == id
	}
	return nil
}

type windowInputSpy struct {
	closeCountingInput
	mu    sync.Mutex
	calls []string
	after func()
}

func (b *windowInputSpy) TypeLiteral(_ context.Context, value string) error {
	b.mu.Lock()
	b.calls = append(b.calls, "literal:"+value)
	b.mu.Unlock()
	if b.after != nil {
		b.after()
	}
	return nil
}

func (b *windowInputSpy) Type(_ context.Context, value string) error {
	b.mu.Lock()
	b.calls = append(b.calls, "keys:"+value)
	b.mu.Unlock()
	if b.after != nil {
		b.after()
	}
	return nil
}

func (b *windowInputSpy) callsSnapshot() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.calls...)
}

func windowConvenienceFixture(t *testing.T, info ...window.Info) (*sessionCore, *focusTestWindowManager, *windowInputSpy) {
	t.Helper()
	manager := &focusTestWindowManager{handleWindowManager: newHandleWindowManager(info...)}
	backend := &windowInputSpy{}
	session := newSessionForTesting(nil, input.Inputter(backend), manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	return session, manager, backend
}

func TestWindowTargetActivatesAndChecksEachInput(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "office-1", Title: "Journal", PID: 42, AppID: "editor", Active: false,
	})
	if err := session.Window("Journal").Do(Type("text {ctrl+s}+é"), Key("tab")); err != nil {
		t.Fatal(err)
	}
	if got := backend.callsSnapshot(); !reflect.DeepEqual(got, []string{"literal:text {ctrl+s}+é", "keys:{tab}"}) {
		t.Fatalf("dispatches = %v", got)
	}
	if got := manager.actions; !reflect.DeepEqual(got, []string{"activate:office-1"}) {
		t.Fatalf("activations = %v", got)
	}
}

func TestWindowTargetPreflightRejectsLateInvalidStepBeforeActivation(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "office-1", Title: "Journal", PID: 42, AppID: "editor",
	})
	err := session.Window("Journal").Do(Type("secret"), Key("x}{ctrl+a"))
	if !errors.Is(err, ErrInvalidArgument) || len(manager.actions) != 0 || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("preflight err=%v activations=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

func TestWindowTargetRejectsAmbiguousTitleWithoutInput(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t,
		window.Info{NativeID: "1", Title: "Journal", PID: 42, AppID: "editor"},
		window.Info{NativeID: "2", Title: "Journal", PID: 43, AppID: "editor"},
	)
	err := session.Window("Journal").Do(Type("private"))
	if !errors.Is(err, ErrWindowAmbiguous) || len(manager.actions) != 0 || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("ambiguous target err=%v activations=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

func TestWindowTargetFailsBeforeInputIfIdentityUnverifiable(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "1", Title: "Journal",
	})
	err := session.Window("Journal").Do(Type("private"))
	if !errors.Is(err, ErrUnsupported) || len(manager.actions) != 0 || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("unverifiable target err=%v activations=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

func TestWindowTargetStopsOnFocusLossWithoutReactivation(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "1", Title: "Journal", PID: 42, AppID: "editor",
	})
	backend.after = func() {
		manager.mu.Lock()
		manager.windows[0].Active = false
		manager.mu.Unlock()
	}
	err := session.Window("Journal").Do(Type("first"), Type("second"))
	var stepErr *StepError
	if !errors.Is(err, ErrWindowFocusUnverified) || !errors.As(err, &stepErr) ||
		stepErr.Index != 1 || stepErr.MayHaveDispatched ||
		len(manager.actions) != 1 || !reflect.DeepEqual(backend.callsSnapshot(), []string{"literal:first"}) {
		t.Fatalf("focus loss err=%v activations=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

func TestWindowTargetDoesNotRetargetRecycledIDWithChangedIdentity(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "1", Title: "Journal", PID: 42, AppID: "editor",
	})
	backend.after = func() {
		manager.mu.Lock()
		manager.windows[0].PID = 99
		manager.mu.Unlock()
	}
	err := session.Window("Journal").Do(Type("first"), Type("second"))
	if !errors.Is(err, ErrWindowIdentityChanged) || len(manager.actions) != 1 ||
		!reflect.DeepEqual(backend.callsSnapshot(), []string{"literal:first"}) {
		t.Fatalf("retarget err=%v activations=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

func TestWindowTargetFailsClosedWhenFocusCannotBeObserved(t *testing.T) {
	manager := newHandleWindowManager(window.Info{
		NativeID: "1", Title: "Journal", PID: 42, AppID: "editor", Active: false,
	})
	backend := &windowInputSpy{}
	session := newSessionForTesting(nil, backend, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := session.WithContext(ctx).Window("Journal").Do(Type("private"))
	if !errors.Is(err, context.DeadlineExceeded) || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("unobserved focus err=%v calls=%v", err, backend.callsSnapshot())
	}
	if got := manager.actions; !reflect.DeepEqual(got, []string{"activate:1"}) {
		t.Fatalf("activation attempts = %v", got)
	}
}

func TestWindowTargetInvalidClosedAndCanceledViews(t *testing.T) {
	var nilTarget *WindowTarget
	if err := nilTarget.Do(Type("text")); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil target: %v", err)
	}
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "1", Title: "Journal", PID: 42, AppID: "editor",
	})
	if err := session.Window(" ").Do(Type("text")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("blank selector: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := session.WithContext(ctx).Window("Journal").Do(Type("text")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled view: %v", err)
	}
	if len(manager.actions) != 0 || len(backend.callsSnapshot()) != 0 {
		t.Fatal("invalid views dispatched input or activation")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Window("Journal").Do(Type("text")); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session: %v", err)
	}
}

func TestWindowTargetRejectsGlobalPointerClickBeforeActivation(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{
		NativeID: "office-1", Title: "Journal", PID: 42, AppID: "editor",
	})
	err := session.Window("Journal").Do(Type("first"), Click(500, 500))
	if !errors.Is(err, ErrUnsupported) || len(manager.actions) != 0 || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("unsafe global click err=%v activations=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

// A real editor often changes its title after the first keystroke.
func TestWindowTargetContinuesAfterTitleChangeOnSameIdentity(t *testing.T) {
	session, manager, backend := windowConvenienceFixture(t, window.Info{NativeID: "1", Title: "Journal", PID: 42, AppID: "editor"})
	backend.after = func() { manager.mu.Lock(); manager.windows[0].Title = "*Journal"; manager.mu.Unlock() }
	if err := session.Window("Journal").Do(Type("first"), Type("second")); err != nil {
		t.Fatal(err)
	}
	if got := backend.callsSnapshot(); !reflect.DeepEqual(got, []string{"literal:first", "literal:second"}) {
		t.Fatalf("input after title change: %v", got)
	}
}

// Exercise the public facade as well as its private session coordinator.
// Both must remain covered when the wrapper is located with its implementation.
func TestPublicConvenienceFacadeDelegatesToOwningSession(t *testing.T) {
	sentinel := errors.New("reject requested option")
	if _, err := Open(func(*openConfig) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("public OpenDefault option error: %v", err)
	}
	backend := &convenienceInput{}
	core := convenienceSession(t, backend)
	s := wrapSession(core)
	ctx := context.Background()
	if view := s.WithContext(ctx); view.core != core || view.scope.ctx != ctx {
		t.Fatal("public context view lost its private session or context")
	}
	if view := s.WithTimeout(time.Second); view.core != core || view.scope.timeout != time.Second {
		t.Fatal("public timeout view lost its private session or timeout")
	}
	for _, err := range []error{s.Do(Type("batch")), s.Type("literal"), s.Key("tab")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if target := s.Window("Editor"); target == nil {
		t.Fatal("public window selection returned no target")
	}
}

func TestCallerImmediateAndBatchTimeoutOverrideReachProvider(t *testing.T) {
	backend := &deadlineConvenienceInput{}
	core := convenienceSession(t, backend)
	s := wrapSession(core)
	for name, run := range map[string]func() error{
		"root":  func() error { return s.WithTimeout(time.Minute).Type("hello") },
		"input": func() error { return s.Input().WithTimeout(time.Minute).Type("hello") },
		"batch": func() error { return s.WithTimeout(time.Minute).Do(Type("hello")) },
	} {
		t.Run(name, func(t *testing.T) {
			before := time.Now()
			if err := run(); err != nil {
				t.Fatal(err)
			}
			if !backend.ok || backend.deadline.Before(before.Add(59*time.Second)) || backend.deadline.After(time.Now().Add(time.Minute)) {
				t.Fatalf("override was capped or missing: %v", backend.deadline.Sub(before))
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.WithTimeout(time.Minute).WithContext(ctx).Type("hello"); err != nil {
		t.Fatal(err)
	}
	deadline, _ := ctx.Deadline()
	if !backend.deadline.Equal(deadline) {
		t.Fatalf("parent deadline lost: %v != %v", backend.deadline, deadline)
	}
}

func TestCallerBatchCancellationRetainsCause(t *testing.T) {
	backend := &blockingConvenienceInput{started: make(chan struct{})}
	s := wrapSession(convenienceSession(t, backend))
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("caller revoked batch")
	done := make(chan error, 1)
	go func() { done <- s.WithContext(ctx).Do(Type("first"), Type("second")) }()
	<-backend.started
	cancel(cause)
	if err := <-done; !errors.Is(err, cause) || !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation cause: %v", err)
	}
}

func TestCallerPublicShortcutsAndWindowTargetScopes(t *testing.T) {
	backend := &convenienceInput{}
	s := wrapSession(convenienceSession(t, backend))
	if err := s.Hotkey("ctrl", "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Click(1, 2); err != nil {
		t.Fatal(err)
	}
	var nilTarget *WindowTarget
	if nilTarget.WithContext(context.Background()) != nil || nilTarget.WithTimeout(time.Second) != nil {
		t.Fatal("nil target acquired authority")
	}
	var nilSession *Session
	if target := nilSession.Window("Editor"); target == nil || target.view != nil {
		t.Fatal("nil session target acquired authority")
	}
	target := s.Window("Editor")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	copy := target.WithContext(ctx).WithTimeout(time.Minute)
	if target.view.scope.explicit || target.view.scope.timeout != 0 || copy.view.scope.ctx != ctx || copy.view.scope.timeout != time.Minute || copy.view.core != s.core {
		t.Fatal("target scope changed original or lost owner")
	}
	cancel()
	if err := copy.Do(Type("no dispatch")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled target: %v", err)
	}
}

func TestPlan7ResolvedWindowRejectsReplacementAfterCacheRefresh(t *testing.T) {
	core, manager, backend := windowConvenienceFixture(t, window.Info{NativeID: "1", Title: "Journal", PID: 42, AppID: "editor"})
	s := wrapSession(core)
	w, err := s.Windows().Find(window.Title("Journal"))
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.windows[0].PID = 99
	manager.mu.Unlock()
	// Even an ordinary metadata refresh must not reauthorize the target.
	if _, err := w.Info(); !errors.Is(err, ErrWindowIdentityChanged) {
		t.Fatalf("replacement info: %v", err)
	}
	if w.state.CachedInfo().PID != 42 {
		t.Fatal("replacement contaminated cache")
	}
	err = w.Do(Type("private"))
	if !errors.Is(err, ErrWindowIdentityChanged) || len(manager.actions) != 0 || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("replacement err=%v activation=%v input=%v", err, manager.actions, backend.callsSnapshot())
	}
}

func TestPlan7ResolvedWindowDoesNotRediscoverByTitle(t *testing.T) {
	core, manager, backend := windowConvenienceFixture(t, window.Info{NativeID: "1", Title: "Journal", PID: 42, AppID: "editor"})
	s := wrapSession(core)
	w, err := s.Windows().Find(window.Title("Journal"))
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.windows[0].Title = "Changed"
	manager.windows = append(manager.windows, window.Info{NativeID: "2", Title: "Journal", PID: 99, AppID: "editor"})
	manager.mu.Unlock()
	if err := w.Do(Type("one"), Key("enter")); err != nil {
		t.Fatal(err)
	}
	if err := w.WithTimeout(time.Second).Do(Type("two")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manager.actions, []string{"activate:1", "activate:1"}) || !reflect.DeepEqual(backend.callsSnapshot(), []string{"literal:one", "keys:{enter}", "literal:two"}) {
		t.Fatalf("activation=%v input=%v", manager.actions, backend.callsSnapshot())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.WithContext(ctx).Do(Type("never")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var nilWindow *Window
	if err := nilWindow.Do(Type("never")); !errors.Is(err, ErrNilSession) {
		t.Fatal(err)
	}
	if err := w.Do(); err != nil {
		t.Fatal(err)
	}
	if err := w.Do(Click(1, 2)); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
}
