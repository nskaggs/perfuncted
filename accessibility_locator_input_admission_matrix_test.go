package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

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
