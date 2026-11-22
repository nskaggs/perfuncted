package perfuncted

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConvenienceCoverageOpenAndViewCopies(t *testing.T) {
	sentinel := errors.New("reject requested option")
	if _, err := OpenDefault(func(*openConfig) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("option error: %v", err)
	}
	s := convenienceSession(t, &convenienceInput{})
	var nilView *SessionView
	if nilView.WithContext(context.Background()).session != nil {
		t.Fatal("nil receiver acquired a session")
	}
	if nilView.WithTimeout(time.Second).session != nil {
		t.Fatal("nil receiver acquired a session")
	}
	parent := s.WithContext(context.Background()).WithTimeout(time.Second)
	copyWithContext := parent.WithContext(context.Background())
	copyWithTimeout := parent.WithTimeout(2 * time.Second)
	if parent.timeout != time.Second || copyWithContext.timeout != time.Second ||
		copyWithTimeout.timeout != 2*time.Second || parent.session != s || copyWithTimeout.session != s {
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
	original := s.Input
	s.Input = nil
	err := s.preflightSteps(context.Background(), []Step{Type("private")})
	s.Input = original
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
