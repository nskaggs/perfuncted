package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/window"
)

type bundleAccessibilityFake struct {
	apps                 []accessibility.Application
	freshApps            []accessibility.Application
	gen                  uint64
	findCalls            int
	eventCalls           int
	eventErr             error
	eventDone            <-chan struct{}
	eventOptions         accessibility.EventOptions
	eventMu              sync.Mutex
	resolvedTargets      []accessibility.WindowTarget
	freshApplicationFind int
	freshWindowResolve   int
}

type accessibilityAutomationSpy struct {
	calls         []string
	exactAction   accessibility.Action
	invokedAction accessibility.Action
	invokeErr     error
	invokeErrors  []error
	invokeCount   int
	dispatchCheck func() error
	textErr       error
	textErrors    []error
	textCount     int
	textValue     string
}

type snapshotAccessibilityAutomationFake struct {
	*accessibilityAutomationFake
	snapshot             accessibility.Snapshot
	snapshotOptions      accessibility.SnapshotOptions
	snapshotErr          error
	snapshotCalls        int
	eventCallsAtSnapshot int
}

func (f *snapshotAccessibilityAutomationFake) Snapshot(_ context.Context, _ accessibility.NodeID, options accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	f.snapshotOptions = options
	f.snapshotCalls++
	f.eventMu.Lock()
	f.eventCallsAtSnapshot = f.eventCalls
	f.eventMu.Unlock()
	return f.snapshot, f.snapshotErr
}

func TestPublicAccessibilityHelpersRemainReachable(t *testing.T) {
	if condition := AccessibilityFocused(accessibility.SnapshotOptions{}); condition == nil {
		t.Fatal("AccessibilityFocused returned nil")
	}
}

func TestAccessibilityFindRequiresFreshCompleteSnapshot(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 3}
	target := accessibility.NodeID{BusName: "org.test", ObjectPath: "/save", Generation: 3}
	other := accessibility.NodeID{BusName: "org.test", ObjectPath: "/cancel", Generation: 3}
	tests := []struct {
		name      string
		truncated bool
		wantErr   bool
	}{
		{name: "complete snapshot returns all matching nodes"},
		{name: "truncated snapshot fails closed", truncated: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &snapshotAccessibilityAutomationFake{
				accessibilityAutomationFake: &accessibilityAutomationFake{
					bundleAccessibilityFake:    &bundleAccessibilityFake{gen: root.Generation},
					accessibilityAutomationSpy: &accessibilityAutomationSpy{},
				},
				snapshot: accessibility.Snapshot{
					Root: accessibility.Node{ID: root, Role: "application"},
					Nodes: []accessibility.Node{
						{ID: root, Role: "application"},
						{ID: target, Parent: root, Name: "Save", Role: "button"},
						{ID: other, Parent: root, Name: "Cancel", Role: "button"},
					},
					Generation: root.Generation,
					Truncated:  test.truncated,
				},
			}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			defer session.Close()

			got, err := session.Accessibility.Find(context.Background(), root, accessibility.Query{Name: "Save", Role: "button"}, accessibility.SnapshotOptions{})
			if test.wantErr {
				if !errors.Is(err, accessibility.ErrIncompleteSnapshot) {
					t.Fatalf("Find error = %v, want ErrIncompleteSnapshot", err)
				}
				if len(got) != 0 {
					t.Fatalf("Find returned partial matches %+v", got)
				}
			} else {
				if err != nil {
					t.Fatalf("Find: %v", err)
				}
				if len(got) != 1 || got[0].ID != target {
					t.Fatalf("Find = %+v, want only %v", got, target)
				}
			}
			if !backend.snapshotOptions.Fresh || backend.snapshotCalls != 1 {
				t.Fatalf("snapshot options/calls = %+v/%d, want one fresh snapshot", backend.snapshotOptions, backend.snapshotCalls)
			}
			if backend.findCalls != 0 {
				t.Fatalf("backend.Find calls = %d, want shared snapshot query path", backend.findCalls)
			}
		})
	}
}

