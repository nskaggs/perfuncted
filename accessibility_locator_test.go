package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/accessibility"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestAccessibilityLocatorActionRejectsAmbiguousTargetWithoutDispatch(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 2, false)
	checks := 0

	receipt, evidence, err := locator.ClickAndWait(context.Background(), Predicate("activated", func(context.Context) (bool, error) {
		checks++
		return true, nil
	}))
	if !errors.Is(err, accessibility.ErrAmbiguous) {
		t.Fatalf("ClickAndWait error = %v, want ambiguous target", err)
	}
	if receipt.Node.ID.BusName != "" || evidence.Evaluations != 0 || checks != 0 {
		t.Fatalf("ambiguous action exposed a target or evaluated its postcondition: receipt=%+v evidence=%+v checks=%d", receipt, evidence, checks)
	}
	if fake.invokeCount != 0 || len(fake.calls) != 0 {
		t.Fatalf("ambiguous action dispatched provider calls %v, want none", fake.calls)
	}
	_, observationDone, _ := fake.eventObservationState()
	select {
	case <-observationDone:
	default:
		t.Fatal("ambiguous action left semantic event observation active")
	}
}

func TestAccessibilityLocatorActionAfterSessionCloseDoesNotQueryOrDispatch(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	if err := locator.bundle.session.Close(); err != nil {
		t.Fatalf("close owning session: %v", err)
	}
	checks := 0

	receipt, evidence, err := locator.ClickAndWait(context.Background(), Predicate("activated", func(context.Context) (bool, error) {
		checks++
		return true, nil
	}))
	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("ClickAndWait after session close = %v, want ErrSessionClosed", err)
	}
	if receipt.Node.ID.BusName != "" || evidence.Evaluations != 0 || checks != 0 {
		t.Fatalf("closed locator disclosed an action result: receipt=%+v evidence=%+v checks=%d", receipt, evidence, checks)
	}
	if fake.snapshotCalls != 0 || fake.invokeCount != 0 || len(fake.calls) != 0 {
		t.Fatalf("closed locator reached provider: snapshots=%d invocations=%d calls=%v", fake.snapshotCalls, fake.invokeCount, fake.calls)
	}
}

type locatorFailingScopeRefresh struct {
	*snapshotAccessibilityAutomationFake
	refreshCalls int
	refreshErr   error
}

func (b *locatorFailingScopeRefresh) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.refreshCalls++
	if b.refreshCalls == 2 {
		return accessibility.Application{}, b.refreshErr
	}
	return b.snapshotAccessibilityAutomationFake.FindApplicationFresh(ctx, filter)
}

func TestAccessibilityLocatorDoesNotRetryStaleActionWhenScopeRefreshFails(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	wantErr := errors.New("synthetic application identity lookup failure")
	fake.textErr = accessibility.ErrStaleGeneration
	backend := &locatorFailingScopeRefresh{snapshotAccessibilityAutomationFake: fake, refreshErr: wantErr}
	locator.bundle.backend = backend
	checks := 0

	receipt, evidence, err := locator.FillAndWait(context.Background(), "replacement", Predicate("updated", func(context.Context) (bool, error) {
		checks++
		return true, nil
	}))
	if !errors.Is(err, wantErr) {
		t.Fatalf("FillAndWait error = %v, want scope refresh failure", err)
	}
	if backend.refreshCalls != 2 || fake.textCount != 1 {
		t.Fatalf("scope refreshes=%d text writes=%d, want initial resolution, one failed refresh, and no retry", backend.refreshCalls, fake.textCount)
	}
	if receipt.Node.ID.BusName == "" || receipt.Dispatch != accessibility.DispatchNotSent || evidence.Evaluations != 0 || checks != 0 {
		t.Fatalf("failed stale-action recovery returned unexpected evidence: receipt=%+v evidence=%+v checks=%d", receipt, evidence, checks)
	}
}

// A semantic target must not be treated as unchanged when its PID/bus/app
// identity disappears and only a non-unique human-readable name remains.
func TestLocatorIdentityRejectsAuthoritativeEvidenceDowngrade(t *testing.T) {
	initial := locatorIdentity{pid: 412, busName: ":1.412", appID: "org.editor.App", name: "Editor"}
	cases := []struct {
		name    string
		current locatorIdentity
		same    bool
	}{
		{"unchanged strong identity", initial, true},
		{"PID retained while optional app missing", locatorIdentity{pid: 412, name: "Editor"}, true},
		{"PID and bus lost, name retained", locatorIdentity{name: "Editor"}, false},
		{"PID lost, same app id", locatorIdentity{appID: "org.editor.App", name: "Editor"}, true},
		{"PID lost, different app id", locatorIdentity{appID: "org.other.App", name: "Editor"}, false},
		{"bus lost, name retained", locatorIdentity{name: "Editor", appID: "org.editor.App"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameLocatorScope(initial, tc.current); got != tc.same {
				t.Fatalf("identity downgrade accepted=%v want=%v initial=%+v current=%+v", got, tc.same, initial, tc.current)
			}
		})
	}
}

// Invalid locator operations must fail before a semantic snapshot, event
// subscription, or mutation dispatch can expose or change a target.
func TestLocatorRejectsInvalidMutationInputsWithoutProviderActivity(t *testing.T) {
	loc, fake := newLocatorWaitFixture(t, 1, false)
	valid := sessionCondition("after mutation", func(context.Context, *Session) (bool, error) { return true, nil })
	for _, tc := range []struct {
		name string
		call func() (AccessibilityActionReceipt, WaitEvidence, error)
		want error
	}{
		{"fill nil context", func() (AccessibilityActionReceipt, WaitEvidence, error) { return loc.FillAndWait(nil, "text", valid) }, ErrInvalidArgument},
		{"fill nil condition", func() (AccessibilityActionReceipt, WaitEvidence, error) {
			return loc.FillAndWait(context.Background(), "text", nil)
		}, ErrInvalidArgument},
		{"fill invalid UTF-8", func() (AccessibilityActionReceipt, WaitEvidence, error) {
			return loc.FillAndWait(context.Background(), "\xff", valid)
		}, ErrInvalidArgument},
		{"action nil context", func() (AccessibilityActionReceipt, WaitEvidence, error) {
			return loc.InvokeActionAndWait(nil, "click", valid)
		}, ErrInvalidArgument},
		{"action nil condition", func() (AccessibilityActionReceipt, WaitEvidence, error) {
			return loc.InvokeActionAndWait(context.Background(), "click", nil)
		}, ErrInvalidArgument},
		{"click nil context", func() (AccessibilityActionReceipt, WaitEvidence, error) { return loc.ClickAndWait(nil, valid) }, ErrInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt, evidence, err := tc.call()
			if !errors.Is(err, tc.want) || receipt.Node.ID.BusName != "" || evidence.Evaluations != 0 {
				t.Fatalf("invalid mutation: receipt=%+v evidence=%+v err=%v", receipt, evidence, err)
			}
		})
	}
	if fake.snapshotCalls != 0 || fake.eventCalls != 0 {
		t.Fatalf("invalid mutation contacted backend: snapshots=%d events=%d", fake.snapshotCalls, fake.eventCalls)
	}
}

func TestLocatorUnownedOperationsDoNotDisclosePrivateTargets(t *testing.T) {
	loc, _ := newLocatorWaitFixture(t, 1, false)
	detached := *loc
	detached.bundle = nil
	var nilLocator *AccessibilityLocator
	postcondition := sessionCondition("done", func(context.Context, *Session) (bool, error) { return true, nil })
	for _, tc := range []struct {
		name    string
		locator *AccessibilityLocator
	}{
		{"nil locator", nilLocator},
		{"detached locator", &detached},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if nodes, identity, root, stop, err := tc.locator.find(context.Background()); !errors.Is(err, ErrUnavailable) || len(nodes) != 0 || identity != (locatorIdentity{}) || root != (accessibility.NodeID{}) || stop != nil {
				t.Fatalf("unowned find: nodes=%v identity=%+v root=%+v err=%v", nodes, identity, root, err)
			}
			if receipt, evidence, err := tc.locator.FillAndWait(context.Background(), "text", postcondition); !errors.Is(err, ErrUnavailable) || receipt.Node.ID.BusName != "" || evidence.Evaluations != 0 {
				t.Fatalf("unowned fill: receipt=%+v evidence=%+v err=%v", receipt, evidence, err)
			}
			if receipt, evidence, err := tc.locator.ClickAndWait(context.Background(), postcondition); !errors.Is(err, ErrUnavailable) || receipt.Node.ID.BusName != "" || evidence.Evaluations != 0 {
				t.Fatalf("unowned action: receipt=%+v evidence=%+v err=%v", receipt, evidence, err)
			}
		})
	}
}

