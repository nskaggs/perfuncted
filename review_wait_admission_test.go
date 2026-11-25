package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReviewWaitRejectsInvalidConditionBeforeObservation(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	calls := 0
	ready := Predicate("ready", func(context.Context) (bool, error) { calls++; return true, nil })
	for _, tc := range []struct {
		name      string
		condition Condition
	}{
		{"empty all", All()},
		{"empty any", Any()},
		{"invalid alternative", Any(ready, Predicate("invalid", nil))},
		{"invalid all child", All(ready, nil)},
		{"invalid wrapped capability", WithConditionCapabilities(ready, Capability("invalid"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls = 0
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			// Bound the wait even if an invalid condition is mistakenly polled.
			evidence, err := session.WaitWithEvidence(ctx, tc.condition)
			if !errors.Is(err, ErrInvalidArgument) || calls != 0 || evidence.Evaluations != 0 {
				t.Fatalf("err=%v calls=%d evidence=%+v", err, calls, evidence)
			}
		})
	}
}
