package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/window"
)

func reviewChangeFixture(t *testing.T) (*AccessibilityLocator, *snapshotAccessibilityAutomationFake, accessibility.NodeID, accessibility.Node) {
	t.Helper()
	loc, fake := newLocatorWaitFixture(t, 1, false)
	root := fake.snapshot.Root.ID
	root.Incarnation = 1
	fake.snapshot.Root.ID = root
	fake.snapshot.Nodes[0].ID = root
	fake.snapshot.Nodes[1].ID.Incarnation = 2
	fake.snapshot.Nodes[1].Parent = root
	fake.apps[0].Node.ID = root
	return loc, fake, root, fake.snapshot.Nodes[1]
}

func TestReviewChangedActionRequiresCurrentBaseline(t *testing.T) {
	ready := Predicate("ready", func(context.Context) (bool, error) { return true, nil })
	for _, wrap := range []struct {
		name string
		fn   func(Condition) Condition
	}{
		{"direct", func(c Condition) Condition { return c }},
		{"all", func(c Condition) Condition { return All(ready, c) }},
		{"any", func(c Condition) Condition { return Any(ready, c) }},
		{"not", Not},
		{"nested", func(c Condition) Condition {
			return WithConditionCapabilities(All(ready, Not(c)), CapabilityAccessibility)
		}},
	} {
		for _, method := range []string{"locator-click", "locator-fill", "session"} {
			t.Run(method+"/"+wrap.name, func(t *testing.T) {
				loc, fake, root, baseline := reviewChangeFixture(t)
				baseline.Text = "stale baseline"
				c := wrap.fn(AccessibilityNodeChanged(root, baseline, accessibility.SnapshotOptions{}))
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				var err error
				switch method {
				case "locator-click":
					_, _, err = loc.WithContext(ctx).ClickAndWait(c, WaitEvery(time.Millisecond))
				case "locator-fill":
					_, _, err = loc.WithContext(ctx).FillAndWait("new", c, WaitEvery(time.Millisecond))
				case "session":
					_, _, err = loc.bundle.session.InvokeAccessibilityActionAndWait(ctx, root, accessibility.Query{Name: "Save"}, "press", accessibility.SnapshotOptions{}, c, WaitEvery(time.Millisecond))
				}
				if !errors.Is(err, ErrManagedScopeChanged) || len(fake.calls) != 0 || fake.textCount != 0 || fake.invokeCount != 0 {
					t.Fatalf("err=%v calls=%v writes=%d invokes=%d; stale baseline must prevent dispatch", err, fake.calls, fake.textCount, fake.invokeCount)
				}
			})
		}
	}
}

func TestReviewChangedConditionFreshAndUnfiltered(t *testing.T) {
	loc, fake, root, baseline := reviewChangeFixture(t)
	c := AccessibilityNodeChanged(root, baseline, accessibility.SnapshotOptions{})
	ok, err := c.evaluate(context.Background(), loc.bundle.session)
	if err != nil || ok || !fake.snapshotOptions.Fresh {
		t.Fatalf("ok=%v err=%v opts=%+v", ok, err, fake.snapshotOptions)
	}
	for _, opts := range []accessibility.SnapshotOptions{{VisibleOnly: true}, {SkipRoles: []string{"panel"}}} {
		c := AccessibilityNodeChanged(root, baseline, opts)
		if err := validateConditionPreflight(c); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("filtered options %+v accepted: %v", opts, err)
		}
	}
}

func TestReviewChangedConditionRejectsIncompleteBaselineAndFilteredWait(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  accessibility.SnapshotOptions
		alter func(*accessibility.Node)
	}{
		{"visible only", accessibility.SnapshotOptions{VisibleOnly: true}, func(*accessibility.Node) {}},
		{"skipped role", accessibility.SnapshotOptions{SkipRoles: []string{"panel"}}, func(*accessibility.Node) {}},
		{"warning", accessibility.SnapshotOptions{}, func(n *accessibility.Node) { n.Warnings = []string{"unobserved state"} }},
		{"truncated text", accessibility.SnapshotOptions{}, func(n *accessibility.Node) { n.TextTruncated = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loc, fake, root, baseline := reviewChangeFixture(t)
			tc.alter(&baseline)
			fake.snapshot.Nodes = fake.snapshot.Nodes[:1]
			c := AccessibilityNodeChanged(root, baseline, tc.opts)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := loc.bundle.session.Wait(ctx, c)
			if !errors.Is(err, ErrInvalidArgument) || fake.snapshotCalls != 0 {
				t.Fatalf("err=%v snapshots=%d", err, fake.snapshotCalls)
			}
		})
	}
}

