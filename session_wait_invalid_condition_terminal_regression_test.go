package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Invalid condition configurations can never become valid through polling.
// A long-lived wait must reject these immediately rather than retrying them as
// transient desktop observation failures until its caller gives up.
func TestWaitRejectsMalformedConditionWithoutPollingUntilDeadline(t *testing.T) {
	for _, tc := range []struct {
		name      string
		condition Condition
	}{
		{"nil predicate", Predicate("broken", nil)},
		{"all with nil child", All(Predicate("ready", func(context.Context) (bool, error) { return true, nil }), nil)},
		{"any with nil child", Any(nil, Predicate("pending", func(context.Context) (bool, error) { return false, nil }))},
		{"not with nil child", Not(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := NewSessionForTesting(nil, nil, nil, nil, nil)
			defer session.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			evidence, err := session.WaitWithEvidence(ctx, tc.condition, WaitEvery(time.Second))
			if !errors.Is(err, ErrInvalidArgument) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("invalid condition waited instead of failing with ErrInvalidArgument: evidence=%+v err=%v", evidence, err)
			}
			if evidence.Evaluations != 1 || evidence.PollWakeups != 0 {
				t.Fatalf("invalid condition was polled repeatedly: %+v", evidence)
			}
		})
	}
}