func TestLocatorMatchesNilContextIsRejectedBeforeProviderAccess(t *testing.T) {
	loc, fake := newLocatorWaitFixture(t, 1, false)
	nodes, err := loc.Matches(nil)
	if !errors.Is(err, ErrInvalidArgument) || len(nodes) != 0 || fake.snapshotCalls != 0 || fake.eventCalls != 0 {
		t.Fatalf("nil matches: nodes=%v err=%v snapshots=%d events=%d", nodes, err, fake.snapshotCalls, fake.eventCalls)
	}
}

// A locator must never accept a target after an invalidation, a closed
// subscription, or a missing subscription. An empty live stream is the only
// observation state that permits the caller to proceed with a fresh read.
func TestLocatorObservationContractRejectsMissingClosedAndInvalidatedStreams(t *testing.T) {
	live := make(chan accessibility.Event, 1)
	closed := make(chan accessibility.Event)
	close(closed)
	invalidated := make(chan accessibility.Event, 1)
	invalidated <- accessibility.Event{Kind: "object:children-changed"}
	for _, tc := range []struct {
		name        string
		stream      <-chan accessibility.Event
		wantChanged bool
	}{
		{name: "nil subscription", wantChanged: true},
		{name: "closed subscription", stream: closed, wantChanged: true},
		{name: "queued invalidation", stream: invalidated, wantChanged: true},
		{name: "live empty subscription", stream: live},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := locatorObservationReady(tc.stream)
			if got := errors.Is(err, accessibility.ErrObservationChanged); got != tc.wantChanged {
				t.Fatalf("observation error = %v; changed = %t, want %t", err, got, tc.wantChanged)
			}
			if !tc.wantChanged && err != nil {
				t.Fatalf("live observation unexpectedly rejected: %v", err)
			}
		})
	}
}

func TestLocatorRetriesOnlyObservationAndStaleIdentityFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"stale generation", errors.Join(errors.New("wrapped"), accessibility.ErrStaleGeneration), true},
		{"stale node", errors.Join(errors.New("wrapped"), accessibility.ErrStaleNode), true},
		{"observation changed", errors.Join(errors.New("wrapped"), accessibility.ErrObservationChanged), true},
		{"unsupported", accessibility.ErrUnsupported, false},
		{"invalid argument", ErrInvalidArgument, false},
		{"generic provider failure", errors.New("provider unavailable"), false},
		{"success", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := locatorResolutionMayRetry(tc.err); got != tc.retry {
				t.Fatalf("retry(%v) = %t, want %t", tc.err, got, tc.retry)
			}
		})
	}
}

func TestAccessibilityLocatorRejectsScopeReplacementAfterStaleSnapshot(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	fake.snapshotErr = accessibility.ErrStaleGeneration
	first := fake.apps[0]
	replacement := first
	replacement.ID = accessibility.NodeID{
		BusName: "org.synthetic.Replacement", ObjectPath: "/replacement", Generation: 12,
	}
	fake.freshApps = []accessibility.Application{first, replacement}

	matches, err := locator.Matches(context.Background())
	if !errors.Is(err, accessibility.ErrScope) || len(matches) != 0 {
		t.Fatalf("scope replacement after stale snapshot returned matches=%+v error=%v, want no matches and ErrScope", matches, err)
	}
	if fake.snapshotCalls != 1 || fake.freshApplicationFind != 2 {
		t.Fatalf("scope resolutions=%d snapshots=%d, want two resolutions and only the original snapshot", fake.freshApplicationFind, fake.snapshotCalls)
	}
}

// Locator scope resolution must reject absent managed handles before any
// provider request, rather than falling back to an unscoped desktop query.
func TestLocatorScopeRejectsEmptyManagedWindowBeforeProviderAccess(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	locator.scope = locatorScope{kind: locatorWindowScope, windowID: ""}
	root, identity, err := locator.scopeRoot(context.Background())
	if !errors.Is(err, ErrInvalidArgument) || root != (accessibility.NodeID{}) || identity != (locatorIdentity{}) {
		t.Fatalf("empty managed window scope: root=%+v identity=%+v err=%v", root, identity, err)
	}
	if fake.snapshotCalls != 0 || fake.findCalls != 0 {
		t.Fatalf("empty window scope touched provider: snapshot=%d find=%d", fake.snapshotCalls, fake.findCalls)
	}
}

// Comparing consecutive provider observations must reject both target
// replacement and disappearance of previously verified identity evidence.
func TestLocatorIdentityChangeRequiresStableScopeAndRoot(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.example.Editor", ObjectPath: "/app", Generation: 4}
	initial := locatorIdentity{windowID: "win-1", pid: 220, appID: "org.example.Editor", busName: root.BusName, name: "Editor"}
	for _, tc := range []struct {
		name                     string
		initial, current         locatorIdentity
		initialRoot, currentRoot accessibility.NodeID
		have                     bool
		changed                  bool
	}{
		{name: "uninitialized observation", initial: initial, current: locatorIdentity{windowID: "win-2"}, initialRoot: root, currentRoot: root, have: false},
		{name: "same root and identity", initial: initial, current: initial, initialRoot: root, currentRoot: root, have: true},
		{name: "different managed window", initial: initial, current: locatorIdentity{windowID: "win-2", pid: 220, busName: root.BusName}, initialRoot: root, currentRoot: root, have: true, changed: true},
		{name: "different root path", initial: initial, current: initial, initialRoot: root, currentRoot: accessibility.NodeID{BusName: root.BusName, ObjectPath: "/replacement", Generation: 4}, have: true, changed: true},
		{name: "different root bus", initial: initial, current: initial, initialRoot: root, currentRoot: accessibility.NodeID{BusName: "org.example.Replacement", ObjectPath: root.ObjectPath, Generation: 4}, have: true, changed: true},
		{name: "lost authoritative identity", initial: initial, current: locatorIdentity{name: "Editor", windowID: "win-1"}, initialRoot: root, currentRoot: root, have: true, changed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := locatorIdentityChanged(tc.have, tc.initial, tc.initialRoot, tc.current, tc.currentRoot); got != tc.changed {
				t.Fatalf("identity changed=%v want=%v", got, tc.changed)
			}
		})
	}
}

// A rejected input context must not start observation or resolve a snapshot.
func TestLocatorFindRejectsNilAndPreCanceledContextsBeforeBackendRead(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name    string
		context context.Context
		want    error
	}{
		{"nil context", nil, ErrInvalidArgument},
		{"canceled context", ctx, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matches, identity, root, stop, err := locator.find(tc.context)
			if !errors.Is(err, tc.want) || len(matches) != 0 || identity != (locatorIdentity{}) || root != (accessibility.NodeID{}) || stop != nil {
				t.Fatalf("invalid locator context exposed state: matches=%v identity=%+v root=%+v stop=%v err=%v", matches, identity, root, stop != nil, err)
			}
		})
	}
	if fake.snapshotCalls != 0 || fake.eventCalls != 0 {
		t.Fatalf("invalid context accessed backend: snapshots=%d events=%d", fake.snapshotCalls, fake.eventCalls)
	}
}

