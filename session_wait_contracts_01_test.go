package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/window"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Explicit, non-owned accessibility event sources must participate in the
// session's invalidation hub. The event attribution must remain attached to
// its original epoch rather than being replaced by later notifications.
func TestWaitChangesAcceptsDirectAccessibilityEventSource(t *testing.T) {
	source := newWaitAccessibilityEventBackend()
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	session.Accessibility.backend = source
	epoch := session.waitChanges()
	select {
	case <-source.opened:
	case <-time.After(time.Second):
		t.Fatal("direct event source subscription was not opened")
	}
	event := accessibility.Event{Kind: "state-changed", Property: "enabled"}
	source.events <- event
	select {
	case <-epoch.done:
		if epoch.change.source != "accessibility" || epoch.change.event == nil || epoch.change.event.Kind != event.Kind || epoch.change.event.Property != event.Property {
			t.Fatalf("direct event was not attributed to its epoch: %+v", epoch.change)
		}
	case <-time.After(time.Second):
		t.Fatal("direct event did not notify waiting session")
	}
	// Closing the producer must not close the session's hub or manufacture a
	// second event; notifications from independent sources remain functional.
	close(source.events)
	next := session.waitChanges()
	session.notifyWaiters()
	select {
	case <-next.done:
		if next.change.source != "application" || next.change.event != nil {
			t.Fatalf("unrelated invalidation inherited old event: %+v", next.change)
		}
	case <-time.After(time.Second):
		t.Fatal("closed direct event stream prevented application wake")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
}

// The backend may report a transient evaluation error while the caller
// withdraws wait authority. Cancellation must win over the retry policy,
// including any failures already accumulated for this wait.
func TestWaitConditionFailureAfterCallerCancellationDoesNotRetry(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("synthetic window discovery read failed")
	probes := 0
	failures := &waitEvaluateFailures{consecutive: 2, total: 4, since: time.Now()}
	condition := Predicate("window-read-failure", func(context.Context) (bool, error) {
		probes++
		cancel()
		return false, failure
	})
	matched, err := session.evaluateWaitCondition(ctx, condition, failures)
	if matched || !errors.Is(err, context.Canceled) || probes != 1 {
		t.Fatalf("revoked condition wait returned matched=%v err=%v probes=%d", matched, err, probes)
	}
	if failures.consecutive != 2 || failures.total != 4 {
		t.Fatalf("canceled evaluation incorrectly retried/recorded backend failure: %+v", failures)
	}
}

// Receipt bookkeeping must be a no-op for a missing optional input bundle,
// preserving the original caller result without manufacturing diagnostics.
func TestMissingInputBundleReceiptIsInertAndDoesNotSwallowError(t *testing.T) {
	var absent *InputBundle
	failure := errors.New("synthetic transport unavailable")
	if err := absent.recordInputReceipt("type", time.Now(), failure); !errors.Is(err, failure) {
		t.Fatalf("missing receipt owner changed failure: %v", err)
	}
	if err := absent.recordInputReceipt("type", time.Now(), nil); err != nil {
		t.Fatalf("missing receipt owner manufactured failure: %v", err)
	}
	if got := absent.recentInputReceipts(); got != nil {
		t.Fatalf("missing input bundle exposed receipts: %v", got)
	}
}

// Native window identifiers are only meaningful within their originating
// session. An identical ID on a different compositor must never authorize a
// closure proof or trigger even one backend lookup.
func TestWindowClosedRejectsForeignSessionHandleBeforeBackendLookup(t *testing.T) {
	ownerManager := newHandleWindowManager(window.Info{NativeID: "reused-id", Title: "owner window"})
	otherManager := newHandleWindowManager(window.Info{NativeID: "reused-id", Title: "different window"})
	owner := NewSessionForTesting(nil, nil, ownerManager, nil, nil)
	other := NewSessionForTesting(nil, nil, otherManager, nil, nil)
	t.Cleanup(func() { _ = owner.Close(); _ = other.Close() })
	target := &Window{id: WindowID{session: owner, native: "reused-id"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	evidence, err := other.WaitWithEvidence(ctx, WindowClosed(target), WaitEvery(100*time.Millisecond))
	if !errors.Is(err, ErrInvalidArgument) || evidence.Evaluations != 1 || evidence.PollWakeups != 0 {
		t.Fatalf("foreign session handle not rejected immediately: evidence=%+v err=%v", evidence, err)
	}
	ok, err := WindowClosed(target).evaluate(ctx, owner)
	if err != nil || ok {
		t.Fatalf("same-owner live window incorrectly considered closed: ok=%v err=%v", ok, err)
	}
}

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
			if evidence.Evaluations != 0 || evidence.PollWakeups != 0 {
				t.Fatalf("invalid condition reached observation: %+v", evidence)
			}
		})
	}
}

// Loss of a window backend does not prove an ID has disappeared. A closure
// waiter cannot turn a missing observation capability into a success result.
func TestWindowClosedDoesNotInferDisappearanceFromMissingManager(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	handle := &Window{id: WindowID{session: session, native: "unobserved-window"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ok, err := WindowClosed(handle).evaluate(ctx, session)
	if ok || !errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unavailable manager proved closure: ok=%v err=%v", ok, err)
	}
	evidence, err := session.WaitWithEvidence(ctx, WindowClosed(handle), WaitEvery(100*time.Millisecond))
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrUnsupported) || evidence.Evaluations != 1 || evidence.PollWakeups != 0 {
		t.Fatalf("unavailable manager was treated as closed or polled: evidence=%+v err=%v", evidence, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unavailable manager exhausted caller timeout: %v", err)
	}
}

// A polling-only predicate has no relationship to desktop event invalidations.
// Unrelated event storms must not bypass the configured polling interval or
// misattribute event wakeups to a state-only predicate.
func TestWaitPollingOnlyIgnoresUnrelatedInvalidationEvents(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	var evaluations atomic.Int32
	var ready atomic.Bool
	first := make(chan struct{})
	type result struct {
		evidence WaitEvidence
		err      error
	}
	completed := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		evidence, err := session.WaitWithEvidence(ctx, Predicate("external flag", func(context.Context) (bool, error) {
			if evaluations.Add(1) == 1 {
				close(first)
			}
			return ready.Load(), nil
		}), WaitEvery(250*time.Millisecond))
		completed <- result{evidence, err}
	}()
	select {
	case <-first:
	case <-ctx.Done():
		t.Fatalf("predicate did not start: %v", ctx.Err())
	}
	for i := 0; i < 20; i++ {
		session.notifyWaiters()
	}
	select {
	case <-time.After(20 * time.Millisecond):
	case got := <-completed:
		t.Fatalf("unrelated event ended polling-only wait: %+v", got)
	}
	if got := evaluations.Load(); got != 1 {
		t.Fatalf("unrelated desktop events triggered %d predicate evaluations, expected 1", got)
	}
	ready.Store(true)
	select {
	case got := <-completed:
		if got.err != nil || got.evidence.Evaluations < 2 || got.evidence.PollWakeups < 1 || got.evidence.Wakeups != 0 {
			t.Fatalf("poll-only evidence attributed desktop wakeups: %+v", got)
		}
	case <-ctx.Done():
		t.Fatalf("polling-only predicate never observed its new state: %v", ctx.Err())
	}
}
