package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

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