// A failed scope lookup must never return the previously observed application
// or a fabricated window root. Locators are used to authorize subsequent
// semantic actions, so even error paths must carry no target identity.
func TestLocatorScopeRootFailuresNeverReturnAuthoritativeIdentity(t *testing.T) {
	t.Run("unknown managed window", func(t *testing.T) {
		locator, fake := newLocatorWaitFixture(t, 1, false)
		locator.scope = locatorScope{kind: locatorWindowScope, windowID: "unregistered-window"}
		root, identity, err := locator.scopeRoot(context.Background())
		if err == nil || root != (accessibility.NodeID{}) || identity != (locatorIdentity{}) {
			t.Fatalf("unregistered managed window disclosed root=%+v identity=%+v error=%v", root, identity, err)
		}
		if fake.snapshotCalls != 0 {
			t.Fatalf("unregistered window read %d provider snapshots", fake.snapshotCalls)
		}
	})
	t.Run("provider refuses application scope", func(t *testing.T) {
		locator, fake := newLocatorWaitFixture(t, 1, false)
		rejection := errors.New("synthetic provider denied application identity")
		backend := &locatorApplicationLookupFailureBackend{snapshotAccessibilityAutomationFake: fake, failure: rejection}
		locator.bundle.backend = backend
		root, identity, err := locator.scopeRoot(context.Background())
		if !errors.Is(err, rejection) || root != (accessibility.NodeID{}) || identity != (locatorIdentity{}) {
			t.Fatalf("rejected application disclosed root=%+v identity=%+v error=%v", root, identity, err)
		}
		if backend.lookups != 1 || fake.snapshotCalls != 0 {
			t.Fatalf("scope used lookups=%d snapshots=%d", backend.lookups, fake.snapshotCalls)
		}
	})
}

func TestAccessibilityLocatorWaitStopsOnPermanentBackendFailure(t *testing.T) {
	locator, backend := newLocatorWaitFixture(t, 1, false)
	failure := ErrApplicationExited
	backend.snapshotErr = failure

	node, evidence, err := locator.Wait(context.Background())
	if !errors.Is(err, failure) {
		t.Fatalf("locator wait error = %v, want permanent failure %v", err, failure)
	}
	if node.ID.BusName != "" || evidence.Evaluations != 1 || backend.snapshotCalls != 1 {
		t.Fatalf("permanent snapshot failure was retried or published as a match: node=%+v evidence=%+v snapshots=%d", node, evidence, backend.snapshotCalls)
	}
}

type locatorLostObservationBackend struct {
	*snapshotAccessibilityAutomationFake
	closedBeforeRead bool
	stream           chan accessibility.Event
}

func (b *locatorLostObservationBackend) Events(_ context.Context, _ accessibility.EventOptions) (<-chan accessibility.Event, error) {
	b.stream = make(chan accessibility.Event, 1)
	if b.closedBeforeRead {
		close(b.stream)
	}
	return b.stream, nil
}
func (b *locatorLostObservationBackend) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	snapshot, err := b.snapshotAccessibilityAutomationFake.Snapshot(ctx, root, opts)
	if !b.closedBeforeRead {
		close(b.stream)
	}
	return snapshot, err
}

// A complete snapshot is not a stable semantic target unless its event
// subscription remained live throughout resolution.
func TestAccessibilityLocatorRejectsLostEventObservation(t *testing.T) {
	for _, phase := range []string{"before-snapshot", "during-snapshot"} {
		t.Run(phase, func(t *testing.T) {
			locator, fake := newLocatorWaitFixture(t, 1, false)
			backend := &locatorLostObservationBackend{snapshotAccessibilityAutomationFake: fake, closedBeforeRead: phase == "before-snapshot"}
			old := locator.bundle.backend
			locator.bundle.backend = backend
			t.Cleanup(func() { locator.bundle.backend = old })
			matches, err := locator.Matches(context.Background())
			if !errors.Is(err, accessibility.ErrObservationChanged) || len(matches) != 0 {
				t.Fatalf("lost observation at %s exposed %d matches: %v", phase, len(matches), err)
			}
			if phase == "before-snapshot" && fake.snapshotCalls != 0 {
				t.Fatalf("snapshot dispatched after missing observation: %d", fake.snapshotCalls)
			}
		})
	}
}

type revokingLocatorFillBackend struct {
	*snapshotAccessibilityAutomationFake
	after  func()
	writes int
}

func (b *revokingLocatorFillBackend) SetTextContents(_ context.Context, _ accessibility.NodeID, _ string) error {
	b.writes++
	if b.after != nil {
		b.after()
	}
	return nil
}

// A provider may ignore cancellation or session closure while performing a
// text mutation. The failed high-level operation must not disclose the
// previously resolved private node in its dispatch receipt.
func TestAccessibilityLocatorFillSuppressesReceiptAfterRevokedAuthority(t *testing.T) {
	for _, mode := range []string{"caller-cancel", "session-close"} {
		t.Run(mode, func(t *testing.T) {
			locator, fake := newLocatorWaitFixture(t, 1, false)
			session := locator.bundle.session
			backend := &revokingLocatorFillBackend{snapshotAccessibilityAutomationFake: fake}
			previous := locator.bundle.backend
			locator.bundle.backend = backend
			t.Cleanup(func() { locator.bundle.backend = previous })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := context.Canceled
			if mode == "caller-cancel" {
				backend.after = cancel
			} else {
				want = ErrSessionClosed
				backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			}
			checks := 0
			receipt, _, err := locator.FillAndWait(ctx, "private-value", Predicate("verified", func(context.Context) (bool, error) { checks++; return true, nil }))
			if !errors.Is(err, want) || receipt.Node.ID.BusName != "" || receipt.Node.Name != "" || backend.writes != 1 || checks != 0 {
				t.Fatalf("%s leaked fill after revocation: receipt=%+v err=%v writes=%d checks=%d", mode, receipt, err, backend.writes, checks)
			}
		})
	}
}

func TestLocatorScopeIdentityNeverAcceptsDifferentAuthoritativeTargets(t *testing.T) {
	editor := locatorIdentity{windowID: "17", pid: 101, appID: "org.editor", busName: "org.editor.Bus", name: "Editor"}
	for _, tc := range []struct {
		name          string
		first, second locatorIdentity
		same          bool
	}{
		{"same authoritative identifiers", editor, editor, true},
		{"different managed window", editor, locatorIdentity{windowID: "18", pid: 101, appID: "org.editor", busName: "org.editor.Bus"}, false},
		{"different bus owner", editor, locatorIdentity{windowID: "17", pid: 101, appID: "org.editor", busName: "org.editor.New"}, false},
		{"different process", editor, locatorIdentity{windowID: "17", pid: 102, appID: "org.editor", busName: "org.editor.Bus"}, false},
		{"same process different app", editor, locatorIdentity{windowID: "17", pid: 101, appID: "org.other", busName: "org.editor.Bus"}, false},
		{"optional app absent", editor, locatorIdentity{windowID: "17", pid: 101, busName: "org.editor.Bus"}, true},
		{"app fallback agreement", locatorIdentity{appID: "org.editor"}, locatorIdentity{appID: "org.editor"}, true},
		{"app fallback mismatch", locatorIdentity{appID: "org.editor"}, locatorIdentity{appID: "org.other"}, false},
		{"bus fallback agreement", locatorIdentity{busName: "org.editor.Bus"}, locatorIdentity{busName: "org.editor.Bus"}, true},
		{"bus fallback mismatch", locatorIdentity{busName: "org.editor.Bus"}, locatorIdentity{busName: "org.other.Bus"}, false},
		{"name fallback case insensitive", locatorIdentity{name: " Editor "}, locatorIdentity{name: "editor"}, true},
		{"name fallback mismatch", locatorIdentity{name: "Editor"}, locatorIdentity{name: "Browser"}, false},
		{"no identity evidence", locatorIdentity{}, locatorIdentity{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sameLocatorScope(tc.first, tc.second)
			if got != tc.same {
				t.Fatalf("sameLocatorScope(%+v,%+v)=%v, want %v", tc.first, tc.second, got, tc.same)
			}
		})
	}
}

