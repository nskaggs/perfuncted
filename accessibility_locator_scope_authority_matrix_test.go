package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

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
