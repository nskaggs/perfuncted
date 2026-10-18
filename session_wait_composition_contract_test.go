package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

// Any describes alternative authoritative proofs. A transiently unavailable
// proof must not hide a different proof that is already satisfied.
func TestAnyEvaluatesAvailableAlternatives(t *testing.T) {
	unavailable := errors.New("window observer unavailable")
	otherUnavailable := errors.New("accessibility observer unavailable")
	testCases := []struct {
		name     string
		parts    []Condition
		wantOK   bool
		wantErrs []error
	}{
		{
			name: "unavailable then satisfied",
			parts: []Condition{
				Predicate("unavailable", func(context.Context) (bool, error) { return false, unavailable }),
				Predicate("ready", func(context.Context) (bool, error) { return true, nil }),
			},
			wantOK: true,
		},
		{
			name: "unsatisfied then unavailable then satisfied",
			parts: []Condition{
				Predicate("pending", func(context.Context) (bool, error) { return false, nil }),
				Predicate("unavailable", func(context.Context) (bool, error) { return false, unavailable }),
				Predicate("ready", func(context.Context) (bool, error) { return true, nil }),
			},
			wantOK: true,
		},
		{
			name: "no satisfied proof preserves one error",
			parts: []Condition{
				Predicate("pending", func(context.Context) (bool, error) { return false, nil }),
				Predicate("unavailable", func(context.Context) (bool, error) { return false, unavailable }),
			},
			wantErrs: []error{unavailable},
		},
		{
			name: "no satisfied proof preserves all errors",
			parts: []Condition{
				Predicate("unavailable", func(context.Context) (bool, error) { return false, unavailable }),
				Predicate("other unavailable", func(context.Context) (bool, error) { return false, otherUnavailable }),
			},
			wantErrs: []error{unavailable, otherUnavailable},
		},
		{
			name: "no satisfied proof and no error is pending",
			parts: []Condition{
				Predicate("pending", func(context.Context) (bool, error) { return false, nil }),
			},
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := Any(tt.parts...).evaluate(context.Background(), nil)
			if ok != tt.wantOK {
				t.Fatalf("Any() = (%v, %v); want success %v", ok, err, tt.wantOK)
			}
			if len(tt.wantErrs) == 0 && err != nil {
				t.Fatalf("unexpected Any error: %v", err)
			}
			if len(tt.wantErrs) > 0 && err == nil {
				t.Fatal("Any discarded failed observation with no satisfied alternative")
			}
			for _, expected := range tt.wantErrs {
				if !errors.Is(err, expected) {
					t.Errorf("Any error %v does not preserve %v", err, expected)
				}
			}
		})
	}
}

func TestMixedConditionPreservesEventWakeups(t *testing.T) {
	for _, tt := range []struct {
		name    string
		compose func(Condition, Condition) Condition
	}{
		{"Any", func(poll, event Condition) Condition { return Any(poll, event) }},
		{"All", func(poll, event Condition) Condition { return All(poll, event) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := newHandleWindowManager()
			session := NewSessionForTesting(nil, nil, manager, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			firstEvaluation := make(chan struct{})
			poll := Predicate("local prerequisite", func(context.Context) (bool, error) {
				select {
				case <-firstEvaluation:
				default:
					close(firstEvaluation)
				}
				return tt.name == "All", nil
			})
			condition := tt.compose(poll, WindowExists(WindowMatch{TitleExact: "Ready"}))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			type result struct {
				evidence WaitEvidence
				err      error
			}
			done := make(chan result, 1)
			go func() {
				evidence, err := session.WaitWithEvidence(ctx, condition, WaitEvery(5*time.Second))
				done <- result{evidence, err}
			}()
			select {
			case <-firstEvaluation:
			case <-ctx.Done():
				t.Fatal("initial wait evaluation never started")
			}
			manager.setWindows(true, window.Info{NativeID: "ready", Title: "Ready"})
			select {
			case result := <-done:
				if result.err != nil {
					t.Fatalf("mixed wait error: %v", result.err)
				}
				if result.evidence.WindowWakeups < 1 {
					t.Fatalf("mixed wait returned without event evidence: %+v", result.evidence)
				}
			case <-ctx.Done():
				t.Fatal("mixed condition did not consume a window event; fallback was deliberately longer than deadline")
			}
		})
	}
}

// A successful predicate is not evidence of success if it invalidated the
// caller's context while being evaluated.
func TestWaitDoesNotAcceptSuccessAfterCancellation(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := session.Wait(ctx, Predicate("cancelling predicate", func(context.Context) (bool, error) {
		cancel()
		return true, nil
	}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() = %v; want cancellation, not a stale success", err)
	}
}