func TestLocatorDispatchReceiptClassificationAndSelectionAreFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want accessibility.DispatchOutcome
	}{
		{"successful", nil, accessibility.DispatchAccepted},
		{"stale generation", fmtWrap(accessibility.ErrStaleGeneration), accessibility.DispatchNotSent},
		{"stale node", fmtWrap(accessibility.ErrStaleNode), accessibility.DispatchNotSent},
		{"unsupported", fmtWrap(accessibility.ErrUnsupported), accessibility.DispatchNotSent},
		{"rejected mutation", fmtWrap(accessibility.ErrMutationRejected), accessibility.DispatchRejected},
		{"uncertain transport", errors.New("provider dropped the reply"), accessibility.DispatchUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := textDispatchOutcome(tc.err); got != tc.want {
				t.Fatalf("dispatch=%v want=%v", got, tc.want)
			}
		})
	}
	target := accessibility.Node{ID: accessibility.NodeID{BusName: "org.editor.Bus", ObjectPath: "/target", Generation: 5}, Name: "Save"}
	for _, tc := range []struct {
		name  string
		nodes []accessibility.Node
		want  error
	}{
		{"no result", nil, accessibility.ErrNotFound},
		{"unique result", []accessibility.Node{target}, nil},
		{"ambiguous result", []accessibility.Node{target, target}, accessibility.ErrAmbiguous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, err := uniqueLocatorMatch(tc.nodes)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if tc.want == nil && !reflect.DeepEqual(node, target) {
				t.Fatalf("node=%+v want=%+v", node, target)
			}
			if tc.want != nil && node.ID.BusName != "" {
				t.Fatalf("error leaked target %+v", node)
			}
		})
	}
}

func fmtWrap(err error) error { return errors.Join(errors.New("outer"), err) }

func TestLocatorScopeRootApplicationAndInvalidScopes(t *testing.T) {
	loc, _ := newLocatorWaitFixture(t, 1, false)
	root, identity, err := func() (accessibility.NodeID, locatorIdentity, error) {
		r, i, e := loc.scopeRoot(context.Background())
		return r, i, e
	}()
	if err != nil || root.ObjectPath != "/application" || identity.name != "Editor" || identity.busName != "org.synthetic.Editor" {
		t.Fatalf("application root=%+v identity=%+v error=%v", root, identity, err)
	}
	bad := *loc
	for _, tc := range []struct {
		name   string
		target *AccessibilityLocator
		want   error
	}{
		{"nil locator", nil, ErrUnavailable},
		{"missing bundle", &AccessibilityLocator{}, ErrUnavailable},
		{"missing scope", &bad, ErrInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "missing scope" {
				bad.scope.kind = 0
			}
			_, _, err := tc.target.scopeRoot(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("scope error=%v want=%v", err, tc.want)
			}
		})
	}
}

func TestLocatorConstructorsCopySelectorIntentAndTrimManagedWindowID(t *testing.T) {
	base := accessibility.Selector{States: []string{"focused"}, Attributes: map[string]string{"role": "button"}, Ancestors: []accessibility.AncestorSelector{{Name: "Editor"}}}
	b := &AccessibilityBundle{}
	loc := b.LocatorForWindow("  17  ", base, accessibility.SnapshotOptions{})
	base.States[0] = "untrusted"
	base.Attributes["role"] = "untrusted"
	base.Ancestors[0].Name = "untrusted"
	if loc.scope.windowID != "17" || loc.selector.States[0] != "focused" || loc.selector.Attributes["role"] != "button" || loc.selector.Ancestors[0].Name != "Editor" {
		t.Fatalf("locator reused caller-owned selector or untrimmed handle: %+v", loc)
	}
	app := b.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, base, accessibility.SnapshotOptions{})
	if app.scope.kind != locatorApplicationScope || app.scope.application.Name != "Editor" {
		t.Fatalf("application constructor=%+v", app.scope)
	}
}

type locatorLateDeadlineSnapshot struct {
	*snapshotAccessibilityAutomationFake
	expired bool
}

func (b *locatorLateDeadlineSnapshot) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	<-ctx.Done()
	b.expired = errors.Is(ctx.Err(), context.DeadlineExceeded)
	return b.snapshotAccessibilityAutomationFake.Snapshot(ctx, root, opts)
}

// A backend may return a structurally complete tree after its bounded read
// deadline. The locator must refuse that stale tree even if the caller's outer
// context has not yet expired.
func TestAccessibilityLocatorRejectsSnapshotAfterInternalReadDeadline(t *testing.T) {
	for _, operation := range []string{"matches", "resolve"} {
		t.Run(operation, func(t *testing.T) {
			locator, fake := newLocatorWaitFixture(t, 1, false)
			locator.bundle.session.config.Timeouts = TimeoutPolicy{Long: 10 * time.Millisecond}
			backend := &locatorLateDeadlineSnapshot{snapshotAccessibilityAutomationFake: fake}
			previous := locator.bundle.backend
			locator.bundle.backend = backend
			t.Cleanup(func() { locator.bundle.backend = previous })
			var found string
			var err error
			if operation == "matches" {
				var nodes []accessibility.Node
				nodes, err = locator.Matches(context.Background())
				if len(nodes) > 0 {
					found = nodes[0].Name
				}
			} else {
				var node accessibility.Node
				node, err = locator.Resolve(context.Background())
				found = node.Name
			}
			if !errors.Is(err, context.DeadlineExceeded) || found != "" || !backend.expired || fake.snapshotCalls != 1 {
				t.Fatalf("%s after provider read deadline: target=%q err=%v deadline=%t snapshots=%d", operation, found, err, backend.expired, fake.snapshotCalls)
			}
		})
	}
}

// The event and snapshot stand-ins deliberately ignore canceled contexts.
// Pre-canceled semantic reads must never query or reveal provider-owned data.
func TestAccessibilityLocatorDoesNotReadOrDiscloseSnapshotAfterPreCancellation(t *testing.T) {
	loc, fake := newLocatorWaitFixture(t, 1, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	matched, err := loc.Matches(ctx)
	if !errors.Is(err, context.Canceled) || len(matched) != 0 {
		t.Fatalf("canceled locator disclosed matches=%+v error=%v", matched, err)
	}
	if fake.snapshotCalls != 0 {
		t.Fatalf("canceled request dispatched %d provider snapshot reads", fake.snapshotCalls)
	}
	node, err := loc.Resolve(ctx)
	if !errors.Is(err, context.Canceled) || node.ID.BusName != "" {
		t.Fatalf("canceled locator disclosed target=%+v error=%v", node, err)
	}
	if fake.snapshotCalls != 0 {
		t.Fatalf("canceled resolve dispatched %d provider snapshot reads", fake.snapshotCalls)
	}
}

// A remote AT-SPI provider may answer after the caller canceled while the
// snapshot was in flight. The response must not be exposed even if complete.
type locatorCancelingSnapshot struct {
	*snapshotAccessibilityAutomationFake
	cancel context.CancelFunc
}

func (f *locatorCancelingSnapshot) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	result, err := f.snapshotAccessibilityAutomationFake.Snapshot(ctx, root, opts)
	f.cancel()
	return result, err
}