func (s *accessibilityAutomationSpy) mark(name string) { s.calls = append(s.calls, name) }
func (s *accessibilityAutomationSpy) InvokeAction(context.Context, accessibility.NodeID, int32) error {
	s.mark("action")
	return nil
}
func (s *accessibilityAutomationSpy) InvokeActionByName(context.Context, accessibility.NodeID, string) (accessibility.Action, error) {
	s.mark("action-name")
	s.invokeCount++
	if s.dispatchCheck != nil {
		if err := s.dispatchCheck(); err != nil {
			return s.exactAction, err
		}
	}
	if s.invokeCount <= len(s.invokeErrors) {
		if err := s.invokeErrors[s.invokeCount-1]; err != nil {
			return s.exactAction, err
		}
	}
	if s.invokeErr != nil {
		return s.exactAction, s.invokeErr
	}
	if s.exactAction == (accessibility.Action{}) {
		s.exactAction = accessibility.Action{Index: 7, Name: "provider-actual", Description: "selected at invocation", KeyBinding: "Alt+S"}
	}
	s.invokedAction = s.exactAction
	return s.exactAction, nil
}
func (s *accessibilityAutomationSpy) InvokeDefaultAction(context.Context, accessibility.NodeID) (accessibility.Action, error) {
	s.mark("default-action")
	return accessibility.Action{Index: 0, Name: "default"}, nil
}
func (s *accessibilityAutomationSpy) GrabFocus(context.Context, accessibility.NodeID) error {
	s.mark("focus")
	return nil
}
func (s *accessibilityAutomationSpy) ScrollTo(context.Context, accessibility.NodeID, accessibility.ScrollType) error {
	s.mark("scroll")
	return nil
}
func (s *accessibilityAutomationSpy) ScrollToPoint(context.Context, accessibility.NodeID, accessibility.CoordType, int, int) error {
	s.mark("scroll-point")
	return nil
}
func (s *accessibilityAutomationSpy) SetPosition(context.Context, accessibility.NodeID, int, int, accessibility.CoordType) error {
	s.mark("set-position")
	return nil
}
func (s *accessibilityAutomationSpy) SetSize(context.Context, accessibility.NodeID, int, int) error {
	s.mark("set-size")
	return nil
}
func (s *accessibilityAutomationSpy) SetExtents(context.Context, accessibility.NodeID, int, int, int, int, accessibility.CoordType) error {
	s.mark("set-extents")
	return nil
}
func (s *accessibilityAutomationSpy) SetValue(context.Context, accessibility.NodeID, float64) error {
	s.mark("set-value")
	return nil
}
func (s *accessibilityAutomationSpy) SetTextContents(_ context.Context, _ accessibility.NodeID, value string) error {
	s.mark("text")
	s.textCount++
	s.textValue = value
	if s.textCount <= len(s.textErrors) {
		if err := s.textErrors[s.textCount-1]; err != nil {
			return err
		}
	}
	return s.textErr
}
func (s *accessibilityAutomationSpy) InsertText(context.Context, accessibility.NodeID, int32, string) error {
	s.mark("insert")
	return nil
}
func (s *accessibilityAutomationSpy) DeleteText(context.Context, accessibility.NodeID, int32, int32) error {
	s.mark("delete")
	return nil
}
func (s *accessibilityAutomationSpy) CopyText(context.Context, accessibility.NodeID, int32, int32) error {
	s.mark("copy")
	return nil
}
func (s *accessibilityAutomationSpy) CutText(context.Context, accessibility.NodeID, int32, int32) error {
	s.mark("cut")
	return nil
}
func (s *accessibilityAutomationSpy) PasteText(context.Context, accessibility.NodeID, int32) error {
	s.mark("paste")
	return nil
}
func (s *accessibilityAutomationSpy) SetCaretOffset(context.Context, accessibility.NodeID, int32) error {
	s.mark("caret")
	return nil
}
func (s *accessibilityAutomationSpy) SetTextSelection(context.Context, accessibility.NodeID, int32, int32, int32) error {
	s.mark("selection")
	return nil
}
func (s *accessibilityAutomationSpy) AddTextSelection(context.Context, accessibility.NodeID, int32, int32) error {
	s.mark("add-selection")
	return nil
}
func (s *accessibilityAutomationSpy) RemoveTextSelection(context.Context, accessibility.NodeID, int32) error {
	s.mark("remove-selection")
	return nil
}
func (s *accessibilityAutomationSpy) SetTextSelections(context.Context, accessibility.NodeID, []accessibility.DocumentTextSelection) error {
	s.mark("document-selections")
	return nil
}
func (s *accessibilityAutomationSpy) SelectChild(context.Context, accessibility.NodeID, int32) error {
	s.mark("select-child")
	return nil
}
func (s *accessibilityAutomationSpy) DeselectChild(context.Context, accessibility.NodeID, int32) error {
	s.mark("deselect-child")
	return nil
}
func (s *accessibilityAutomationSpy) SelectAll(context.Context, accessibility.NodeID) error {
	s.mark("select-all")
	return nil
}
func (s *accessibilityAutomationSpy) ClearSelection(context.Context, accessibility.NodeID) error {
	s.mark("clear-selection")
	return nil
}
func (s *accessibilityAutomationSpy) DeselectSelectedChild(context.Context, accessibility.NodeID) error {
	s.mark("deselect-selected-child")
	return nil
}
func (s *accessibilityAutomationSpy) SelectRow(context.Context, accessibility.NodeID, int32) error {
	s.mark("select-row")
	return nil
}
func (s *accessibilityAutomationSpy) DeselectRow(context.Context, accessibility.NodeID, int32) error {
	s.mark("deselect-row")
	return nil
}
func (s *accessibilityAutomationSpy) SelectColumn(context.Context, accessibility.NodeID, int32) error {
	s.mark("select-column")
	return nil
}
func (s *accessibilityAutomationSpy) DeselectColumn(context.Context, accessibility.NodeID, int32) error {
	s.mark("deselect-column")
	return nil
}

type accessibilityAutomationFake struct {
	*bundleAccessibilityFake
	*accessibilityAutomationSpy
}

type accessibilityReopenerFake struct {
	*bundleAccessibilityFake
	fresh        accessibility.Backend
	closed       bool
	disconnected bool
}

func (f *accessibilityReopenerFake) SupportedOperations() []string {
	return append(f.bundleAccessibilityFake.SupportedOperations(), "reopen")
}
func (f *accessibilityReopenerFake) Close() error { f.closed = true; return nil }
func (f *accessibilityReopenerFake) Reopen(context.Context) (accessibility.Backend, error) {
	return f.fresh, nil
}
func (f *accessibilityReopenerFake) Applications(ctx context.Context) ([]accessibility.Application, error) {
	if f.disconnected {
		return nil, accessibility.ErrDisconnected
	}
	return f.bundleAccessibilityFake.Applications(ctx)
}

func (*accessibilityAutomationFake) SupportedOperations() []string {
	return []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events", "outline", "invoke-action", "invoke-action-by-name", "invoke-default-action", "grab-focus", "scroll", "scroll-to-point", "set-position", "set-size", "set-extents", "set-value", "set-text-contents", "insert-text", "delete-text", "copy-text", "cut-text", "paste-text", "set-caret", "set-text-selection", "add-text-selection", "remove-text-selection", "set-document-text-selections", "select-child", "deselect-child", "select-all", "clear-selection", "deselect-selected-child", "select-row", "deselect-row", "select-column", "deselect-column", "window-root", "reopen"}
}