func TestReviewChangedActionRejectsDifferentVerificationRoot(t *testing.T) {
	loc, fake, root, baseline := reviewChangeFixture(t)
	unrelated := root
	unrelated.ObjectPath = "/unrelated"
	c := AccessibilityNodeChanged(unrelated, baseline, accessibility.SnapshotOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, err := loc.bundle.session.InvokeAccessibilityActionAndWait(ctx, root, accessibility.Query{Name: "Save"}, "press", accessibility.SnapshotOptions{}, c)
	if !errors.Is(err, ErrManagedScopeChanged) || len(fake.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, fake.calls)
	}
}

func TestReviewChangedConditionRejectsUnobservableTargetState(t *testing.T) {
	for _, alter := range []func(*accessibility.Node){
		func(n *accessibility.Node) { n.TextTruncated = true },
		func(n *accessibility.Node) { n.Warnings = []string{"incomplete state"} },
	} {
		loc, fake, root, baseline := reviewChangeFixture(t)
		alter(&fake.snapshot.Nodes[1])
		c := AccessibilityNodeChanged(root, baseline, accessibility.SnapshotOptions{})
		ok, err := c.evaluate(context.Background(), loc.bundle.session)
		if ok || !errors.Is(err, ErrUnavailable) {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	}
}

func TestReviewChangedConditionProvesFreshChangeAndDisappearance(t *testing.T) {
	loc, fake, root, baseline := reviewChangeFixture(t)
	c := AccessibilityNodeChanged(root, baseline, accessibility.SnapshotOptions{})
	fake.snapshot.Nodes[1].Name = "Saved"
	ok, err := c.evaluate(context.Background(), loc.bundle.session)
	if !ok || err != nil || !fake.snapshotOptions.Fresh {
		t.Fatalf("changed: ok=%v err=%v", ok, err)
	}
	fake.snapshot.Nodes = fake.snapshot.Nodes[:1]
	ok, err = c.evaluate(context.Background(), loc.bundle.session)
	if !ok || err != nil {
		t.Fatalf("absent: ok=%v err=%v", ok, err)
	}
}

func TestReviewChangedConditionOwnsBaseline(t *testing.T) {
	loc, fake, root, baseline := reviewChangeFixture(t)
	baseline.Attributes = map[string]string{"state": "before"}
	baseline.Value = &accessibility.ValueInfo{Current: 1}
	baseline.States = []string{"enabled"}
	fake.snapshot.Nodes[1] = baseline
	condition := AccessibilityNodeChanged(root, baseline, accessibility.SnapshotOptions{})
	// A provider or caller can reuse the original node's mutable storage.
	baseline.Attributes["state"] = "after"
	baseline.Value.Current = 2
	baseline.States[0] = "focused"
	ok, err := condition.evaluate(context.Background(), loc.bundle.session)
	if !ok || err != nil {
		t.Fatalf("mutated baseline erased change: ok=%v err=%v", ok, err)
	}
}

func TestReviewWrappedNilConditionFailsWithoutPanic(t *testing.T) {
	loc, _, _, _ := reviewChangeFixture(t)
	err := loc.bundle.session.Wait(context.Background(), WithConditionCapabilities(nil, CapabilityAccessibility))
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("got %v", err)
	}
}

func TestReviewCompositeOwnsChildSlice(t *testing.T) {
	ready := Predicate("ready", func(context.Context) (bool, error) { return true, nil })
	pending := Predicate("pending", func(context.Context) (bool, error) { return false, nil })
	parts := []Condition{ready}
	all, any := All(parts...), Any(parts...)
	parts[0] = pending
	for _, condition := range []Condition{all, any} {
		ok, err := condition.evaluate(context.Background(), nil)
		if !ok || err != nil {
			t.Fatalf("caller mutation changed %s: %v %v", condition.describe(), ok, err)
		}
	}
}