func TestAccessibilityLocatorRejectsLateCancellationDuringProviderSnapshot(t *testing.T) {
	loc, fake := newLocatorWaitFixture(t, 1, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loc.bundle.backend = &locatorCancelingSnapshot{snapshotAccessibilityAutomationFake: fake, cancel: cancel}
	matched, err := loc.Matches(ctx)
	if !errors.Is(err, context.Canceled) || len(matched) != 0 {
		t.Fatalf("canceled provider reply disclosed matches=%+v err=%v", matched, err)
	}
	if fake.snapshotCalls != 1 {
		t.Fatalf("fixture must cancel after a real snapshot, got %d calls", fake.snapshotCalls)
	}
}

type locatorCancelingSubscription struct {
	*snapshotAccessibilityAutomationFake
	cancel context.CancelFunc
}

func (f *locatorCancelingSubscription) Events(ctx context.Context, opts accessibility.EventOptions) (<-chan accessibility.Event, error) {
	events, err := f.snapshotAccessibilityAutomationFake.Events(ctx, opts)
	f.cancel()
	return events, err
}

func TestAccessibilityLocatorCancellationDuringEventSubscriptionSkipsProviderSnapshot(t *testing.T) {
	loc, fake := newLocatorWaitFixture(t, 1, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loc.bundle.backend = &locatorCancelingSubscription{snapshotAccessibilityAutomationFake: fake, cancel: cancel}
	matched, err := loc.Matches(ctx)
	if !errors.Is(err, context.Canceled) || len(matched) != 0 {
		t.Fatalf("canceled event subscription disclosed matches=%+v err=%v", matched, err)
	}
	if fake.snapshotCalls != 0 {
		t.Fatalf("canceled event setup issued %d forbidden snapshot reads", fake.snapshotCalls)
	}
}

type locatorCancelingApplication struct {
	*snapshotAccessibilityAutomationFake
	cancel context.CancelFunc
}

func (f *locatorCancelingApplication) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	app, err := f.snapshotAccessibilityAutomationFake.FindApplicationFresh(ctx, filter)
	f.cancel()
	return app, err
}

func TestAccessibilityLocatorCancellationDuringScopeResolutionSkipsProviderSnapshot(t *testing.T) {
	loc, fake := newLocatorWaitFixture(t, 1, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loc.bundle.backend = &locatorCancelingApplication{snapshotAccessibilityAutomationFake: fake, cancel: cancel}
	matched, err := loc.Matches(ctx)
	if !errors.Is(err, context.Canceled) || len(matched) != 0 {
		t.Fatalf("canceled scope lookup disclosed matches=%+v err=%v", matched, err)
	}
	if fake.snapshotCalls != 0 {
		t.Fatalf("canceled scope lookup issued %d forbidden snapshot reads", fake.snapshotCalls)
	}
}

type locatorRetryChangedApplicationBackend struct {
	*snapshotAccessibilityAutomationFake
	refreshes int
}

func (b *locatorRetryChangedApplicationBackend) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.refreshes++
	app, err := b.snapshotAccessibilityAutomationFake.FindApplicationFresh(ctx, filter)
	if b.refreshes == 2 {
		app.ID.BusName = "org.private.ReplacedApplication"
		app.ID.ObjectPath = "/replacement"
	}
	return app, err
}

// A rejected, not-sent mutation does not grant the caller lasting access to
// the original target once scope ownership changes during retry validation.
func TestAccessibilityLocatorRetrySuppressesOldReceiptOnScopeReplacement(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	fake.textErr = accessibility.ErrStaleGeneration
	backend := &locatorRetryChangedApplicationBackend{snapshotAccessibilityAutomationFake: fake}
	locator.bundle.backend = backend
	receipt, _, err := locator.FillAndWait(context.Background(), "private-value", Predicate("verified", func(context.Context) (bool, error) { return true, nil }))
	if !errors.Is(err, ErrManagedScopeChanged) || receipt.Node.ID.BusName != "" || receipt.Node.Name != "" || backend.refreshes != 2 || fake.textCount != 1 {
		t.Fatalf("scope replacement exposed stale private target: receipt=%+v err=%v refreshes=%d writes=%d", receipt, err, backend.refreshes, fake.textCount)
	}
}

type locatorRetryRevokingScope struct {
	*snapshotAccessibilityAutomationFake
	onRefresh  func()
	scopeCalls int
}

func (b *locatorRetryRevokingScope) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.scopeCalls++
	app, err := b.snapshotAccessibilityAutomationFake.FindApplicationFresh(ctx, filter)
	if b.scopeCalls == 2 && b.onRefresh != nil {
		b.onRefresh()
	}
	return app, err
}

// A stale, not-sent action may leave a receipt from the first resolution.
// Revocation during the required scope refresh cannot publish that receipt.
func TestAccessibilityLocatorRetryDoesNotDiscloseReceiptAfterScopeRevocation(t *testing.T) {
	for _, mode := range []string{"caller-cancel", "session-close"} {
		t.Run(mode, func(t *testing.T) {
			locator, fake := newLocatorWaitFixture(t, 1, false)
			fake.textErr = accessibility.ErrStaleGeneration
			backend := &locatorRetryRevokingScope{snapshotAccessibilityAutomationFake: fake}
			locator.bundle.backend = backend
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := context.Canceled
			if mode == "caller-cancel" {
				backend.onRefresh = cancel
			} else {
				want = ErrSessionClosed
				backend.onRefresh = func() {
					session := locator.bundle.session
					session.lifecycleMu.Lock()
					session.closed = true
					session.lifecycleMu.Unlock()
				}
			}
			receipt, _, err := locator.FillAndWait(ctx, "redacted", Predicate("done", func(context.Context) (bool, error) { return true, nil }))
			if !errors.Is(err, want) || receipt.Node.ID.BusName != "" || receipt.Action.Name != "" || backend.scopeCalls != 2 || fake.textCount != 1 {
				t.Fatalf("%s leaked first action receipt after revocation: receipt=%+v err=%v scopeCalls=%d writes=%d", mode, receipt, err, backend.scopeCalls, fake.textCount)
			}
		})
	}
}

type locatorScopeInvalidationBackend struct {
	*snapshotAccessibilityAutomationFake
	stream chan accessibility.Event
}

func (b *locatorScopeInvalidationBackend) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	b.stream = make(chan accessibility.Event, 1)
	return b.stream, nil
}
func (b *locatorScopeInvalidationBackend) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	app, err := b.snapshotAccessibilityAutomationFake.FindApplicationFresh(ctx, filter)
	b.stream <- accessibility.Event{Kind: "object:children-changed"}
	return app, err
}

// A semantic change during application-scope resolution invalidates the
// observation before snapshot dispatch; there is no reason to query that
// already-inconsistent scope or expose its target.
func TestAccessibilityLocatorRejectsScopeInvalidationBeforeSnapshot(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	backend := &locatorScopeInvalidationBackend{snapshotAccessibilityAutomationFake: fake}
	previous := locator.bundle.backend
	locator.bundle.backend = backend
	t.Cleanup(func() { locator.bundle.backend = previous })
	matches, err := locator.Matches(context.Background())
	if !errors.Is(err, accessibility.ErrObservationChanged) || len(matches) != 0 || fake.snapshotCalls != 0 {
		t.Fatalf("scope invalidation: matches=%+v err=%v snapshots=%d", matches, err, fake.snapshotCalls)
	}
}

type locatorClosingSnapshot struct {
	*snapshotAccessibilityAutomationFake
	after func()
}

func (f *locatorClosingSnapshot) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	result, err := f.snapshotAccessibilityAutomationFake.Snapshot(ctx, root, opts)
	if f.after != nil {
		f.after()
	}
	return result, err
}

// A semantic locator cannot publish nodes from a snapshot which finished
// only after its owning session was closed during provider execution.
func TestAccessibilityLocatorRejectsSnapshotAfterSessionClosure(t *testing.T) {
	for _, operation := range []string{"matches", "resolve"} {
		t.Run(operation, func(t *testing.T) {
			locator, fake := newLocatorWaitFixture(t, 1, false)
			session := locator.bundle.session
			backend := &locatorClosingSnapshot{snapshotAccessibilityAutomationFake: fake}
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			var found string
			var err error
			if operation == "matches" {
				var nodes []accessibility.Node
				nodes, err = locator.Matches(context.Background())
				if len(nodes) != 0 {
					found = nodes[0].Name
				}
			} else {
				var node accessibility.Node
				node, err = locator.Resolve(context.Background())
				found = node.Name
			}
			if !errors.Is(err, ErrSessionClosed) || found != "" || fake.snapshotCalls != 1 {
				t.Fatalf("%s disclosed locator target after session closure: target=%q err=%v snapshots=%d", operation, found, err, fake.snapshotCalls)
			}
		})
	}
}

type locatorApplicationAppearsBackend struct {
	*snapshotAccessibilityAutomationFake
	lookups int
}

func (b *locatorApplicationAppearsBackend) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.lookups++
	if b.lookups == 1 {
		return accessibility.Application{}, accessibility.ErrNotFound
	}
	return b.snapshotAccessibilityAutomationFake.FindApplicationFresh(ctx, filter)
}

func TestAccessibilityLocatorWaitFindsApplicationAfterInitialAbsence(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	backend := &locatorApplicationAppearsBackend{snapshotAccessibilityAutomationFake: fake}
	locator.bundle.backend = backend

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	node, evidence, err := locator.Wait(ctx, WaitEvery(time.Millisecond))
	if err != nil || node.Name != "Save" || node.ID.ObjectPath != "/save" {
		t.Fatalf("wait after scoped application appeared returned node=%+v error=%v", node, err)
	}
	if evidence.Evaluations != 2 || backend.lookups != 2 || fake.snapshotCalls != 1 {
		t.Fatalf("evaluations=%d application lookups=%d snapshots=%d, want two evaluations, two lookups, and one snapshot", evidence.Evaluations, backend.lookups, fake.snapshotCalls)
	}
}