func (f *bundleAccessibilityFake) SupportedOperations() []string {
	return []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events", "window-root"}
}
func (f *bundleAccessibilityFake) Applications(context.Context) ([]accessibility.Application, error) {
	return append([]accessibility.Application(nil), f.apps...), nil
}
func (f *bundleAccessibilityFake) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	generation := f.gen
	if generation == 0 {
		generation = 1
	}
	node := accessibility.Node{ID: accessibility.NodeID{BusName: "org.test", ObjectPath: "/node", Generation: generation}, Name: "Save", Actions: []accessibility.Action{{Index: 1, Name: "press"}}}
	return accessibility.Snapshot{Nodes: []accessibility.Node{node}, Root: node, Generation: generation, Source: "fake"}, nil
}
func (f *bundleAccessibilityFake) Find(ctx context.Context, _ accessibility.NodeID, query accessibility.Query, _ accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	f.findCalls++
	snapshot, err := f.Snapshot(ctx, accessibility.NodeID{}, accessibility.SnapshotOptions{})
	if err != nil {
		return nil, err
	}
	return accessibility.FilterSnapshot(snapshot, query), nil
}
func (f *bundleAccessibilityFake) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	return accessibility.Node{Name: "focused"}, nil
}
func (f *bundleAccessibilityFake) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	return accessibility.Node{Name: "point"}, nil
}
func (f *bundleAccessibilityFake) Close() error { return nil }
func (f *bundleAccessibilityFake) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	return f.apps[0], nil
}
func (f *bundleAccessibilityFake) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	f.freshApplicationFind++
	if f.freshApplicationFind <= len(f.freshApps) {
		return f.freshApps[f.freshApplicationFind-1], nil
	}
	return f.FindApplication(ctx, filter)
}
func (f *bundleAccessibilityFake) Events(ctx context.Context, opts accessibility.EventOptions) (<-chan accessibility.Event, error) {
	f.eventMu.Lock()
	defer f.eventMu.Unlock()
	f.eventCalls++
	f.eventOptions = opts
	f.eventDone = ctx.Done()
	if f.eventErr != nil {
		return nil, f.eventErr
	}
	return make(chan accessibility.Event), nil
}

func (f *bundleAccessibilityFake) eventObservationState() (int, <-chan struct{}, accessibility.EventOptions) {
	f.eventMu.Lock()
	defer f.eventMu.Unlock()
	return f.eventCalls, f.eventDone, f.eventOptions
}
func (f *bundleAccessibilityFake) ResolveWindow(_ context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	f.resolvedTargets = append(f.resolvedTargets, target)
	if target.ID == "missing" {
		return accessibility.WindowScope{}, accessibility.ErrNotFound
	}
	if len(f.apps) == 0 {
		return accessibility.WindowScope{}, accessibility.ErrNotFound
	}
	app := f.apps[0]
	if app.ID.BusName == "" {
		app.ID = accessibility.NodeID{BusName: "org.test", ObjectPath: "/application", Generation: f.gen}
	}
	return accessibility.WindowScope{WindowID: target.ID, Root: accessibility.NodeID{BusName: "org.test", ObjectPath: "/window", Generation: f.gen}, ApplicationRoot: app.ID, Application: app, PID: app.PID, Generation: f.gen}, nil
}
func (f *bundleAccessibilityFake) ResolveWindowFresh(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	f.freshWindowResolve++
	return f.ResolveWindow(ctx, target)
}
func (f *bundleAccessibilityFake) Generation() uint64              { return f.gen }
func (f *bundleAccessibilityFake) Invalidate(accessibility.NodeID) { f.gen++ }

func TestAccessibilityBundleDelegatesOptionalSurface(t *testing.T) {
	fake := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "Firefox"}}}, gen: 7}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	app, err := session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{Name: "fire"})
	if err != nil || app.Name != "Firefox" {
		t.Fatalf("FindApplication = %+v, %v", app, err)
	}
	if got := session.Accessibility.Generation(); got != 7 {
		t.Fatalf("generation = %d", got)
	}
	session.Accessibility.Invalidate(accessibility.NodeID{})
	if got := session.Accessibility.Generation(); got != 8 {
		t.Fatalf("generation after invalidation = %d", got)
	}
}

func TestAccessibilityBundleDelegatesTypedAutomation(t *testing.T) {
	spy := &accessibilityAutomationSpy{}
	fake := &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 3}, accessibilityAutomationSpy: spy}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	id := accessibility.NodeID{BusName: "org.test", ObjectPath: "/button", Generation: 3}
	if err := session.Accessibility.FocusNode(context.Background(), id); err != nil {
		t.Fatalf("FocusNode: %v", err)
	}
	if err := session.Accessibility.SetValue(context.Background(), id, 0.5); err != nil {
		t.Fatalf("SetValue: %v", err)
	}
	if _, err := session.Accessibility.InvokeDefaultAction(context.Background(), id); err != nil {
		t.Fatalf("InvokeDefaultAction: %v", err)
	}
	if len(spy.calls) != 3 || spy.calls[0] != "focus" || spy.calls[1] != "set-value" || spy.calls[2] != "default-action" {
		t.Fatalf("automation calls = %v", spy.calls)
	}
}

func TestAccessibilityBundleInvokesSemanticActionWithReceipt(t *testing.T) {
	spy := &accessibilityAutomationSpy{}
	fake := &accessibilityAutomationFake{
		bundleAccessibilityFake:    &bundleAccessibilityFake{gen: 3},
		accessibilityAutomationSpy: spy,
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	receipt, err := session.Accessibility.InvokeSemanticAction(
		context.Background(),
		accessibility.NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 3},
		accessibility.Query{Name: "Save"},
		"press",
		accessibility.SnapshotOptions{},
	)
	if err != nil {
		t.Fatalf("InvokeSemanticAction: %v", err)
	}
	if receipt.Mechanism != "at-spi.action" || receipt.Action.Index != 7 || receipt.Action.Name != "provider-actual" || receipt.Generation != 3 || receipt.Dispatch != accessibility.DispatchAccepted || receipt.Outcome.Status != ActionOutcomeNotObserved {
		t.Fatalf("semantic action receipt = %+v", receipt)
	}
	if len(spy.calls) != 1 || spy.calls[0] != "action-name" {
		t.Fatalf("semantic action calls = %v", spy.calls)
	}
}