func TestReviewWaitRejectsInvalidConditionBeforeObservation(t *testing.T) {
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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

// The backend's lifecycle-event producer may close independently of the
// session. An exhausted producer must not cause spurious repeated wakeups or
// invalidate independent application notifications.
func TestWaitHubClosedWindowChangeStreamPreservesOtherNotifications(t *testing.T) {
	backend := newHandleWindowManager(window.Info{NativeID: "w-1"})
	session := newSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	first := session.waitChanges()
	backend.changes <- struct{}{}
	select {
	case <-first.done:
		if first.change.source != "window" || first.change.event != nil {
			t.Fatalf("window source was misattributed: %+v", first.change)
		}
	case <-time.After(time.Second):
		t.Fatal("window source did not notify the invalidation hub")
	}

	close(backend.changes)
	next := session.waitChanges()
	select {
	case <-next.done:
		t.Fatalf("closed window producer invented a wakeup: %+v", next.change)
	case <-time.After(10 * time.Millisecond):
	}
	session.notifyWaiters()
	select {
	case <-next.done:
		if next.change.source != "application" || next.change.event != nil {
			t.Fatalf("closed window source corrupted an independent wakeup: %+v", next.change)
		}
	case <-time.After(time.Second):
		t.Fatal("application notification lost after closing window source")
	}
}

// Direct accessibility event sources may terminate independently of the
// session; once their stream closes the invalidation hub must remain usable.
type directlyClosingAccessibilitySource struct {
	waitAccessibilityBackend
	events chan accessibility.Event
	opened chan struct{}
}

func (s *directlyClosingAccessibilitySource) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	close(s.opened)
	return s.events, nil
}