func TestAccessibilityLocatorMatchesPropagatesApplicationProviderError(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	failure := errors.New("provider rejected scoped application lookup")
	backend := &locatorApplicationLookupFailureBackend{
		snapshotAccessibilityAutomationFake: fake,
		failure:                             failure,
	}
	locator.bundle.backend = backend

	matches, err := locator.Matches(context.Background())
	if !errors.Is(err, failure) || len(matches) != 0 {
		t.Fatalf("provider lookup failure returned matches=%+v error=%v", matches, err)
	}
	if backend.lookups != 1 || fake.snapshotCalls != 0 {
		t.Fatalf("application lookups=%d snapshots=%d, want one lookup and no snapshot", backend.lookups, fake.snapshotCalls)
	}
}

type locatorApplicationLookupFailureBackend struct {
	*snapshotAccessibilityAutomationFake
	failure error
	lookups int
}

func (b *locatorApplicationLookupFailureBackend) FindApplicationFresh(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.lookups++
	return accessibility.Application{}, b.failure
}

func newLocatorWaitFixture(t *testing.T, targets int, truncated bool) (*AccessibilityLocator, *snapshotAccessibilityAutomationFake) {
	t.Helper()
	root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/application", Generation: 11}
	app := accessibility.Application{Node: accessibility.Node{ID: root, Name: "Editor", Role: "application"}}
	nodes := []accessibility.Node{app.Node}
	for i := 0; i < targets; i++ {
		nodes = append(nodes, accessibility.Node{ID: accessibility.NodeID{BusName: root.BusName, ObjectPath: []string{"/save", "/save-duplicate"}[i], Generation: 11}, Parent: root, Role: "button", Name: "Save"})
	}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{apps: []accessibility.Application{app}, gen: 11}, accessibilityAutomationSpy: &accessibilityAutomationSpy{}},
		snapshot:                    accessibility.Snapshot{Root: app.Node, Nodes: nodes, Generation: 11, Truncated: truncated},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	t.Cleanup(func() { _ = session.Close() })
	locator := session.Accessibility.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, accessibility.Selector{Role: "button", Name: "Save"}, accessibility.SnapshotOptions{})
	return locator, fake
}
func TestAccessibilityLocatorWaitResolvesSingleAuthoritativeTargetAndPreservesEvidence(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	node, evidence, err := locator.Wait(context.Background(), WaitEvery(time.Millisecond))
	if err != nil || node.Name != "Save" || node.ID.ObjectPath != "/save" || evidence.Evaluations != 1 || fake.snapshotCalls != 1 || !fake.snapshotOptions.Fresh {
		t.Fatalf("one verified locator wait: node=%+v evidence=%+v calls=%d fresh=%v err=%v", node, evidence, fake.snapshotCalls, fake.snapshotOptions.Fresh, err)
	}
}
func TestAccessibilityLocatorWaitNotFoundPollsUntilCallerCancellation(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 0, false)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	node, evidence, err := locator.Wait(ctx, WaitEvery(time.Millisecond))
	if !errors.Is(err, context.DeadlineExceeded) || node.ID.BusName != "" || evidence.Evaluations < 2 || fake.snapshotCalls < 2 {
		t.Fatalf("transient no match was not polled until caller cancellation: node=%+v evidence=%+v calls=%d err=%v", node, evidence, fake.snapshotCalls, err)
	}
}
func TestAccessibilityLocatorWaitFailsClosedOnAmbiguousOrIncompleteSemanticSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name      string
		targets   int
		truncated bool
		want      error
	}{
		{"ambiguous", 2, false, accessibility.ErrAmbiguous},
		{"truncated", 1, true, accessibility.ErrIncompleteSnapshot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			locator, fake := newLocatorWaitFixture(t, tc.targets, tc.truncated)
			ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
			defer cancel()
			node, evidence, err := locator.Wait(ctx, WaitEvery(time.Millisecond))
			if !errors.Is(err, tc.want) || node.ID.BusName != "" || evidence.Evaluations != 1 || fake.snapshotCalls != 1 {
				t.Fatalf("unverifiable match should fail on first evaluation: node=%+v evidence=%+v snapshots=%d err=%v want=%v", node, evidence, fake.snapshotCalls, err, tc.want)
			}
		})
	}
}
func TestAccessibilityLocatorWaitRejectsInvalidCallersWithoutQueryingDesktop(t *testing.T) {
	locator, fake := newLocatorWaitFixture(t, 1, false)
	for _, tc := range []struct {
		name       string
		loc        *AccessibilityLocator
		newContext func() context.Context
		option     []WaitOption
		want       error
	}{
		{"nil locator", nil, context.Background, nil, ErrUnavailable},
		{"nil context", locator, func() context.Context { return nil }, nil, ErrInvalidArgument},
		{"invalid wait interval", locator, context.Background, []WaitOption{WaitEvery(-time.Millisecond)}, ErrInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := tc.loc.Wait(tc.newContext(), tc.option...)
			if !errors.Is(err, tc.want) {
				t.Fatalf("invalid wait input %v: %v", tc.want, err)
			}
		})
	}
	if fake.snapshotCalls != 0 {
		t.Fatalf("invalid wait inputs queried provider %d times", fake.snapshotCalls)
	}
}

type sequencedAccessibilityWaitBackend struct {
	*snapshotAccessibilityAutomationFake
	mu        sync.Mutex
	snapshots []accessibility.Snapshot
}

type missingApplicationWaitBackend struct {
	*sequencedAccessibilityWaitBackend
}

func (*missingApplicationWaitBackend) FindApplicationFresh(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	return accessibility.Application{}, accessibility.ErrNotFound
}

func (b *sequencedAccessibilityWaitBackend) Snapshot(_ context.Context, _ accessibility.NodeID, options accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.snapshotCalls++
	b.snapshotOptions = options
	index := b.snapshotCalls - 1
	if index >= len(b.snapshots) {
		index = len(b.snapshots) - 1
	}
	return b.snapshots[index], nil
}

func newSequencedAccessibilityWaitFixture(t *testing.T, snapshots []accessibility.Snapshot) (*AccessibilityLocator, *sequencedAccessibilityWaitBackend) {
	t.Helper()
	root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/application", Generation: 11}
	app := accessibility.Application{Node: accessibility.Node{ID: root, Name: "Editor", Role: "application"}, PID: 42}
	base := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{apps: []accessibility.Application{app}, gen: root.Generation},
			accessibilityAutomationSpy: &accessibilityAutomationSpy{},
		},
	}
	backend := &sequencedAccessibilityWaitBackend{snapshotAccessibilityAutomationFake: base, snapshots: snapshots}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	t.Cleanup(func() { _ = session.Close() })
	locator := session.Accessibility.LocatorForApplication(
		accessibility.ApplicationFilter{Name: "Editor"},
		accessibility.Selector{Role: "button", Name: "Save"},
		accessibility.SnapshotOptions{},
	)
	return locator, backend
}

