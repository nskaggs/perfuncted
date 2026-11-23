package perfuncted

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

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
	flat := []accessibility.Node{root}
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