func TestWaitHubClosedAccessibilityStreamPreservesOtherNotifications(t *testing.T) {
	source := &directlyClosingAccessibilitySource{
		events: make(chan accessibility.Event, 1),
		opened: make(chan struct{}),
	}
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	session.accessibility.backend = source
	first := session.waitChanges()
	select {
	case <-source.opened:
	case <-time.After(time.Second):
		t.Fatal("accessibility event source was not opened")
	}
	source.events <- accessibility.Event{Kind: "focus"}
	select {
	case <-first.done:
		if first.change.source != "accessibility" || first.change.event == nil || first.change.event.Kind != "focus" {
			t.Fatalf("accessibility event attributed incorrectly: %+v", first.change)
		}
	case <-time.After(time.Second):
		t.Fatal("accessibility stream did not notify the invalidation hub")
	}
	close(source.events)
	next := session.waitChanges()
	select {
	case <-next.done:
		t.Fatalf("closed accessibility stream invented wakeup: %+v", next.change)
	case <-time.After(10 * time.Millisecond):
	}
	session.notifyWaiters()
	select {
	case <-next.done:
		if next.change.source != "application" || next.change.event != nil {
			t.Fatalf("closed accessibility stream corrupted application event: %+v", next.change)
		}
	case <-time.After(time.Second):
		t.Fatal("application notification lost after closing accessibility stream")
	}
}

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
			session := newSessionForTesting(nil, nil, manager, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			// Wait for a completed first window query, not merely the first
			// predicate call. Otherwise a ready window can be observed in
			// the initial evaluation before the event is published.
			firstWindowQuery := make(chan struct{})
			poll := Predicate("local prerequisite", func(context.Context) (bool, error) {
				return tt.name == "All", nil
			})
			windowReady := sessionCondition("ready window", func(ctx context.Context, s *sessionCore) (bool, error) {
				ready, err := WindowExists(window.Match{TitleExact: "Ready"}).evaluate(ctx, s)
				select {
				case <-firstWindowQuery:
				default:
					close(firstWindowQuery)
				}
				return ready, err
			})
			condition := tt.compose(poll, windowReady)
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
			case <-firstWindowQuery:
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
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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

// Explicit, non-owned accessibility event sources must participate in the
// session's invalidation hub. The event attribution must remain attached to
// its original epoch rather than being replaced by later notifications.
func TestWaitChangesAcceptsDirectAccessibilityEventSource(t *testing.T) {
	source := newWaitAccessibilityEventBackend()
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	session.accessibility.backend = source
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
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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
	var absent *inputState
	failure := errors.New("synthetic transport unavailable")
	if err := absent.RecordInputReceipt("type", time.Now(), failure); !errors.Is(err, failure) {
		t.Fatalf("missing receipt owner changed failure: %v", err)
	}
	if err := absent.RecordInputReceipt("type", time.Now(), nil); err != nil {
		t.Fatalf("missing receipt owner manufactured failure: %v", err)
	}
	if got := absent.RecentInputReceipts(); got != nil {
		t.Fatalf("missing input bundle exposed receipts: %v", got)
	}
}

// Native window identifiers are only meaningful within their originating
// session. An identical ID on a different compositor must never authorize a
// closure proof or trigger even one backend lookup.
func TestWindowClosedRejectsForeignSessionHandleBeforeBackendLookup(t *testing.T) {
	ownerManager := newHandleWindowManager(window.Info{NativeID: "reused-id", Title: "owner window"})
	otherManager := newHandleWindowManager(window.Info{NativeID: "reused-id", Title: "different window"})
	owner := newSessionForTesting(nil, nil, ownerManager, nil, nil)
	other := newSessionForTesting(nil, nil, otherManager, nil, nil)
	t.Cleanup(func() { _ = owner.Close(); _ = other.Close() })
	target := &windowState{id: WindowID{session: owner, native: "reused-id"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	evidence, err := other.WaitWithEvidence(ctx, windowClosed(target), WaitEvery(100*time.Millisecond))
	if !errors.Is(err, ErrInvalidArgument) || evidence.Evaluations != 1 || evidence.PollWakeups != 0 {
		t.Fatalf("foreign session handle not rejected immediately: evidence=%+v err=%v", evidence, err)
	}
	ok, err := windowClosed(target).evaluate(ctx, owner)
	if err != nil || ok {
		t.Fatalf("same-owner live window incorrectly considered closed: ok=%v err=%v", ok, err)
	}
}

// A wait with no owner or no condition must be rejected without constructing
// observers or claiming any condition evaluation occurred.
func TestWaitWithEvidenceRejectsMissingOwnerAndConditionBeforePolling(t *testing.T) {
	var missing *sessionCore
	evidence, err := missing.WaitWithEvidence(context.Background(), Predicate("ready", func(context.Context) (bool, error) {
		t.Fatal("nil session invoked predicate")
		return true, nil
	}))
	if !errors.Is(err, ErrNilSession) || evidence != (WaitEvidence{}) {
		t.Fatalf("nil session returned evidence=%+v error=%v", evidence, err)
	}
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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
			session := newSessionForTesting(nil, nil, nil, nil, nil)
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
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	handle := &windowState{id: WindowID{session: session, native: "unobserved-window"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ok, err := windowClosed(handle).evaluate(ctx, session)
	if ok || !errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unavailable manager proved closure: ok=%v err=%v", ok, err)
	}
	evidence, err := session.WaitWithEvidence(ctx, windowClosed(handle), WaitEvery(100*time.Millisecond))
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
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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

// A missing session-bound window is a programming error, not a transient
// compositor observation. Retrying until the caller's deadline conceals it.
func TestWaitWindowClosedRejectsMissingWindowHandleImmediately(t *testing.T) {
	session := newSessionForTesting(nil, nil, newHandleWindowManager(), nil, nil)
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	evidence, err := session.WaitWithEvidence(ctx, windowClosed(nil), WaitEvery(time.Second))
	if !errors.Is(err, ErrNilSession) || evidence.Evaluations != 1 {
		t.Fatalf("missing handle retried as a transient failure: %+v %v", evidence, err)
	}
}

// A backend without stable window IDs cannot ever verify a particular handle.
// Its unsupported capability must fail without a long retry loop.
func TestWaitWindowClosedRejectsUnsupportedStableHandleLookupImmediately(t *testing.T) {
	manager := &flakyWindowManager{}
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	target := &windowState{id: WindowID{session: session, native: "synthetic-window"}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	evidence, err := session.WaitWithEvidence(ctx, windowClosed(target), WaitEvery(time.Second))
	if !errors.Is(err, ErrUnsupported) || !errors.Is(err, window.ErrNotSupported) || evidence.Evaluations != 1 {
		t.Fatalf("unsupported stable ID lookup retried: %+v %v", evidence, err)
	}
}

// WindowClosed is a stable-identity query, not a title query. A renamed window
// is still present; disappearance of its native handle is the success proof.
func TestWaitWindowClosedRequiresStableHandleDisappearance(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "opaque-123", Title: "Before"})
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	target := &windowState{id: WindowID{session: session, native: "opaque-123"}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	manager.setWindows(false, window.Info{NativeID: "opaque-123", Title: "After"})
	proof, err := windowClosed(target).evaluate(ctx, session)
	if err != nil || proof {
		t.Fatalf("title-only change falsely proved window closed: proof=%v err=%v", proof, err)
	}
	manager.setWindows(false)
	proof, err = windowClosed(target).evaluate(ctx, session)
	if err != nil || !proof {
		t.Fatalf("stable window disappearance was not recognized: proof=%v err=%v", proof, err)
	}
	evidence, err := session.WaitWithEvidence(ctx, windowClosed(target))
	if err != nil || evidence.Evaluations != 1 {
		t.Fatalf("closed window not recognized immediately: %+v %v", evidence, err)
	}
}

// A manager that cannot answer is not evidence that a live window vanished.
func TestWaitWindowClosedPropagatesTransientBackendFailure(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "opaque-123", Title: "Live"})
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	target := &windowState{id: WindowID{session: session, native: "opaque-123"}}
	failure := errors.New("synthetic window transport timeout")
	manager.setError(failure)
	proof, err := windowClosed(target).evaluate(context.Background(), session)
	if proof || !errors.Is(err, failure) {
		t.Fatalf("failed lookup falsely proved window closed: proof=%v err=%v", proof, err)
	}
	failures := waitEvaluateFailures{}
	proof, err = session.evaluateWaitCondition(context.Background(), windowClosed(target), &failures)
	if proof || err != nil || failures.total != 1 {
		t.Fatalf("transient manager failure no longer retries: proof=%v err=%v count=%d", proof, err, failures.total)
	}
	manager.setError(nil)
	manager.setWindows(false)
	proof, err = session.evaluateWaitCondition(context.Background(), windowClosed(target), &failures)
	if err != nil || !proof || failures.consecutive != 0 {
		t.Fatalf("subsequent valid disappearance failed: proof=%v err=%v failures=%+v", proof, err, failures)
	}
}

// A stable window handle is not evidence of its disappearance after the
// observer's own Session has closed. In particular, the capability admission
// error must not be mistaken for an unavailable compositor and retried.
func TestWindowClosedConditionPreservesClosedSessionError(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "private-handle", Title: "private"})
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	handle := &windowState{id: WindowID{session: session, native: "private-handle"}}
	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	matched, err := windowClosed(handle).evaluate(context.Background(), session)
	if matched || !errors.Is(err, ErrSessionClosed) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed owner incorrectly proved disappearance: matched=%v err=%v", matched, err)
	}
}