func accessibilityWaitFixtureSnapshot(generation uint64, nodes ...accessibility.Node) accessibility.Snapshot {
	root := accessibility.Node{ID: accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/application", Generation: generation}, Name: "Editor", Role: "application"}
	flat := make([]accessibility.Node, 1, 1+len(nodes))
	flat[0] = root
	for i := range nodes {
		if nodes[i].ID.BusName == "" {
			nodes[i].ID.BusName = root.ID.BusName
		}
		if nodes[i].ID.Generation == 0 {
			nodes[i].ID.Generation = generation
		}
		if nodes[i].Parent.BusName == "" {
			nodes[i].Parent = root.ID
		}
		flat = append(flat, nodes[i])
	}
	return accessibility.Snapshot{Root: root, Nodes: flat, Generation: generation}
}

func waitSaveNode(path string) accessibility.Node {
	return accessibility.Node{
		ID:   accessibility.NodeID{ObjectPath: path},
		Role: "button", Name: "Save", States: []string{"focusable"},
	}
}

func TestAccessibilityLocatorWaitForStateEnabledAndScopedFocus(t *testing.T) {
	disabled := waitSaveNode("/save")
	disabled.States = append(disabled.States, "disabled")
	focused := waitSaveNode("/save")
	focused.Focused = true
	focused.States = append(focused.States, "focused", "enabled")
	focused.Enabled = true
	locator, backend := newSequencedAccessibilityWaitFixture(t, []accessibility.Snapshot{
		accessibilityWaitFixtureSnapshot(11, disabled),
		accessibilityWaitFixtureSnapshot(11, focused),
	})
	locator.selector.States = []string{"disabled"}
	ctx := context.Background()
	node, evidence, err := locator.WaitForState(ctx, AccessibilityWaitEnabled, time.Second, WaitEvery(time.Millisecond))
	if err != nil || !node.Enabled || evidence.Evaluations < 2 || backend.snapshotCalls < 2 || !backend.snapshotOptions.Fresh {
		t.Fatalf("enabled wait: node=%+v evidence=%+v snapshots=%d fresh=%t err=%v", node, evidence, backend.snapshotCalls, backend.snapshotOptions.Fresh, err)
	}

	locator, _ = newSequencedAccessibilityWaitFixture(t, []accessibility.Snapshot{
		accessibilityWaitFixtureSnapshot(11, waitSaveNode("/save")),
		accessibilityWaitFixtureSnapshot(11, focused),
	})
	node, _, err = locator.WaitForState(ctx, AccessibilityWaitFocused, time.Second, WaitEvery(time.Millisecond))
	if err != nil || !node.Focused || node.ID.ObjectPath != "/save" {
		t.Fatalf("focused wait returned a different control or false state: node=%+v err=%v", node, err)
	}
}

func TestAccessibilityLocatorWaitForStatePresenceAbsenceAndTimeout(t *testing.T) {
	locator, _ := newSequencedAccessibilityWaitFixture(t, []accessibility.Snapshot{
		accessibilityWaitFixtureSnapshot(11),
		accessibilityWaitFixtureSnapshot(11, waitSaveNode("/save")),
	})
	node, evidence, err := locator.WaitForState(context.Background(), AccessibilityWaitPresent, time.Second, WaitEvery(time.Millisecond))
	if err != nil || node.ID.ObjectPath != "/save" || evidence.Evaluations < 2 {
		t.Fatalf("present wait: node=%+v evidence=%+v err=%v", node, evidence, err)
	}

	locator, _ = newSequencedAccessibilityWaitFixture(t, []accessibility.Snapshot{accessibilityWaitFixtureSnapshot(11)})
	node, evidence, err = locator.WaitForState(context.Background(), AccessibilityWaitAbsent, time.Second)
	if err != nil || node.ID.BusName != "" || evidence.Evaluations != 1 {
		t.Fatalf("complete scoped absence: node=%+v evidence=%+v err=%v", node, evidence, err)
	}

	locator, _ = newSequencedAccessibilityWaitFixture(t, []accessibility.Snapshot{accessibilityWaitFixtureSnapshot(11, waitSaveNode("/save"))})
	_, _, err = locator.WaitForState(context.Background(), AccessibilityWaitAbsent, 20*time.Millisecond, WaitEvery(time.Millisecond))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("persistent target absence wait error = %v, want deadline", err)
	}
}

func TestAccessibilityLocatorWaitForStateAbsenceRequiresLiveScope(t *testing.T) {
	base := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{gen: 11},
			accessibilityAutomationSpy: &accessibilityAutomationSpy{},
		},
	}
	sequence := &sequencedAccessibilityWaitBackend{snapshotAccessibilityAutomationFake: base, snapshots: []accessibility.Snapshot{accessibilityWaitFixtureSnapshot(11)}}
	backend := &missingApplicationWaitBackend{sequencedAccessibilityWaitBackend: sequence}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	t.Cleanup(func() { _ = session.Close() })
	locator := session.Accessibility.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, accessibility.Selector{Role: "button", Name: "Save"}, accessibility.SnapshotOptions{})
	_, evidence, err := locator.WaitForState(context.Background(), AccessibilityWaitAbsent, time.Second, WaitEvery(time.Millisecond))
	if !errors.Is(err, ErrManagedScopeChanged) || !errors.Is(err, accessibility.ErrNotFound) || evidence.Evaluations != 1 || backend.snapshotCalls != 0 {
		t.Fatalf("missing application incorrectly proved absence: evidence=%+v snapshots=%d err=%v", evidence, backend.snapshotCalls, err)
	}
}

func TestAccessibilityLocatorWaitForStateVisibilityAndDisabled(t *testing.T) {
	visible := waitSaveNode("/save")
	visible.Visible = true
	visible.Enabled = true
	hiddenDisabled := waitSaveNode("/save")
	locator, _ := newSequencedAccessibilityWaitFixture(t, []accessibility.Snapshot{
		accessibilityWaitFixtureSnapshot(11, visible),
		accessibilityWaitFixtureSnapshot(11, hiddenDisabled),
	})
	node, _, err := locator.WaitForState(context.Background(), AccessibilityWaitHidden, time.Second, WaitEvery(time.Millisecond))
	if err != nil || node.Visible {
		t.Fatalf("hidden wait: node=%+v err=%v", node, err)
	}
	// A second wait confirms the enabled state is read from the resolved node,
	// rather than from an unrelated global focus or selector result.
	locator, _ = newSequencedAccessibilityWaitFixture(t, []accessibility.Snapshot{
		accessibilityWaitFixtureSnapshot(11, visible),
		accessibilityWaitFixtureSnapshot(11, hiddenDisabled),
	})
	node, _, err = locator.WaitForState(context.Background(), AccessibilityWaitDisabled, time.Second, WaitEvery(time.Millisecond))
	if err != nil || node.Enabled {
		t.Fatalf("disabled wait: node=%+v err=%v", node, err)
	}
}

func TestAccessibilityLocatorWaitForStateValueChangeUsesStableBaseline(t *testing.T) {
	initial := waitSaveNode("/save")
	initial.Role = "status bar"
	initial.Name = "Status"
	initial.Interfaces = []string{"org.a11y.atspi.Text"}
	initial.Text = "Saving"
	updated := initial
	updated.Text = "Saved"
	root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/application", Generation: 11}
	app := accessibility.Application{Node: accessibility.Node{ID: root, Name: "Editor", Role: "application"}, PID: 42}
	base := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{apps: []accessibility.Application{app}, gen: root.Generation},
			accessibilityAutomationSpy: &accessibilityAutomationSpy{},
		},
	}
	backend := &sequencedAccessibilityWaitBackend{snapshotAccessibilityAutomationFake: base, snapshots: []accessibility.Snapshot{
		accessibilityWaitFixtureSnapshot(11, initial), accessibilityWaitFixtureSnapshot(11, updated),
	}}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	t.Cleanup(func() { _ = session.Close() })
	locator := session.Accessibility.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, accessibility.Selector{Role: "status bar", Name: "Status"}, accessibility.SnapshotOptions{})
	node, evidence, err := locator.WaitForState(context.Background(), AccessibilityWaitValueChanged, time.Second, WaitEvery(time.Millisecond))
	if err != nil || node.Text != "Saved" || evidence.Evaluations < 2 {
		t.Fatalf("value changed wait: node=%+v evidence=%+v err=%v", node, evidence, err)
	}
}

func TestAccessibilityLocatorWaitForStateNumericValueChange(t *testing.T) {
	initial := waitSaveNode("/range")
	initial.Interfaces = []string{"org.a11y.atspi.Value"}
	initial.Value = &accessibility.ValueInfo{Current: 0.25}
	updated := initial
	updated.Value = &accessibility.ValueInfo{Current: 0.75}
	locator, _ := newSequencedAccessibilityWaitFixture(t, []accessibility.Snapshot{
		accessibilityWaitFixtureSnapshot(11, initial), accessibilityWaitFixtureSnapshot(11, updated),
	})
	// The shared test fixture's selector is button/name based; use the same
	// stable role/name identity while exercising AT-SPI Value.CurrentValue.
	locator.selector.Role = "button"
	locator.selector.Name = "Save"
	node, evidence, err := locator.WaitForState(context.Background(), AccessibilityWaitValueChanged, time.Second, WaitEvery(time.Millisecond))
	if err != nil || node.Value == nil || node.Value.Current != 0.75 || evidence.Evaluations < 2 {
		t.Fatalf("numeric value changed wait: node=%+v evidence=%+v err=%v", node, evidence, err)
	}
}