func TestAccessibilityBundleSemanticActionReceiptUsesInvocationMetadata(t *testing.T) {
	spy := &accessibilityAutomationSpy{exactAction: accessibility.Action{Index: 12, Name: "actual-name", Description: "fresh metadata"}}
	fake := &accessibilityAutomationFake{
		bundleAccessibilityFake:    &bundleAccessibilityFake{gen: 3},
		accessibilityAutomationSpy: spy,
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	receipt, err := session.Accessibility.InvokeSemanticAction(
		context.Background(),
		accessibility.NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 3},
		accessibility.Query{Name: "Save"},
		"press",
		accessibility.SnapshotOptions{},
	)
	if err != nil {
		t.Fatalf("InvokeSemanticAction: %v", err)
	}
	if receipt.Dispatch != accessibility.DispatchAccepted {
		t.Fatalf("dispatch state = %q, want accepted", receipt.Dispatch)
	}
	if receipt.Action != spy.invokedAction {
		t.Fatalf("receipt action = %+v, want invoked action %+v", receipt.Action, spy.invokedAction)
	}
	if len(spy.calls) != 1 || spy.calls[0] != "action-name" {
		t.Fatalf("calls = %v, want one exact invocation", spy.calls)
	}
}

func TestSessionAccessibilityActionWaitUsesIndependentPostcondition(t *testing.T) {
	spy := &accessibilityAutomationSpy{}
	fake := &accessibilityAutomationFake{
		bundleAccessibilityFake:    &bundleAccessibilityFake{gen: 3},
		accessibilityAutomationSpy: spy,
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	checks := 0
	receipt, evidence, err := session.InvokeAccessibilityActionAndWait(
		context.Background(),
		accessibility.NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 3},
		accessibility.Query{Name: "Save"},
		"press",
		accessibility.SnapshotOptions{},
		sessionCondition("postcondition", func(context.Context, *Session) (bool, error) {
			checks++
			return checks == 2, nil
		}),
		WaitEvery(time.Millisecond),
	)
	if err != nil {
		t.Fatalf("InvokeAccessibilityActionAndWait: %v", err)
	}
	if receipt.Mechanism != "at-spi.action" || evidence.Evaluations != 2 || receipt.Outcome.Status != ActionOutcomeVerified || receipt.Outcome.Condition == "" {
		t.Fatalf("receipt=%+v evidence=%+v", receipt, evidence)
	}
}

func TestInvokeSemanticActionRejectsTruncatedUniqueSnapshot(t *testing.T) {
	spy := &accessibilityAutomationSpy{}
	node := accessibility.Node{ID: accessibility.NodeID{BusName: "org.test", ObjectPath: "/save", Generation: 3}, Name: "Save"}
	backend := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{gen: 3},
			accessibilityAutomationSpy: spy,
		},
		snapshot: accessibility.Snapshot{Root: node, Nodes: []accessibility.Node{node}, Generation: 3, Truncated: true, TruncationReasons: []string{"max nodes"}},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	_, err := session.Accessibility.InvokeSemanticAction(
		context.Background(), accessibility.NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 3},
		accessibility.Query{Name: "Save"}, "press", accessibility.SnapshotOptions{},
	)
	if !errors.Is(err, accessibility.ErrIncompleteSnapshot) {
		t.Fatalf("InvokeSemanticAction error = %v, want incomplete snapshot", err)
	}
	if len(spy.calls) != 0 {
		t.Fatalf("actions = %v, want no dispatch", spy.calls)
	}
}

func TestInvokeSemanticActionRejectsAmbiguousSnapshotWithoutDispatch(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 3}
	first := accessibility.Node{ID: accessibility.NodeID{BusName: "org.test", ObjectPath: "/first", Generation: 3}, Parent: root, Name: "Save", Role: "button"}
	second := accessibility.Node{ID: accessibility.NodeID{BusName: "org.test", ObjectPath: "/second", Generation: 3}, Parent: root, Name: "Save", Role: "button"}
	spy := &accessibilityAutomationSpy{}
	backend := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{gen: 3},
			accessibilityAutomationSpy: spy,
		},
		snapshot: accessibility.Snapshot{Root: accessibility.Node{ID: root}, Nodes: []accessibility.Node{{ID: root}, first, second}, Generation: 3},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	_, err := session.Accessibility.InvokeSelectorSemanticAction(context.Background(), root, accessibility.Selector{Role: "button", Name: "Save"}, "press", accessibility.SnapshotOptions{})
	if !errors.Is(err, accessibility.ErrAmbiguous) {
		t.Fatalf("semantic action error = %v, want ambiguous target", err)
	}
	if spy.invokeCount != 0 {
		t.Fatalf("action dispatches = %d, want none for ambiguous target", spy.invokeCount)
	}
}

func TestAccessibilityLocatorReadsFreshSnapshotAndVerifiesActionOnce(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 3}
	target := accessibility.NodeID{BusName: "org.test", ObjectPath: "/save", Generation: 3}
	app := accessibility.Application{Node: accessibility.Node{ID: root, Name: "Editor"}}
	spy := &accessibilityAutomationSpy{}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{apps: []accessibility.Application{app}, gen: 3},
			accessibilityAutomationSpy: spy,
		},
		snapshot: accessibility.Snapshot{Root: app.Node, Nodes: []accessibility.Node{
			app.Node,
			{ID: target, Parent: root, Name: "Save", Role: "button", States: []string{"enabled"}},
		}, Generation: 3},
	}
	spy.dispatchCheck = func() error {
		_, eventDone, _ := fake.eventObservationState()
		if eventDone == nil {
			return errors.New("locator did not start event observation")
		}
		select {
		case <-eventDone:
			return errors.New("locator stopped event observation before dispatch")
		default:
			return nil
		}
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	locator := session.Accessibility.LocatorForApplication(
		accessibility.ApplicationFilter{Name: "Editor"},
		accessibility.Selector{Role: "button", Name: "Save", States: []string{"enabled"}},
		accessibility.SnapshotOptions{},
	)
	checks := 0
	receipt, evidence, err := locator.InvokeActionAndWait(context.Background(), "press", Predicate("saved", func(context.Context) (bool, error) {
		checks++
		return true, nil
	}))
	if err != nil {
		t.Fatalf("InvokeActionAndWait: %v", err)
	}
	if !fake.snapshotOptions.Fresh {
		t.Fatal("locator did not request a fresh provider snapshot")
	}
	eventCalls, _, eventOptions := fake.eventObservationState()
	if eventCalls < 1 || fake.eventCallsAtSnapshot != 1 {
		t.Fatalf("event subscriptions=%d at snapshot=%d, want locator observation active before snapshot", eventCalls, fake.eventCallsAtSnapshot)
	}
	requireSemanticEventCoverage(t, eventOptions)
	if fake.freshApplicationFind == 0 {
		t.Fatal("application locator did not resolve its scope from fresh provider state")
	}
	if receipt.Node.ID != target || receipt.Dispatch != accessibility.DispatchAccepted || receipt.Outcome.Status != ActionOutcomeVerified || receipt.Outcome.Condition != "saved" {
		t.Fatalf("verified action receipt = %+v", receipt)
	}
	if evidence.Evaluations != 1 || checks != 1 || spy.invokeCount != 1 {
		t.Fatalf("evaluations=%d postcondition checks=%d dispatches=%d, want one each", evidence.Evaluations, checks, spy.invokeCount)
	}
}