// flakyWindowManager fails the first N List calls, then reports windows.
type flakyWindowManager struct {
	mu        sync.Mutex
	failures  int
	err       error
	windows   []window.Info
	listCalls int
}

func (m *flakyWindowManager) List(context.Context) ([]window.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listCalls++
	if m.listCalls <= m.failures {
		return nil, m.err
	}
	return append([]window.Info(nil), m.windows...), nil
}

func (m *flakyWindowManager) IterateWindows(
	ctx context.Context,
) iter.Seq2[window.Info, error] {
	return func(yield func(window.Info, error) bool) {
		windows, err := m.List(ctx)
		if err != nil {
			yield(window.Info{}, err)
			return
		}
		for _, w := range windows {
			if !yield(w, nil) {
				return
			}
		}
	}
}

func (m *flakyWindowManager) ActiveTitle(context.Context) (string, error) {
	return "", nil
}

func (m *flakyWindowManager) Close() error { return nil }

func TestSessionWaitRetriesTransientEvaluationErrors(t *testing.T) {
	manager := &flakyWindowManager{
		failures: 5,
		err:      fmt.Errorf("window/sway: get_tree: read unix: i/o timeout"),
		windows:  []window.Info{{NativeID: "ready", Title: "Ready"}},
	}
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := session.Wait(ctx, WindowExists(window.Match{TitleExact: "Ready"}), WaitEvery(5*time.Millisecond)); err != nil {
		t.Fatalf("Wait aborted on transient evaluation errors: %v", err)
	}
}

