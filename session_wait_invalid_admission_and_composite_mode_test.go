package perfuncted

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// A wait with no owner or no condition must be rejected without constructing
// observers or claiming any condition evaluation occurred.
func TestWaitWithEvidenceRejectsMissingOwnerAndConditionBeforePolling(t *testing.T) {
	var missing *Session
	evidence, err := missing.WaitWithEvidence(context.Background(), Predicate("ready", func(context.Context) (bool, error) {
		t.Fatal("nil session invoked predicate")
		return true, nil
	}))
	if !errors.Is(err, ErrNilSession) || evidence != (WaitEvidence{}) {
		t.Fatalf("nil session returned evidence=%+v error=%v", evidence, err)
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	evidence, err = session.WaitWithEvidence(context.Background(), nil)
	if !errors.Is(err, ErrInvalidArgument) || evidence != (WaitEvidence{}) {
		t.Fatalf("nil condition returned evidence=%+v error=%v", evidence, err)
	}
	calls := 0
	evidence, err = session.WaitWithEvidence(context.Background(), Predicate("ready", func(context.Context) (bool, error) { calls++; return true, nil }), nil)
	if err != nil || calls != 1 || evidence.Evaluations != 1 {
		t.Fatalf("nil option changed wait behavior: evidence=%+v err=%v calls=%d", evidence, err, calls)
	}
}

// A malformed composite kind cannot become meaningful by polling. Refuse it
// before touching any child: otherwise a broken condition may claim success.
func TestCompositeConditionRejectsUnsupportedModeWithoutEvaluatingChildren(t *testing.T) {
	calls := 0
	condition := compositeCondition{name: "unsupported", mode: "none", conditions: []Condition{
		Predicate("ready", func(context.Context) (bool, error) { calls++; return true, nil }),
	}}
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ok, err := condition.evaluate(ctx, session)
	if ok || err == nil || !strings.Contains(err.Error(), "unknown composite mode") || calls != 0 {
		t.Fatalf("malformed composite accepted: ok=%t err=%v calls=%d", ok, err, calls)
	}
}