func TestAccessibilityLocatorFailsClosedWhenEventObservationIsUnavailable(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 3}
	app := accessibility.Application{Node: accessibility.Node{ID: root, Name: "Editor"}}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{apps: []accessibility.Application{app}, gen: 3, eventErr: accessibility.ErrUnsupported},
			accessibilityAutomationSpy: &accessibilityAutomationSpy{},
		},
		snapshot: accessibility.Snapshot{Root: app.Node, Nodes: []accessibility.Node{app.Node}, Generation: 3},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	locator := session.Accessibility.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, accessibility.Selector{Name: "Save"}, accessibility.SnapshotOptions{})
	if _, err := locator.Matches(context.Background()); !errors.Is(err, accessibility.ErrObservationChanged) || !errors.Is(err, accessibility.ErrUnsupported) {
		t.Fatalf("Matches error = %v, want observation-changed and unsupported causes", err)
	}
	if fake.snapshotCalls != 0 || fake.freshApplicationFind != 0 {
		t.Fatalf("scope resolutions=%d snapshots=%d, want failure before observing unguarded state", fake.freshApplicationFind, fake.snapshotCalls)
	}
	_, observationDone, eventOptions := fake.eventObservationState()
	requireSemanticEventCoverage(t, eventOptions)
	select {
	case <-observationDone:
	default:
		t.Fatal("failed event observation did not cancel its subscription context")
	}
}

func TestAccessibilityLocatorCancelsObservationAfterResolutionErrors(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 3}
	targets := []accessibility.Node{
		{ID: accessibility.NodeID{BusName: root.BusName, ObjectPath: "/save-one", Generation: root.Generation}, Parent: root, Name: "Save", Role: "button"},
		{ID: accessibility.NodeID{BusName: root.BusName, ObjectPath: "/save-two", Generation: root.Generation}, Parent: root, Name: "Save", Role: "button"},
	}
	for _, test := range []struct {
		name      string
		nodes     []accessibility.Node
		wantError error
	}{
		{name: "not found", wantError: accessibility.ErrNotFound},
		{name: "ambiguous", nodes: targets, wantError: accessibility.ErrAmbiguous},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := accessibility.Application{Node: accessibility.Node{ID: root, Name: "Editor", Role: "application"}}
			nodes := append([]accessibility.Node{app.Node}, test.nodes...)
			fake := &snapshotAccessibilityAutomationFake{
				accessibilityAutomationFake: &accessibilityAutomationFake{
					bundleAccessibilityFake:    &bundleAccessibilityFake{apps: []accessibility.Application{app}, gen: root.Generation},
					accessibilityAutomationSpy: &accessibilityAutomationSpy{},
				},
				snapshot: accessibility.Snapshot{Root: app.Node, Nodes: nodes, Generation: root.Generation},
			}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
			defer session.Close()
			locator := session.Accessibility.LocatorForApplication(
				accessibility.ApplicationFilter{Name: "Editor"},
				accessibility.Selector{Role: "button", Name: "Save"},
				accessibility.SnapshotOptions{},
			)
			if _, err := locator.Resolve(context.Background()); !errors.Is(err, test.wantError) {
				t.Fatalf("Resolve error = %v, want %v", err, test.wantError)
			}
			_, observationDone, _ := fake.eventObservationState()
			select {
			case <-observationDone:
			default:
				t.Fatal("resolution error left locator event subscription active")
			}
		})
	}
}

func requireSemanticEventCoverage(t *testing.T, options accessibility.EventOptions) {
	t.Helper()
	if !options.RequireSemanticCoverage {
		t.Fatal("locator subscription did not require semantic event coverage")
	}
}

func TestAccessibilityLocatorDoesNotActOnObservationChangedSnapshot(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 3}
	spy := &accessibilityAutomationSpy{}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{ID: root, Name: "Editor"}}}, gen: 3},
			accessibilityAutomationSpy: spy,
		},
		snapshotErr: accessibility.ErrObservationChanged,
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	checks := 0
	locator := session.Accessibility.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, accessibility.Selector{Role: "button", Name: "Save"}, accessibility.SnapshotOptions{})
	_, _, err := locator.InvokeActionAndWait(context.Background(), "press", Predicate("saved", func(context.Context) (bool, error) {
		checks++
		return true, nil
	}))
	if !errors.Is(err, accessibility.ErrObservationChanged) {
		t.Fatalf("locator error = %v, want observation changed", err)
	}
	if fake.snapshotCalls != locatorResolutionAttempts || !fake.snapshotOptions.Fresh {
		t.Fatalf("snapshot calls=%d options=%+v, want bounded fresh resolution", fake.snapshotCalls, fake.snapshotOptions)
	}
	if spy.invokeCount != 0 || checks != 0 {
		t.Fatalf("action dispatches=%d postcondition checks=%d, want no action before a stable snapshot", spy.invokeCount, checks)
	}
}

