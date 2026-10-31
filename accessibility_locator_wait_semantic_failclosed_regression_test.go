package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

func newLocatorWaitFixture(t *testing.T, targets int, truncated bool) (*Session, *AccessibilityLocator, *snapshotAccessibilityAutomationFake) {
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
	return session, locator, fake
}
func TestAccessibilityLocatorWaitResolvesSingleAuthoritativeTargetAndPreservesEvidence(t *testing.T) {
	_, locator, fake := newLocatorWaitFixture(t, 1, false)
	node, evidence, err := locator.Wait(context.Background(), WaitEvery(time.Millisecond))
	if err != nil || node.Name != "Save" || node.ID.ObjectPath != "/save" || evidence.Evaluations != 1 || fake.snapshotCalls != 1 || !fake.snapshotOptions.Fresh {
		t.Fatalf("one verified locator wait: node=%+v evidence=%+v calls=%d fresh=%v err=%v", node, evidence, fake.snapshotCalls, fake.snapshotOptions.Fresh, err)
	}
}
func TestAccessibilityLocatorWaitNotFoundPollsUntilCallerCancellation(t *testing.T) {
	_, locator, fake := newLocatorWaitFixture(t, 0, false)
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
			_, locator, fake := newLocatorWaitFixture(t, tc.targets, tc.truncated)
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
	_, locator, fake := newLocatorWaitFixture(t, 1, false)
	for _, tc := range []struct {
		name   string
		loc    *AccessibilityLocator
		ctx    context.Context
		option []WaitOption
		want   error
	}{
		{"nil locator", nil, context.Background(), nil, ErrUnavailable},
		{"nil context", locator, nil, nil, ErrInvalidArgument},
		{"invalid wait interval", locator, context.Background(), []WaitOption{WaitEvery(-time.Millisecond)}, ErrInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := tc.loc.Wait(tc.ctx, tc.option...)
			if !errors.Is(err, tc.want) {
				t.Fatalf("invalid wait input %v: %v", tc.want, err)
			}
		})
	}
	if fake.snapshotCalls != 0 {
		t.Fatalf("invalid wait inputs queried provider %d times", fake.snapshotCalls)
	}
}