func TestSessionWaitAbortsOnPermanentEvaluationError(t *testing.T) {
	manager := &flakyWindowManager{
		failures: 1000,
		err:      ErrApplicationExited,
	}
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := session.Wait(ctx, WindowExists(window.Match{TitleExact: "Ready"}), WaitEvery(5*time.Millisecond))
	if err == nil || !errors.Is(err, ErrApplicationExited) {
		t.Fatalf("Wait error = %v; want immediate ErrApplicationExited", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("permanent error took %v to abort; want fast failure", elapsed)
	}
}

func TestSessionWaitGivesUpAfterSustainedTransientErrors(t *testing.T) {
	manager := &flakyWindowManager{
		failures: 1000,
		err:      fmt.Errorf("window/sway: get_tree: read unix: i/o timeout"),
	}
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := session.Wait(ctx, WindowExists(window.Match{TitleExact: "Ready"}), WaitEvery(2*time.Millisecond))
	if err == nil || !strings.Contains(err.Error(), "consecutive and") || !strings.Contains(err.Error(), "total evaluation errors") {
		t.Fatalf("Wait error = %v; want sustained-failure abort", err)
	}
}

// alternatingWindowManager fails every other List call forever. Half its reads
// answer, so a wait that resets its failure count on every successful read can
// never accumulate enough consecutive failures to give up.
type alternatingWindowManager struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (m *alternatingWindowManager) List(context.Context) ([]window.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls%2 == 0 {
		return nil, m.err
	}
	return nil, nil
}

func (m *alternatingWindowManager) IterateWindows(
	ctx context.Context,
) iter.Seq2[window.Info, error] {
	return func(yield func(window.Info, error) bool) {
		windows, err := m.List(ctx)
		if err != nil {
			yield(window.Info{}, err)
			return
		}
		for _, w := range windows {
			if !yield(w, nil) {
				return
			}
		}
	}
}

func (m *alternatingWindowManager) ActiveTitle(context.Context) (string, error) {
	return "", nil
}

func (m *alternatingWindowManager) Close() error { return nil }

// A source that answers only half the time is still mostly unreadable, and the
// caller has to be told so rather than being waited out to its own deadline.
func TestSessionWaitGivesUpOnFailuresInterleavedWithSuccessfulReads(t *testing.T) {
	manager := &alternatingWindowManager{err: fmt.Errorf("window/sway: get_tree: read unix: i/o timeout")}
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	// The deadline is far longer than the bound needs, so an implementation that
	// waits the deadline out is reported by the elapsed check instead.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	start := time.Now()
	err := session.Wait(ctx, WindowExists(window.Match{TitleExact: "Ready"}), WaitEvery(time.Millisecond))
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "total evaluation errors") {
		t.Fatalf("Wait error = %v; want an abort reporting the wait-wide failure total", err)
	}
	if !errors.Is(err, manager.err) {
		t.Fatalf("Wait error = %v; want the underlying read failure attached", err)
	}
	if elapsed >= 30*time.Second {
		t.Fatalf("Wait took %v; it waited out its deadline instead of reporting unreadable state", elapsed)
	}
	manager.mu.Lock()
	calls := manager.calls
	manager.mu.Unlock()
	if calls > 2*waitEvaluateTotalLimit+2 {
		t.Fatalf("List calls = %d, want the wait bounded by the failure total", calls)
	}
}

// The consecutive count is not itself a bound in elapsed time: a wait driven by
// window events evaluates far more often than one driven by its poll interval.
// A run of failures that stays unanswered long enough must be reported even
// though its count is well under the consecutive limit.
func TestWaitEvaluateFailuresBoundStreakByElapsedTime(t *testing.T) {
	start := time.Unix(0, 0)
	now := start
	failures := waitEvaluateFailures{now: func() time.Time { return now }}

	if exhausted, _, _ := failures.record(); exhausted {
		t.Fatal("first failure exhausted the tolerance")
	}
	// Failures spread across less than the budget stay tolerated, which is the
	// point: their count alone would not have stopped them.
	for range 8 {
		now = now.Add(waitEvaluateStreakBudget / 16)
		if exhausted, consecutive, _ := failures.record(); exhausted {
			t.Fatalf("failure %d recorded %v into the streak exhausted the tolerance early", consecutive, now.Sub(start))
		}
	}
	now = now.Add(waitEvaluateStreakBudget)
	exhausted, consecutive, _ := failures.record()
	if consecutive >= waitEvaluateFailureLimit {
		t.Fatalf("consecutive failures = %d; the test must not reach the count limit", consecutive)
	}
	if !exhausted {
		t.Fatalf("record %v into a streak of %d consecutive failures = exhausted %t; want the streak budget to end the run",
			now.Sub(start), consecutive, exhausted)
	}
}

// A run of failures that is answered in time resets the consecutive count but
// not the wait-wide total.
func TestWaitEvaluateFailuresResetEndsStreakNotTotal(t *testing.T) {
	failures := waitEvaluateFailures{}
	for range 3 {
		if exhausted, _, _ := failures.record(); exhausted {
			t.Fatal("few failures exhausted the tolerance")
		}
	}
	failures.reset()
	if failures.consecutive != 0 || failures.total != 3 || !failures.since.IsZero() {
		t.Fatalf("after reset = consecutive %d, total %d, since %v; want 0, 3, zero",
			failures.consecutive, failures.total, failures.since)
	}
}

