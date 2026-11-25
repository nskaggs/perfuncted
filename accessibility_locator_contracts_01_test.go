package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/accessibility"
	"testing"
)

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