func TestAccessibilityWindowLocatorUsesFreshWindowCorrelation(t *testing.T) {
	manager := &accessibilityWindowManager{windows: []window.Info{{NativeID: "managed-window", Title: "Editor", PID: 77}}}
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 3}
	app := accessibility.Application{Node: accessibility.Node{ID: root, Name: "Editor"}, PID: 77}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{apps: []accessibility.Application{app}, gen: 3},
			accessibilityAutomationSpy: &accessibilityAutomationSpy{},
		},
		snapshot: accessibility.Snapshot{Root: app.Node, Nodes: []accessibility.Node{
			app.Node,
			{ID: accessibility.NodeID{BusName: "org.test", ObjectPath: "/save", Generation: 3}, Parent: root, Name: "Save", Role: "button"},
		}, Generation: 3},
	}
	session := NewSessionForTesting(nil, nil, manager, nil, nil, fake)
	defer session.Close()
	locator := session.Accessibility.LocatorForWindow("managed-window", accessibility.Selector{Role: "button", Name: "Save"}, accessibility.SnapshotOptions{})
	if _, err := locator.Resolve(context.Background()); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fake.freshWindowResolve == 0 || !fake.snapshotOptions.Fresh {
		t.Fatalf("fresh window resolutions=%d snapshot fresh=%t, want both fresh", fake.freshWindowResolve, fake.snapshotOptions.Fresh)
	}
}

func TestAccessibilityLocatorClickAndFillVerifySingleDispatch(t *testing.T) { //nolint:gocyclo // exercises both high-level action receipts against one fake session.
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 3}
	target := accessibility.NodeID{BusName: "org.test", ObjectPath: "/field", Generation: 3}
	app := accessibility.Application{Node: accessibility.Node{ID: root, Name: "Editor"}}
	spy := &accessibilityAutomationSpy{}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{apps: []accessibility.Application{app}, gen: 3},
			accessibilityAutomationSpy: spy,
		},
		snapshot: accessibility.Snapshot{Root: app.Node, Nodes: []accessibility.Node{
			app.Node,
			{ID: target, Parent: root, Name: "Email", Role: "text input", States: []string{"editable", "enabled"}},
		}, Generation: 3},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	locator := session.Accessibility.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, accessibility.Selector{Role: "text input", Name: "Email"}, accessibility.SnapshotOptions{})
	checks := 0
	receipt, evidence, err := locator.FillAndWait(context.Background(), "user@example.test", Predicate("filled", func(context.Context) (bool, error) {
		checks++
		return true, nil
	}))
	if err != nil {
		t.Fatalf("FillAndWait: %v", err)
	}
	if receipt.Node.ID != target || receipt.Operation != "set-text-contents" || receipt.Dispatch != accessibility.DispatchAccepted || receipt.Outcome.Status != ActionOutcomeVerified {
		t.Fatalf("fill receipt = %+v", receipt)
	}
	if spy.textCount != 1 || spy.textValue != "user@example.test" || checks != 1 || evidence.Evaluations != 1 {
		t.Fatalf("text dispatches=%d value=%q postcondition checks=%d evaluations=%d", spy.textCount, spy.textValue, checks, evidence.Evaluations)
	}

	spy.calls = nil
	checks = 0
	receipt, evidence, err = locator.ClickAndWait(context.Background(), Predicate("activated", func(context.Context) (bool, error) {
		checks++
		return true, nil
	}))
	if err != nil {
		t.Fatalf("ClickAndWait: %v", err)
	}
	if receipt.Operation != "invoke-action" || receipt.Action.Name != "default" || receipt.Dispatch != accessibility.DispatchAccepted || receipt.Outcome.Status != ActionOutcomeVerified {
		t.Fatalf("click receipt = %+v", receipt)
	}
	if spy.invokeCount != 0 || len(spy.calls) != 1 || spy.calls[0] != "default-action" || checks != 1 || evidence.Evaluations != 1 {
		t.Fatalf("click calls=%v named invocations=%d postcondition checks=%d evaluations=%d", spy.calls, spy.invokeCount, checks, evidence.Evaluations)
	}
}

func TestLocatorActionRetryDispatchMatrix(t *testing.T) {
	stale := fmt.Errorf("%w: stale target", accessibility.ErrStaleNode)
	for _, test := range []struct {
		name             string
		dispatch         accessibility.DispatchOutcome
		retryableNotSent bool
		err              error
		attempt          int
		wantRetry        bool
	}{
		{name: "not sent stale target", dispatch: accessibility.DispatchNotSent, retryableNotSent: true, err: stale, attempt: 0, wantRetry: true},
		{name: "unclassified pre-dispatch stale target", retryableNotSent: true, err: stale, attempt: 0, wantRetry: true},
		{name: "unknown outcome", dispatch: accessibility.DispatchUnknown, retryableNotSent: false, err: stale, attempt: 0},
		{name: "accepted outcome with error", dispatch: accessibility.DispatchAccepted, retryableNotSent: false, err: stale, attempt: 0},
		{name: "rejected outcome", dispatch: accessibility.DispatchRejected, retryableNotSent: false, err: stale, attempt: 0},
		{name: "not-sent unrelated error", dispatch: accessibility.DispatchNotSent, retryableNotSent: true, err: accessibility.ErrUnsupported, attempt: 0},
		{name: "attempt budget exhausted", dispatch: accessibility.DispatchNotSent, retryableNotSent: true, err: stale, attempt: locatorResolutionAttempts - 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempt := locatorDispatchAttempt{
				receipt: AccessibilityActionReceipt{Dispatch: test.dispatch}, err: test.err,
				retryableNotSent: test.retryableNotSent,
			}
			if got := locatorRetryAllowed(attempt, test.attempt); got != test.wantRetry {
				t.Fatalf("locatorRetryAllowed() = %t, want %t", got, test.wantRetry)
			}
		})
	}
}