// Cancellation may occur after public wait preflight and before the first
// condition evaluation. Exercise this boundary without a scheduling race.
func TestWaitWithEvidenceRejectsCancellationBeforeFirstEvaluation(t *testing.T) {
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	evidence, err := session.waitWithEvidence(ctx, Predicate("not evaluated", func(context.Context) (bool, error) {
		t.Fatal("canceled wait evaluated its predicate")
		return false, nil
	}), waitConfig{interval: time.Millisecond})
	if !errors.Is(err, context.Canceled) || evidence != (WaitEvidence{}) {
		t.Fatalf("canceled wait returned evidence=%+v err=%v", evidence, err)
	}
}

type coreOnlyAccessibilityBackend struct{ accessibility.Backend }
type ownerLifecycleBackend struct {
	operations []string
	generation atomic.Uint64
	closed     atomic.Bool
	usedClosed atomic.Bool
	closeOnce  sync.Once
	closeCalls atomic.Int32
	closeErr   error
	closeDone  chan struct{}

	appsEntered chan struct{}
	appsGate    <-chan struct{}
	appsOnce    sync.Once
	apps        []accessibility.Application

	eventReady       chan ownerEventFixture
	eventEntered     chan struct{}
	eventEnteredOnce sync.Once
	eventGate        <-chan struct{}
	eventErr         error
	eventNil         bool
	eventCalls       atomic.Int32
	eventMu          sync.Mutex
	eventCancels     []context.CancelFunc
	eventClosers     []func()
	eventRetired     bool
	retireEntered    chan struct{}
	retireOnce       sync.Once
	retireGate       <-chan struct{}
	retireErr        error
	retireCalls      atomic.Int32
	reopen           func(context.Context) (accessibility.Backend, error)
}

type ownerEventFixture struct {
	events    chan accessibility.Event
	close     func()
	err       error
	nilStream bool
}

func newOwnerLifecycleBackend(generation uint64, operations ...string) *ownerLifecycleBackend {
	backend := &ownerLifecycleBackend{
		operations: operations,
		closeDone:  make(chan struct{}),
		eventReady: make(chan ownerEventFixture, 8),
	}
	backend.generation.Store(generation)
	return backend
}

func (b *ownerLifecycleBackend) SupportedOperations() []string {
	return append([]string(nil), b.operations...)
}

func (b *ownerLifecycleBackend) Applications(context.Context) ([]accessibility.Application, error) {
	if b.closed.Load() {
		b.usedClosed.Store(true)
	}
	if b.appsEntered != nil {
		b.appsOnce.Do(func() { close(b.appsEntered) })
	}
	if b.appsGate != nil {
		<-b.appsGate
	}
	if b.closed.Load() {
		b.usedClosed.Store(true)
	}
	return append([]accessibility.Application(nil), b.apps...), nil
}

func (*ownerLifecycleBackend) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	return accessibility.Snapshot{}, nil
}

func (*ownerLifecycleBackend) Find(context.Context, accessibility.NodeID, accessibility.Query, accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	return nil, nil
}

func (*ownerLifecycleBackend) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	return accessibility.Node{}, nil
}

func (*ownerLifecycleBackend) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	return accessibility.Node{}, nil
}

func (b *ownerLifecycleBackend) Close() error {
	b.closeCalls.Add(1)
	b.closeOnce.Do(func() {
		b.closed.Store(true)
		close(b.closeDone)
	})
	return b.closeErr
}

func (b *ownerLifecycleBackend) Generation() uint64              { return b.generation.Load() }
func (b *ownerLifecycleBackend) Invalidate(accessibility.NodeID) { b.generation.Add(1) }

func (b *ownerLifecycleBackend) Reopen(ctx context.Context) (accessibility.Backend, error) {
	if b.reopen == nil {
		return nil, accessibility.ErrUnsupported
	}
	return b.reopen(ctx)
}

