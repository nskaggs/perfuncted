package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
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
					_, _, err = loc.ClickAndWait(ctx, c, WaitEvery(time.Millisecond))
				case "locator-fill":
					_, _, err = loc.FillAndWait(ctx, "new", c, WaitEvery(time.Millisecond))
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