func TestAccessibilityLocatorFillDoesNotRepeatUnknownDispatch(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 3}
	target := accessibility.NodeID{BusName: "org.test", ObjectPath: "/field", Generation: 3}
	app := accessibility.Application{Node: accessibility.Node{ID: root, Name: "Editor"}}
	spy := &accessibilityAutomationSpy{textErr: errors.New("provider disconnected after text request")}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{apps: []accessibility.Application{app}, gen: 3},
			accessibilityAutomationSpy: spy,
		},
		snapshot: accessibility.Snapshot{Root: app.Node, Nodes: []accessibility.Node{
			app.Node,
			{ID: target, Parent: root, Name: "Email", Role: "text input"},
		}, Generation: 3},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	locator := session.Accessibility.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, accessibility.Selector{Name: "Email"}, accessibility.SnapshotOptions{})
	postconditionChecks := 0
	receipt, _, err := locator.FillAndWait(context.Background(), "new value", Predicate("filled", func(context.Context) (bool, error) {
		postconditionChecks++
		return true, nil
	}))
	if err == nil {
		t.Fatal("FillAndWait succeeded after an uncertain text dispatch")
	}
	if receipt.Dispatch != accessibility.DispatchUnknown || spy.textCount != 1 || postconditionChecks != 0 {
		t.Fatalf("receipt=%+v text dispatches=%d postcondition checks=%d", receipt, spy.textCount, postconditionChecks)
	}
}

func TestAccessibilityLocatorDoesNotRepeatUncertainDispatch(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 3}
	target := accessibility.NodeID{BusName: "org.test", ObjectPath: "/save", Generation: 3}
	app := accessibility.Application{Node: accessibility.Node{ID: root, Name: "Editor"}}
	spy := &accessibilityAutomationSpy{exactAction: accessibility.Action{Index: 2, Name: "press"}}
	spy.invokeErr = &accessibility.ActionInvocationError{
		Action: spy.exactAction, Dispatch: accessibility.DispatchUnknown, Err: fmt.Errorf("socket closed after request write"),
	}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{apps: []accessibility.Application{app}, gen: 3},
			accessibilityAutomationSpy: spy,
		},
		snapshot: accessibility.Snapshot{Root: app.Node, Nodes: []accessibility.Node{
			app.Node,
			{ID: target, Parent: root, Name: "Save", Role: "button"},
		}, Generation: 3},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	locator := session.Accessibility.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, accessibility.Selector{Name: "Save"}, accessibility.SnapshotOptions{})
	postconditionChecks := 0
	receipt, _, err := locator.InvokeActionAndWait(context.Background(), "press", Predicate("done", func(context.Context) (bool, error) {
		postconditionChecks++
		return true, nil
	}))
	var invocationErr *accessibility.ActionInvocationError
	if !errors.As(err, &invocationErr) || invocationErr.Dispatch != accessibility.DispatchUnknown {
		t.Fatalf("action error = %v, want uncertain dispatch error", err)
	}
	if receipt.Dispatch != accessibility.DispatchUnknown || spy.invokeCount != 1 || postconditionChecks != 0 {
		t.Fatalf("receipt=%+v dispatches=%d postcondition checks=%d, want one unverified attempt and no wait", receipt, spy.invokeCount, postconditionChecks)
	}
}

func TestAccessibilityLocatorRetriesOnlyKnownNotSentStaleAction(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 3}
	target := accessibility.NodeID{BusName: "org.test", ObjectPath: "/save", Generation: 3}
	app := accessibility.Application{Node: accessibility.Node{ID: root, Name: "Editor"}}
	spy := &accessibilityAutomationSpy{exactAction: accessibility.Action{Index: 2, Name: "press"}}
	spy.invokeErrors = []error{
		&accessibility.ActionInvocationError{Action: spy.exactAction, Dispatch: accessibility.DispatchNotSent, Err: accessibility.ErrStaleGeneration},
		nil,
	}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{apps: []accessibility.Application{app}, gen: 3},
			accessibilityAutomationSpy: spy,
		},
		snapshot: accessibility.Snapshot{Root: app.Node, Nodes: []accessibility.Node{
			app.Node,
			{ID: target, Parent: root, Name: "Save", Role: "button"},
		}, Generation: 3},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	locator := session.Accessibility.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, accessibility.Selector{Name: "Save"}, accessibility.SnapshotOptions{})
	receipt, _, err := locator.InvokeActionAndWait(context.Background(), "press", Predicate("done", func(context.Context) (bool, error) { return true, nil }))
	if err != nil {
		t.Fatalf("InvokeActionAndWait: %v", err)
	}
	if spy.invokeCount != 2 || receipt.Dispatch != accessibility.DispatchAccepted || receipt.Outcome.Status != ActionOutcomeVerified {
		t.Fatalf("invocations=%d receipt=%+v, want bounded retry then verified success", spy.invokeCount, receipt)
	}
}

func TestAccessibilityLocatorRejectsStaleActionRetryOnChangedRootPath(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 3}
	replacementRoot := accessibility.NodeID{BusName: "org.test", ObjectPath: "/replacement-application", Generation: 3}
	target := accessibility.NodeID{BusName: "org.test", ObjectPath: "/save", Generation: 3}
	app := accessibility.Application{Node: accessibility.Node{ID: root, Name: "Editor"}, PID: 77}
	replacement := accessibility.Application{Node: accessibility.Node{ID: replacementRoot, Name: "Editor"}, PID: 77}
	spy := &accessibilityAutomationSpy{exactAction: accessibility.Action{Index: 2, Name: "press"}}
	spy.invokeErrors = []error{&accessibility.ActionInvocationError{
		Action: spy.exactAction, Dispatch: accessibility.DispatchNotSent, Err: accessibility.ErrStaleNode,
	}}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake: &bundleAccessibilityFake{
				apps: []accessibility.Application{app}, freshApps: []accessibility.Application{app, replacement}, gen: 3,
			},
			accessibilityAutomationSpy: spy,
		},
		snapshot: accessibility.Snapshot{Root: app.Node, Nodes: []accessibility.Node{
			app.Node,
			{ID: target, Parent: root, Name: "Save", Role: "button"},
		}, Generation: 3},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	locator := session.Accessibility.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, accessibility.Selector{Name: "Save"}, accessibility.SnapshotOptions{})
	postconditionChecks := 0
	_, _, err := locator.InvokeActionAndWait(context.Background(), "press", Predicate("saved", func(context.Context) (bool, error) {
		postconditionChecks++
		return true, nil
	}))
	if !errors.Is(err, ErrManagedScopeChanged) {
		t.Fatalf("InvokeActionAndWait error = %v, want ErrManagedScopeChanged", err)
	}
	if spy.invokeCount != 1 || fake.snapshotCalls != 1 || postconditionChecks != 0 {
		t.Fatalf("dispatches=%d snapshots=%d postcondition checks=%d, want one dispatch, one snapshot, no wait", spy.invokeCount, fake.snapshotCalls, postconditionChecks)
	}
}