func TestAccessibilityLocatorWaitForStateFailsClosed(t *testing.T) {
	tests := []struct {
		name      string
		snapshots []accessibility.Snapshot
		want      error
	}{
		{
			name:      "ambiguous",
			snapshots: []accessibility.Snapshot{accessibilityWaitFixtureSnapshot(11, waitSaveNode("/save-a"), waitSaveNode("/save-b"))},
			want:      accessibility.ErrAmbiguous,
		},
		{
			name: "incomplete state set",
			snapshots: []accessibility.Snapshot{accessibilityWaitFixtureSnapshot(11, func() accessibility.Node {
				node := waitSaveNode("/save")
				node.Warnings = []string{"org.a11y.atspi.Accessible.GetState: temporary provider failure"}
				return node
			}())},
			want: accessibility.ErrIncompleteSnapshot,
		},
		{
			name:      "unsupported value",
			snapshots: []accessibility.Snapshot{accessibilityWaitFixtureSnapshot(11, waitSaveNode("/save"))},
			want:      ErrUnsupported,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			locator, backend := newSequencedAccessibilityWaitFixture(t, tc.snapshots)
			state := AccessibilityWaitEnabled
			if tc.name == "unsupported value" {
				state = AccessibilityWaitValueChanged
			}
			_, evidence, err := locator.WaitForState(context.Background(), state, time.Second, WaitEvery(time.Millisecond))
			if !errors.Is(err, tc.want) || evidence.Evaluations != 1 || backend.snapshotCalls != 1 {
				t.Fatalf("wait should fail closed once: evidence=%+v snapshots=%d err=%v want=%v", evidence, backend.snapshotCalls, err, tc.want)
			}
		})
	}
}

func TestAccessibilityLocatorWaitForStateRejectsLostScopeAndTargetReplacement(t *testing.T) {
	firstRoot := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/application", Generation: 11}
	secondRoot := accessibility.NodeID{BusName: "org.synthetic.Editor2", ObjectPath: "/application", Generation: 12}
	apps := []accessibility.Application{
		{Node: accessibility.Node{ID: firstRoot, Name: "Editor", Role: "application"}, PID: 42},
		{Node: accessibility.Node{ID: secondRoot, Name: "Editor", Role: "application"}, PID: 43},
	}
	base := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{apps: apps[:1], freshApps: apps, gen: 11},
			accessibilityAutomationSpy: &accessibilityAutomationSpy{},
		},
	}
	backend := &sequencedAccessibilityWaitBackend{snapshotAccessibilityAutomationFake: base, snapshots: []accessibility.Snapshot{
		accessibilityWaitFixtureSnapshot(11, waitSaveNode("/save")), accessibilityWaitFixtureSnapshot(12, waitSaveNode("/save")),
	}}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	firstSession := session
	t.Cleanup(func() { _ = firstSession.Close() })
	locator := session.Accessibility.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, accessibility.Selector{Role: "button", Name: "Save"}, accessibility.SnapshotOptions{})
	_, _, err := locator.WaitForState(context.Background(), AccessibilityWaitEnabled, time.Second, WaitEvery(time.Millisecond))
	if !errors.Is(err, ErrManagedScopeChanged) {
		t.Fatalf("changed application identity error = %v, want managed scope changed", err)
	}

	// The same application bus/path can still cross a generation boundary. A
	// control that appears after that boundary must not satisfy the old wait.
	firstGenerationRoot := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/application", Generation: 11}
	secondGenerationRoot := firstGenerationRoot
	secondGenerationRoot.Generation = 12
	apps = []accessibility.Application{
		{Node: accessibility.Node{ID: firstGenerationRoot, Name: "Editor", Role: "application"}, PID: 42},
		{Node: accessibility.Node{ID: secondGenerationRoot, Name: "Editor", Role: "application"}, PID: 42},
	}
	base = &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{apps: apps[:1], freshApps: apps, gen: 11},
			accessibilityAutomationSpy: &accessibilityAutomationSpy{},
		},
	}
	backend = &sequencedAccessibilityWaitBackend{snapshotAccessibilityAutomationFake: base, snapshots: []accessibility.Snapshot{
		accessibilityWaitFixtureSnapshot(11), accessibilityWaitFixtureSnapshot(12, waitSaveNode("/save")),
	}}
	session = NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	generationSession := session
	t.Cleanup(func() { _ = generationSession.Close() })
	locator = session.Accessibility.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, accessibility.Selector{Role: "button", Name: "Save"}, accessibility.SnapshotOptions{})
	_, _, err = locator.WaitForState(context.Background(), AccessibilityWaitPresent, time.Second, WaitEvery(time.Millisecond))
	if !errors.Is(err, ErrManagedScopeChanged) {
		t.Fatalf("changed application generation error = %v, want managed scope changed", err)
	}

	replacement := waitSaveNode("/save-replacement")
	locator, _ = newSequencedAccessibilityWaitFixture(t, []accessibility.Snapshot{
		accessibilityWaitFixtureSnapshot(11, waitSaveNode("/save")),
		accessibilityWaitFixtureSnapshot(11, replacement),
	})
	_, _, err = locator.WaitForState(context.Background(), AccessibilityWaitEnabled, time.Second, WaitEvery(time.Millisecond))
	if !errors.Is(err, ErrAccessibilityTargetChanged) {
		t.Fatalf("replaced target error = %v, want accessible target changed", err)
	}
}

func TestAccessibilityLocatorWaitForStateValidatesBeforeObservation(t *testing.T) {
	locator, backend := newSequencedAccessibilityWaitFixture(t, []accessibility.Snapshot{accessibilityWaitFixtureSnapshot(11, waitSaveNode("/save"))})
	for _, tc := range []struct {
		state   AccessibilityWaitState
		timeout time.Duration
		want    error
	}{
		{AccessibilityWaitState("bogus"), time.Second, ErrInvalidArgument},
		{AccessibilityWaitEnabled, 0, ErrInvalidArgument},
		{AccessibilityWaitEnabled, -time.Second, ErrInvalidArgument},
	} {
		_, _, err := locator.WaitForState(context.Background(), tc.state, tc.timeout)
		if !errors.Is(err, tc.want) {
			t.Errorf("WaitForState(%q, %s) error = %v, want %v", tc.state, tc.timeout, err, tc.want)
		}
	}
	if backend.snapshotCalls != 0 {
		t.Fatalf("invalid waits queried provider %d times", backend.snapshotCalls)
	}

	visibleOnlySession := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	t.Cleanup(func() { _ = visibleOnlySession.Close() })
	visibleOnlyLocator := visibleOnlySession.Accessibility.LocatorForApplication(
		accessibility.ApplicationFilter{Name: "Editor"},
		accessibility.Selector{Role: "button", Name: "Save"},
		accessibility.SnapshotOptions{VisibleOnly: true},
	)
	for _, state := range []AccessibilityWaitState{AccessibilityWaitAbsent, AccessibilityWaitHidden} {
		_, _, err := visibleOnlyLocator.WaitForState(context.Background(), state, time.Second)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("visible-only %s wait error = %v, want invalid argument", state, err)
		}
	}
	if backend.snapshotCalls != 0 {
		t.Fatalf("visible-only invalid waits queried provider %d times", backend.snapshotCalls)
	}
}

func TestAccessibilityLocatorWaitForStateReportsDisconnectedBus(t *testing.T) {
	locator, backend := newSequencedAccessibilityWaitFixture(t, []accessibility.Snapshot{accessibilityWaitFixtureSnapshot(11, waitSaveNode("/save"))})
	backend.eventErr = accessibility.ErrDisconnected
	_, evidence, err := locator.WaitForState(context.Background(), AccessibilityWaitEnabled, time.Second, WaitEvery(time.Millisecond))
	if !errors.Is(err, accessibility.ErrDisconnected) || evidence.Evaluations != 1 {
		t.Fatalf("disconnected accessibility bus error = %v with evidence %+v", err, evidence)
	}
}