func (b *ownerLifecycleBackend) Events(ctx context.Context, _ accessibility.EventOptions) (<-chan accessibility.Event, error) {
	b.eventCalls.Add(1)
	if b.eventEntered != nil {
		b.eventEnteredOnce.Do(func() { close(b.eventEntered) })
	}
	eventCtx, cancel := context.WithCancel(ctx)
	b.eventMu.Lock()
	if b.eventRetired {
		b.eventMu.Unlock()
		cancel()
		b.eventReady <- ownerEventFixture{err: accessibility.ErrDisconnected}
		return nil, accessibility.ErrDisconnected
	}
	b.eventCancels = append(b.eventCancels, cancel)
	eventGate := b.eventGate
	eventErr := b.eventErr
	eventNil := b.eventNil
	b.eventMu.Unlock()
	if eventGate != nil {
		select {
		case <-eventGate:
		case <-eventCtx.Done():
			err := eventCtx.Err()
			cancel()
			b.eventReady <- ownerEventFixture{err: err}
			return nil, err
		}
	}
	if err := eventCtx.Err(); err != nil {
		cancel()
		b.eventReady <- ownerEventFixture{err: err}
		return nil, err
	}
	if eventErr != nil {
		cancel()
		b.eventReady <- ownerEventFixture{err: eventErr}
		return nil, eventErr
	}
	if eventNil {
		cancel()
		b.eventReady <- ownerEventFixture{nilStream: true}
		return nil, nil
	}
	stream := make(chan accessibility.Event, 4)
	var closeOnce sync.Once
	closeStream := func() { closeOnce.Do(func() { close(stream) }) }
	b.eventMu.Lock()
	b.eventClosers = append(b.eventClosers, closeStream)
	b.eventMu.Unlock()
	b.eventReady <- ownerEventFixture{events: stream, close: closeStream}
	go func() {
		<-eventCtx.Done()
		closeStream()
	}()
	return stream, nil
}

func (b *ownerLifecycleBackend) RetireEventsForReopen(ctx context.Context) error {
	b.retireCalls.Add(1)
	b.eventMu.Lock()
	b.eventRetired = true
	cancels := append([]context.CancelFunc(nil), b.eventCancels...)
	closers := append([]func(){}, b.eventClosers...)
	gate := b.retireGate
	b.eventMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	for _, closeStream := range closers {
		closeStream()
	}
	if b.retireEntered != nil {
		b.retireOnce.Do(func() { close(b.retireEntered) })
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return errors.Join(b.retireErr, ctx.Err())
}

func awaitOwnerSignal[T any](t *testing.T, ch <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func TestAccessibilityWaitRebindsToReopenedBackendBeforeWaking(t *testing.T) {
	old := newOwnerLifecycleBackend(2, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	fresh := newOwnerLifecycleBackend(3, "applications", "snapshot", "find", "focused", "at-point", "events", "reopen")
	old.reopen = func(context.Context) (accessibility.Backend, error) { return fresh, nil }
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	evaluations := make(chan int32, 8)
	var count atomic.Int32
	var eventObserved atomic.Bool
	waitResult := make(chan struct {
		evidence WaitEvidence
		err      error
	}, 1)
	go func() {
		evidence, err := session.WaitWithEvidence(ctx, sessionCondition("reopened event", func(context.Context, *sessionCore) (bool, error) {
			current := count.Add(1)
			evaluations <- current
			return eventObserved.Load(), nil
		}), WaitEvery(time.Hour))
		waitResult <- struct {
			evidence WaitEvidence
			err      error
		}{evidence: evidence, err: err}
	}()
	oldEvents := awaitOwnerSignal(t, old.eventReady, "initial accessibility event subscription")
	// The subscription can publish before WaitWithEvidence performs its first
	// condition evaluation. Synchronize on that evaluation so the close/reopen
	// sequence cannot race ahead of the wait and strand the test for an hour.
	if got := awaitOwnerSignal(t, evaluations, "initial wait evaluation"); got != 1 {
		t.Fatalf("initial wait evaluation = %d, want 1", got)
	}
	oldEvents.close()

	reopenResult := make(chan error, 1)
	go func() { reopenResult <- session.accessibility.Reopen(ctx) }()
	newEvents := awaitOwnerSignal(t, fresh.eventReady, "reopened accessibility event subscription")
	if err := awaitOwnerSignal(t, reopenResult, "reopen and event rebinding"); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	for current := int32(0); current < 2; {
		current = awaitOwnerSignal(t, evaluations, "wait evaluation after reopen")
	}

	eventObserved.Store(true)
	newEvents.events <- accessibility.Event{Kind: "state-changed", Property: "enabled"}
	result := awaitOwnerSignal(t, waitResult, "accessibility event wake")
	if result.err != nil {
		t.Fatalf("WaitWithEvidence: %v", result.err)
	}
	if result.evidence.PollWakeups != 0 {
		t.Fatalf("poll wakeups = %d, want event-only wake", result.evidence.PollWakeups)
	}
	if result.evidence.AccessibilityWakeups == 0 || result.evidence.LastAccessibilityEvent == nil || result.evidence.LastAccessibilityEvent.Kind != "state-changed" {
		t.Fatalf("wait evidence = %+v, want event from reopened backend", result.evidence)
	}
}