func TestAccessibilityBundleExplicitReopenSwapsBackend(t *testing.T) {
	old := &accessibilityReopenerFake{bundleAccessibilityFake: &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "old"}}}, gen: 1}}
	fresh := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "fresh"}}}, gen: 2}
	old.fresh = fresh
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	defer session.Close()
	if err := session.Accessibility.Reopen(context.Background()); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	apps, err := session.Accessibility.Applications(context.Background())
	if err != nil || len(apps) != 1 || apps[0].Name != "fresh" {
		t.Fatalf("reopened applications = %+v err=%v", apps, err)
	}
	if !old.closed {
		t.Fatal("old accessibility backend was not closed after explicit reopen")
	}
}

func TestAccessibilityBundleExplicitReopenAfterDisconnect(t *testing.T) {
	old := &accessibilityReopenerFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 4}}
	fresh := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "rediscovered"}}}, gen: 6}
	old.fresh = fresh
	old.disconnected = true
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	defer session.Close()
	if _, err := session.Accessibility.Applications(context.Background()); !errors.Is(err, accessibility.ErrDisconnected) {
		t.Fatalf("disconnected applications error = %v, want disconnected", err)
	}
	if err := session.Accessibility.Reopen(context.Background()); err != nil {
		t.Fatalf("Reopen after disconnect: %v", err)
	}
	if got := session.Accessibility.Generation(); got != fresh.gen {
		t.Fatalf("reopened generation = %d, want %d", got, fresh.gen)
	}
	apps, err := session.Accessibility.Applications(context.Background())
	if err != nil || len(apps) != 1 || apps[0].Name != "rediscovered" {
		t.Fatalf("rediscovered applications = %+v, err=%v", apps, err)
	}
}

func TestAccessibilityBundleUnavailableIsTyped(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	_, err := session.Accessibility.Focused(context.Background(), accessibility.SnapshotOptions{})
	if err == nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Focused error = %v", err)
	}
}

type accessibilityWindowManager struct{ windows []window.Info }

func (m *accessibilityWindowManager) List(context.Context) ([]window.Info, error) {
	return append([]window.Info(nil), m.windows...), nil
}
func (m *accessibilityWindowManager) IterateWindows(context.Context) iter.Seq2[window.Info, error] {
	return func(yield func(window.Info, error) bool) {
		for _, info := range m.windows {
			if !yield(info, nil) {
				return
			}
		}
	}
}
func (m *accessibilityWindowManager) ActiveTitle(context.Context) (string, error) { return "", nil }
func (m *accessibilityWindowManager) Close() error                                { return nil }

func TestAccessibilityBundleCorrelatesApplicationWithWindow(t *testing.T) {
	manager := &accessibilityWindowManager{windows: []window.Info{{NativeID: "window-1", Title: "Firefox", PID: 77}}}
	fake := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "Firefox"}, PID: 77}}, gen: 1}
	session := NewSessionForTesting(nil, nil, manager, nil, nil, fake)
	defer session.Close()
	app, err := session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{WindowID: "window-1", WindowTitle: "Firefox"})
	if err != nil || app.PID != 77 {
		t.Fatalf("correlated application = %+v, %v", app, err)
	}
	if _, err := session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{WindowID: "missing"}); !errors.Is(err, accessibility.ErrNotFound) {
		t.Fatalf("missing window error = %v", err)
	}
}

func TestAccessibilityBundleRejectsAmbiguousWindowTitle(t *testing.T) {
	manager := &accessibilityWindowManager{windows: []window.Info{{NativeID: "one", Title: "Firefox"}, {NativeID: "two", Title: "Firefox"}}}
	fake := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "Firefox"}, PID: 77}}}
	session := NewSessionForTesting(nil, nil, manager, nil, nil, fake)
	defer session.Close()
	if _, err := session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{WindowTitle: "Firefox"}); !errors.Is(err, accessibility.ErrAmbiguous) {
		t.Fatalf("ambiguous window error = %v", err)
	}
}

func TestAccessibilityBundleRejectsConflictingWindowPIDBeforeCorrelation(t *testing.T) {
	manager := &accessibilityWindowManager{windows: []window.Info{{NativeID: "window-1", Title: "Firefox", PID: 77}}}
	fake := &bundleAccessibilityFake{apps: []accessibility.Application{
		{Node: accessibility.Node{Name: "Firefox"}, PID: 77},
		{Node: accessibility.Node{Name: "Other"}, PID: 99},
	}}
	session := NewSessionForTesting(nil, nil, manager, nil, nil, fake)
	defer session.Close()

	_, err := session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{
		WindowID: "window-1",
		PID:      99,
	})
	if !errors.Is(err, accessibility.ErrNotFound) {
		t.Fatalf("conflicting PID error = %v, want not found", err)
	}
	if len(fake.resolvedTargets) != 0 {
		t.Fatalf("resolver targets = %+v, want no correlation after PID conflict", fake.resolvedTargets)
	}
}
