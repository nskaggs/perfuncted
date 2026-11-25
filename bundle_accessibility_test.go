package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/window"
	"iter"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type closingActionReceiptBackend struct {
	*accessibilityAutomationFake
	after func()
	calls int
}

func (b *closingActionReceiptBackend) InvokeActionByName(context.Context, accessibility.NodeID, string) (accessibility.Action, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Action{Index: 3, Name: "private-action"}, nil
}
func (b *closingActionReceiptBackend) InvokeDefaultAction(context.Context, accessibility.NodeID) (accessibility.Action, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Action{Index: 5, Name: "private-default"}, nil
}

// A provider may return the selected action after the session has closed;
// its acknowledged result must not leak action metadata or report success.
func TestAccessibilityActionReceiptRejectsClosedSession(t *testing.T) {
	for _, name := range []string{"by-name", "default"} {
		t.Run(name, func(t *testing.T) {
			backend := &closingActionReceiptBackend{accessibilityAutomationFake: &accessibilityAutomationFake{
				bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, accessibilityAutomationSpy: &accessibilityAutomationSpy{},
			}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			var action accessibility.Action
			var err error
			id := accessibility.NodeID{BusName: "org.synthetic", ObjectPath: "/button", Generation: 1}
			if name == "by-name" {
				action, err = session.Accessibility.InvokeActionByName(context.Background(), id, "press")
			} else {
				action, err = session.Accessibility.InvokeDefaultAction(context.Background(), id)
			}
			if !errors.Is(err, ErrSessionClosed) || action.Name != "" || backend.calls != 1 {
				t.Fatalf("%s receipt after session closure: action=%+v err=%v calls=%d", name, action, err, backend.calls)
			}
		})
	}
}

type directOutlineAccessibilityProbe struct {
	*snapshotAccessibilityAutomationFake
	calls       int
	gotRoot     accessibility.NodeID
	gotSnapshot accessibility.SnapshotOptions
	gotOutline  accessibility.OutlineOptions
	response    accessibility.Outline
	failure     error
}

func (p *directOutlineAccessibilityProbe) Outline(_ context.Context, root accessibility.NodeID, snap accessibility.SnapshotOptions, opts accessibility.OutlineOptions) (accessibility.Outline, error) {
	p.calls++
	p.gotRoot = root
	p.gotSnapshot = snap
	p.gotOutline = opts
	return p.response, p.failure
}

// An explicitly exposed provider Outline must be used instead of a second
// snapshot traversal. Provider errors must be surfaced without silently
// replacing a failed authoritative outline with potentially stale data.
func TestAccessibilityBundleDirectOutlineProviderPreservesScopeAndFailures(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/editor", Generation: 11}
	snapshotOpts := accessibility.SnapshotOptions{MaxDepth: 4, MaxNodes: 16, Fresh: true}
	outlineOpts := accessibility.OutlineOptions{MaxDepth: 2, MaxNodes: 8}
	underlying := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 11}, accessibilityAutomationSpy: &accessibilityAutomationSpy{}},
	}
	p := &directOutlineAccessibilityProbe{snapshotAccessibilityAutomationFake: underlying, response: accessibility.Outline{Root: accessibility.OutlineNode{ID: root}, Generation: 11, Truncated: true, ProviderErrors: 2, Warnings: []string{"synthetic provider incomplete"}}}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, p)
	defer session.Close()
	owner := session.Accessibility.backend
	session.Accessibility.backend = p
	defer func() { session.Accessibility.backend = owner }()
	actual, err := session.Accessibility.Outline(context.Background(), root, snapshotOpts, outlineOpts)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Generation != 11 || !actual.Truncated || actual.ProviderErrors != 2 || len(actual.Warnings) != 1 || actual.Root.ID != root {
		t.Fatalf("provider outline lost truncation or provenance: %+v", actual)
	}
	if p.calls != 1 || p.gotRoot != root || !reflect.DeepEqual(p.gotSnapshot, snapshotOpts) || p.gotOutline != outlineOpts || underlying.snapshotCalls != 0 {
		t.Fatalf("provider delegation calls=%d root=%+v snapshotOpts=%+v outlineOpts=%+v fallbackCalls=%d", p.calls, p.gotRoot, p.gotSnapshot, p.gotOutline, underlying.snapshotCalls)
	}
	failure := errors.New("synthetic provider refused scoped outline")
	p.failure = failure
	actual, err = session.Accessibility.Outline(context.Background(), root, snapshotOpts, outlineOpts)
	if !errors.Is(err, failure) || p.calls != 2 || underlying.snapshotCalls != 0 {
		t.Fatalf("provider failure was hidden or retried as unscoped snapshot: err=%v calls=%d fallback=%d", err, p.calls, underlying.snapshotCalls)
	}
}

func semanticActionCoverageFixture(spy *accessibilityAutomationSpy) (*Session, accessibility.NodeID) {
	root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/app", Generation: 19}
	target := accessibility.NodeID{BusName: root.BusName, ObjectPath: "/app/save", Generation: root.Generation}
	app := accessibility.Node{ID: root, Role: "application", Name: "Editor"}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{gen: root.Generation},
			accessibilityAutomationSpy: spy,
		},
		snapshot: accessibility.Snapshot{
			Root: app,
			Nodes: []accessibility.Node{
				app,
				{ID: target, Parent: root, Role: "button", Name: "Save", Actions: []accessibility.Action{{Index: 7, Name: "press"}}},
			},
			Generation: root.Generation,
		},
	}
	return NewSessionForTesting(nil, nil, nil, nil, nil, fake), root
}

func TestAccessibilitySemanticActionDoesNotInventReceiptForProviderError(t *testing.T) {
	providerErr := errors.New("provider rejected action before dispatch")
	spy := &accessibilityAutomationSpy{
		exactAction: accessibility.Action{Index: 7, Name: "press"},
		invokeErr:   providerErr,
	}
	session, root := semanticActionCoverageFixture(spy)
	defer session.Close()

	receipt, err := session.Accessibility.InvokeSemanticAction(
		context.Background(), root, accessibility.Query{Name: "Save", Role: "button"}, "press", accessibility.SnapshotOptions{},
	)
	if !errors.Is(err, providerErr) {
		t.Fatalf("InvokeSemanticAction error = %v, want provider error", err)
	}
	if receipt.Node.ID != (accessibility.NodeID{}) || receipt.Action != (accessibility.Action{}) || receipt.Dispatch != "" {
		t.Fatalf("unclassified provider failure produced a dispatch receipt: %+v", receipt)
	}
	if spy.invokeCount != 1 {
		t.Fatalf("provider action calls = %d, want one", spy.invokeCount)
	}
}

func TestAccessibilitySemanticActionSuppressesReceiptWhenCallerCancelsDuringFailure(t *testing.T) {
	providerErr := errors.New("provider returned private action metadata")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spy := &accessibilityAutomationSpy{exactAction: accessibility.Action{Index: 7, Name: "private-action"}}
	spy.dispatchCheck = func() error {
		cancel()
		return &accessibility.ActionInvocationError{
			Action: spy.exactAction, Dispatch: accessibility.DispatchNotSent, Err: providerErr,
		}
	}
	session, root := semanticActionCoverageFixture(spy)
	defer session.Close()

	receipt, err := session.Accessibility.InvokeSemanticAction(
		ctx, root, accessibility.Query{Name: "Save", Role: "button"}, "press", accessibility.SnapshotOptions{},
	)
	if !errors.Is(err, context.Canceled) || errors.Is(err, providerErr) {
		t.Fatalf("InvokeSemanticAction error = %v, want only caller cancellation", err)
	}
	if receipt.Node.ID != (accessibility.NodeID{}) || receipt.Action != (accessibility.Action{}) || receipt.Dispatch != "" {
		t.Fatalf("canceled action exposed provider metadata: %+v", receipt)
	}
	if spy.invokeCount != 1 {
		t.Fatalf("provider action calls = %d, want one", spy.invokeCount)
	}
}

func TestAccessibilityEventsRejectNilContextBeforeProviderDispatch(t *testing.T) {
	provider := &bundleAccessibilityFake{gen: 1}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, provider)
	defer session.Close()

	stream, err := session.Accessibility.Events(nil, accessibility.EventOptions{})
	if stream != nil || err == nil {
		t.Fatalf("nil-context event request returned stream=%v err=%v", stream, err)
	}
	if provider.eventCalls != 0 {
		t.Fatalf("provider received %d event requests with nil caller context", provider.eventCalls)
	}
}

type malformedEventSubscriptionBackend struct {
	*bundleAccessibilityFake
	stream <-chan accessibility.Event
	err    error
	calls  int
}

func (b *malformedEventSubscriptionBackend) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	b.calls++
	return b.stream, b.err
}

// A failed subscription cannot publish a provider-owned stream; conversely
// success without a stream must not give consumers a permanently dead channel.
func TestAccessibilityBundleRejectsMalformedSubscriptionResults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		stream     bool
		backendErr error
		want       error
	}{
		{name: "nil-stream", want: accessibility.ErrUnsupported},
		{name: "stream-with-error", stream: true, backendErr: errors.New("provider setup failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &malformedEventSubscriptionBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, err: tc.backendErr}
			if tc.stream {
				backend.stream = make(chan accessibility.Event, 1)
			}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			stream, err := session.Accessibility.Events(context.Background(), accessibility.EventOptions{})
			want := tc.want
			if want == nil {
				want = tc.backendErr
			}
			if stream != nil || !errors.Is(err, want) || backend.calls != 1 {
				t.Fatalf("%s malformed provider result published stream=%v err=%v calls=%d", tc.name, stream, err, backend.calls)
			}
		})
	}
}

type revokingAccessibilityEventSetup struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *revokingAccessibilityEventSetup) Events(_ context.Context, _ accessibility.EventOptions) (<-chan accessibility.Event, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	events := make(chan accessibility.Event, 1)
	events <- accessibility.Event{Kind: "private-event"}
	return events, nil
}

func TestAccessibilityEventsRefuseUnauthorizedSubscriptionResults(t *testing.T) {
	for _, mode := range []string{"pre-cancel", "late-cancel", "session-close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &revokingAccessibilityEventSetup{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			oldBackend := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = oldBackend })
			want := context.Canceled
			switch mode {
			case "pre-cancel":
				cancel()
			case "late-cancel":
				backend.after = cancel
			case "session-close":
				want = ErrSessionClosed
				backend.after = func() {
					session.lifecycleMu.Lock()
					session.closed = true
					session.lifecycleMu.Unlock()
				}
			}
			stream, err := session.Accessibility.Events(ctx, accessibility.EventOptions{})
			wantCalls := 1
			if mode == "pre-cancel" {
				wantCalls = 0
			}
			if !errors.Is(err, want) || stream != nil || backend.calls != wantCalls {
				t.Fatalf("stream=%v err=%v calls=%d; want nil stream, %v and %d calls", stream, err, backend.calls, want, wantCalls)
			}
		})
	}
}

type closingAccessibilityFindSnapshot struct {
	*bundleAccessibilityFake
	after func()
	calls int
	root  accessibility.NodeID
}

func (b *closingAccessibilityFindSnapshot) Snapshot(_ context.Context, _ accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	node := accessibility.Node{ID: b.root, Role: "application"}
	return accessibility.Snapshot{Root: node, Nodes: []accessibility.Node{node, {ID: accessibility.NodeID{BusName: b.root.BusName, ObjectPath: "/secret", Generation: b.root.Generation}, Parent: b.root, Role: "button", Name: "private-button"}}, Generation: b.root.Generation}, nil
}

// A completed backend traversal cannot expose private accessibility matches
// after session ownership was revoked, whether the caller requests all matches
// or a unique semantic selector.
func TestAccessibilityFindDiscardsResultsAfterSessionClosure(t *testing.T) {
	for _, name := range []string{"find", "find-one-selector"} {
		t.Run(name, func(t *testing.T) {
			root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/app", Generation: 1}
			backend := &closingAccessibilityFindSnapshot{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, root: root}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			var nameOut string
			var err error
			if name == "find" {
				var got []accessibility.Node
				got, err = session.Accessibility.Find(context.Background(), root, accessibility.Query{Name: "private-button", Role: "button"}, accessibility.SnapshotOptions{})
				if len(got) != 0 {
					nameOut = got[0].Name
				}
			} else {
				var got accessibility.Node
				got, err = session.Accessibility.FindOneSelector(context.Background(), root, accessibility.Selector{Name: "private-button", Role: "button"}, accessibility.SnapshotOptions{})
				nameOut = got.Name
			}
			if !errors.Is(err, ErrSessionClosed) || nameOut != "" || backend.calls != 1 {
				t.Fatalf("find %s after closure: node=%q err=%v backend calls=%d", name, nameOut, err, backend.calls)
			}
		})
	}
}

type closingGenerationReadBackend struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *closingGenerationReadBackend) Generation() uint64 {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return 71
}

// A generation read is an authorization-sensitive provider identity, and
// cannot remain valid if the session closes while the backend is reading it.
func TestAccessibilityGenerationDiscardsIdentityOnLateSessionClosure(t *testing.T) {
	backend := &closingGenerationReadBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 71}}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	t.Cleanup(func() { _ = session.Close() })
	owner := session.Accessibility.backend
	session.Accessibility.backend = backend
	t.Cleanup(func() { session.Accessibility.backend = owner })
	backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
	got := session.Accessibility.Generation()
	if got != 0 || backend.calls != 1 {
		t.Fatalf("generation leaked during closure: got=%d calls=%d", got, backend.calls)
	}
}

type closingAccessibilityIdentityBackend struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *closingAccessibilityIdentityBackend) ResolveWindow(_ context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.WindowScope{WindowID: target.ID, Root: accessibility.NodeID{BusName: "org.private", ObjectPath: "/window", Generation: 1}}, nil
}
func (b *closingAccessibilityIdentityBackend) ResolveWindowFresh(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	return b.ResolveWindow(ctx, target)
}
func (b *closingAccessibilityIdentityBackend) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Application{Node: accessibility.Node{ID: accessibility.NodeID{BusName: "org.private", ObjectPath: "/app", Generation: 1}, Name: "private-app"}}, nil
}
func (b *closingAccessibilityIdentityBackend) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	return b.FindApplication(ctx, filter)
}

// A provider result cannot authorize a window or application identity after
// the session closes while that provider is executing.
func TestAccessibilityIdentityReadsDiscardResultsAfterSessionClosure(t *testing.T) {
	for _, name := range []string{"window", "fresh-window", "application", "fresh-application"} {
		t.Run(name, func(t *testing.T) {
			backend := &closingAccessibilityIdentityBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			var err error
			var leaked string
			switch name {
			case "window", "fresh-window":
				var scope accessibility.WindowScope
				if name == "window" {
					scope, err = session.Accessibility.AccessibilityWindow(context.Background(), accessibility.WindowTarget{ID: "win", Title: "Editor"})
				} else {
					scope, err = session.Accessibility.accessibilityWindow(context.Background(), accessibility.WindowTarget{ID: "win", Title: "Editor"}, true)
				}
				leaked = scope.Root.BusName
			case "application", "fresh-application":
				var app accessibility.Application
				if name == "application" {
					app, err = session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{Name: "private"})
				} else {
					app, err = session.Accessibility.FindApplicationFresh(context.Background(), accessibility.ApplicationFilter{Name: "private"})
				}
				leaked = app.Name
			}
			if !errors.Is(err, ErrSessionClosed) || leaked != "" || backend.calls != 1 {
				t.Fatalf("identity leaked: result=%q err=%v calls=%d", leaked, err, backend.calls)
			}
		})
	}
}

// An accessibility backend offered after its bundle has disappeared, or to a
// bundle not managed by the generation owner, must be closed immediately.
// Otherwise its event subscription and bus connection could outlive authority.
type rejectedAccessibilityBackend struct {
	*bundleAccessibilityFake
	closes int
}

func (b *rejectedAccessibilityBackend) Close() error {
	b.closes++
	return nil
}

func TestAccessibilityInstallBackendClosesUnownedCandidates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bundle *AccessibilityBundle
	}{
		{name: "nil bundle", bundle: nil},
		{name: "bundle without owner", bundle: &AccessibilityBundle{backend: &bundleAccessibilityFake{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := &rejectedAccessibilityBackend{bundleAccessibilityFake: &bundleAccessibilityFake{}}
			tc.bundle.installBackend(candidate)
			if candidate.closes != 1 {
				t.Fatalf("candidate close calls = %d, want one", candidate.closes)
			}
		})
	}
}

type manyMatchingAccessibilityBackend struct {
	*bundleAccessibilityFake
	root          accessibility.NodeID
	nodes         []accessibility.Node
	snapshotCalls int
}

func (b *manyMatchingAccessibilityBackend) Snapshot(_ context.Context, _ accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.snapshotCalls++
	if !opts.Fresh {
		return accessibility.Snapshot{}, errors.New("semantic selection must request a fresh snapshot")
	}
	return accessibility.Snapshot{Root: b.nodes[0], Nodes: b.nodes, Generation: b.root.Generation}, nil
}

// Candidate diagnostics are bounded to avoid a second provider traversal, but
// ambiguity must not disappear merely because more than 32 nodes match.
// All matching identities must remain distinguishable in the error evidence.
func TestAccessibilityFindOneSelectorRetainsLargeAmbiguityEvidence(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/application", Generation: 1}
	nodes := []accessibility.Node{{ID: root, Name: "Editor", Role: "application"}}
	for i := 0; i < 40; i++ {
		id := accessibility.NodeID{BusName: root.BusName, ObjectPath: fmt.Sprintf("/button%d", i), Generation: 1}
		nodes = append(nodes, accessibility.Node{ID: id, Parent: root, Name: "Save", Role: "button"})
	}
	backend := &manyMatchingAccessibilityBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, root: root, nodes: nodes}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	t.Cleanup(func() { _ = session.Close() })
	selector := accessibility.Selector{Name: "Save", Role: "button"}
	node, err := session.Accessibility.FindOneSelector(context.Background(), root, selector, accessibility.SnapshotOptions{})
	if !errors.Is(err, accessibility.ErrAmbiguous) || node.ID.BusName != "" || backend.snapshotCalls != 1 {
		t.Fatalf("ambiguity: node=%+v err=%v snapshots=%d", node, err, backend.snapshotCalls)
	}
	var matchErr *accessibility.MatchError
	if !errors.As(err, &matchErr) {
		t.Fatalf("missing candidate evidence: %v", err)
	}
	seen := map[accessibility.NodeID]bool{}
	for _, candidate := range matchErr.Candidates {
		if seen[candidate.ID] {
			t.Fatalf("duplicate candidate: %+v", candidate.ID)
		}
		seen[candidate.ID] = true
	}
	for _, target := range nodes[1:] {
		if !seen[target.ID] {
			t.Fatalf("ambiguity evidence missing matched identity: %+v", target.ID)
		}
	}
	if len(seen) != 40 {
		t.Fatalf("candidate evidence=%d, want 40", len(seen))
	}
}

// This backend simulates an AT-SPI provider that completes a successful read
// after the caller has canceled. Its values must never be disclosed by any of
// the high-level query entry points, including cached and fresh resolution.
type lateCanceledAccessibilityReadBackend struct {
	*bundleAccessibilityFake
	cancel context.CancelFunc
	calls  int
	root   accessibility.NodeID
}

func (b *lateCanceledAccessibilityReadBackend) SupportedOperations() []string {
	return append(b.bundleAccessibilityFake.SupportedOperations(), "outline")
}
func (b *lateCanceledAccessibilityReadBackend) finish() { b.calls++; b.cancel() }
func (b *lateCanceledAccessibilityReadBackend) Applications(context.Context) ([]accessibility.Application, error) {
	b.finish()
	return []accessibility.Application{{Node: accessibility.Node{ID: b.root, Name: "private-app"}}}, nil
}
func (b *lateCanceledAccessibilityReadBackend) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.finish()
	root := accessibility.Node{ID: b.root, Role: "application"}
	return accessibility.Snapshot{Root: root, Nodes: []accessibility.Node{root, {ID: accessibility.NodeID{BusName: b.root.BusName, ObjectPath: "/private", Generation: b.root.Generation}, Parent: b.root, Name: "private-button", Role: "button"}}, Generation: b.root.Generation}, nil
}
func (b *lateCanceledAccessibilityReadBackend) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	b.finish()
	return accessibility.Node{ID: b.root, Name: "private-focused"}, nil
}
func (b *lateCanceledAccessibilityReadBackend) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	b.finish()
	return accessibility.Node{ID: b.root, Name: "private-point"}, nil
}
func (b *lateCanceledAccessibilityReadBackend) ResolveWindow(context.Context, accessibility.WindowTarget) (accessibility.WindowScope, error) {
	b.finish()
	return accessibility.WindowScope{Root: b.root, WindowID: "private-window"}, nil
}
func (b *lateCanceledAccessibilityReadBackend) ResolveWindowFresh(c context.Context, t accessibility.WindowTarget) (accessibility.WindowScope, error) {
	return b.ResolveWindow(c, t)
}
func (b *lateCanceledAccessibilityReadBackend) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.finish()
	return accessibility.Application{Node: accessibility.Node{ID: b.root, Name: "private-app"}}, nil
}
func (b *lateCanceledAccessibilityReadBackend) FindApplicationFresh(c context.Context, f accessibility.ApplicationFilter) (accessibility.Application, error) {
	return b.FindApplication(c, f)
}

func TestAccessibilityReadResultsAreDiscardedAfterLateCallerCancellation(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.private.Test", ObjectPath: "/app", Generation: 1}
	selector := accessibility.Selector{Name: "private-button", Role: "button"}
	tests := []struct {
		name string
		read func(*AccessibilityBundle, context.Context) (bool, error)
	}{
		{"applications", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.Applications(c)
			return len(a) != 0, e
		}},
		{"snapshot", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.Snapshot(c, root, accessibility.SnapshotOptions{})
			return len(a.Nodes) != 0, e
		}},
		{"outline-fallback", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.Outline(c, root, accessibility.SnapshotOptions{}, accessibility.OutlineOptions{})
			return a.Root.ID.BusName != "" || len(a.Warnings) != 0, e
		}},
		{"find", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.Find(c, root, accessibility.Query{Name: "private-button", Role: "button"}, accessibility.SnapshotOptions{})
			return len(a) != 0, e
		}},
		{"find-one", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.FindOneSelector(c, root, selector, accessibility.SnapshotOptions{})
			return a.Name != "", e
		}},
		{"focused", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.Focused(c, accessibility.SnapshotOptions{})
			return a.Name != "", e
		}},
		{"at-point", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.AtPoint(c, 5, 6)
			return a.Name != "", e
		}},
		{"window", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.AccessibilityWindow(c, accessibility.WindowTarget{ID: "private-window"})
			return a.Root.BusName != "", e
		}},
		{"fresh-window", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.accessibilityWindow(c, accessibility.WindowTarget{ID: "private-window"}, true)
			return a.Root.BusName != "", e
		}},
		{"application", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.FindApplication(c, accessibility.ApplicationFilter{Name: "private"})
			return a.Name != "", e
		}},
		{"fresh-application", func(b *AccessibilityBundle, c context.Context) (bool, error) {
			a, e := b.FindApplicationFresh(c, accessibility.ApplicationFilter{Name: "private"})
			return a.Name != "", e
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &lateCanceledAccessibilityReadBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, cancel: cancel, root: root}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			exposed, err := tc.read(session.Accessibility, ctx)
			if !errors.Is(err, context.Canceled) || exposed || backend.calls != 1 {
				t.Fatalf("late-canceled %s: exposed=%v err=%v backend calls=%d", tc.name, exposed, err, backend.calls)
			}
		})
	}
}

type revokingAccessibilityMutations struct {
	*accessibilityAutomationFake
	after func()
	calls int
}

func (b *revokingAccessibilityMutations) InvokeAction(context.Context, accessibility.NodeID, int32) error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return nil
}
func (b *revokingAccessibilityMutations) ScrollTo(context.Context, accessibility.NodeID, accessibility.ScrollType) error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return nil
}
func (b *revokingAccessibilityMutations) SetTextContents(context.Context, accessibility.NodeID, string) error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return nil
}
func (b *revokingAccessibilityMutations) SelectAll(context.Context, accessibility.NodeID) error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return nil
}

// AT-SPI mutations must never dispatch on a canceled request or report
// success after the caller or owning session revoked authority, even when
// the provider ignores its context and returns nil.
func TestAccessibilityMutationsRespectAuthorityAtDispatchAndReceipt(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.synthetic", ObjectPath: "/element", Generation: 1}
	actions := []struct {
		name string
		run  func(*AccessibilityBundle, context.Context) error
	}{
		{"invoke-action", func(b *AccessibilityBundle, c context.Context) error { return b.InvokeAction(c, id, 1) }},
		{"scroll", func(b *AccessibilityBundle, c context.Context) error {
			return b.ScrollTo(c, id, accessibility.ScrollAnyWhere)
		}},
		{"set-text-contents", func(b *AccessibilityBundle, c context.Context) error {
			return b.ReplaceEditableText(c, id, "private text")
		}},
		{"select-all", func(b *AccessibilityBundle, c context.Context) error { return b.SelectAll(c, id) }},
	}
	for _, action := range actions {
		for _, mode := range []string{"pre-cancel", "late-cancel", "session-close"} {
			t.Run(action.name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				b := &revokingAccessibilityMutations{accessibilityAutomationFake: &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, accessibilityAutomationSpy: &accessibilityAutomationSpy{}}}
				session := NewSessionForTesting(nil, nil, nil, nil, nil, b)
				t.Cleanup(func() { _ = session.Close() })
				owner := session.Accessibility.backend
				session.Accessibility.backend = b
				t.Cleanup(func() { session.Accessibility.backend = owner })
				expected := context.Canceled
				switch mode {
				case "pre-cancel":
					cancel()
				case "late-cancel":
					b.after = cancel
				case "session-close":
					expected = ErrSessionClosed
					b.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
				}
				err := action.run(session.Accessibility, ctx)
				wantCalls := 1
				if mode == "pre-cancel" {
					wantCalls = 0
				}
				if !errors.Is(err, expected) || b.calls != wantCalls {
					t.Fatalf("error=%v calls=%d; want error %v calls %d", err, b.calls, expected, wantCalls)
				}
			})
		}
	}
}

type revokingAccessibilityNodeReads struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *revokingAccessibilityNodeReads) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Node{Name: "private-focused"}, nil
}
func (b *revokingAccessibilityNodeReads) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Node{Name: "private-point"}, nil
}

// Even if a provider completes successfully after session closure, the
// facade must not expose the focused/pointed accessible object's metadata.
func TestAccessibilityNodeReadsDoNotEscapeClosedSession(t *testing.T) {
	for _, name := range []string{"focused", "at-point"} {
		t.Run(name, func(t *testing.T) {
			backend := &revokingAccessibilityNodeReads{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			var node accessibility.Node
			var err error
			if name == "focused" {
				node, err = session.Accessibility.Focused(context.Background(), accessibility.SnapshotOptions{})
			} else {
				node, err = session.Accessibility.AtPoint(context.Background(), 14, 23)
			}
			if !errors.Is(err, ErrSessionClosed) || node.Name != "" || backend.calls != 1 {
				t.Fatalf("node leaked after session closure: node=%+v err=%v calls=%d", node, err, backend.calls)
			}
		})
	}
}

type restrictedBundleAccessibilityBackend struct {
	accessibility.Backend
	operations []string
}

func (b *restrictedBundleAccessibilityBackend) SupportedOperations() []string {
	return append([]string(nil), b.operations...)
}

type applicationFinderOnlyBundleBackend struct {
	*restrictedBundleAccessibilityBackend
	calls int
}

func (b *applicationFinderOnlyBundleBackend) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.calls++
	return accessibility.Application{}, nil
}

func installRestrictedAccessibilityBackend(t *testing.T, backend accessibility.Backend, manager *accessibilityWindowManager) *Session {
	t.Helper()
	session := NewSessionForTesting(nil, nil, manager, nil, nil, backend)
	owner := session.Accessibility.backend
	session.Accessibility.backend = backend
	t.Cleanup(func() {
		session.Accessibility.backend = owner
		_ = session.Close()
	})
	return session
}

func TestAccessibilityBundleRejectsAdvertisedMissingOptionalCapabilities(t *testing.T) {
	ctx := context.Background()
	windowManager := &accessibilityWindowManager{windows: []window.Info{{NativeID: "window-1", Title: "Editor"}}}
	newCore := func(extra ...string) *restrictedBundleAccessibilityBackend {
		base := &bundleAccessibilityFake{gen: 1}
		operations := append([]string(nil), base.SupportedOperations()...)
		operations = append(operations, extra...)
		return &restrictedBundleAccessibilityBackend{Backend: base, operations: operations}
	}

	t.Run("window resolver", func(t *testing.T) {
		backend := newCore()
		session := installRestrictedAccessibilityBackend(t, backend, windowManager)
		scope, err := session.Accessibility.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: "window-1"})
		if scope.WindowID != "" || scope.Root != (accessibility.NodeID{}) || !errors.Is(err, accessibility.ErrUnsupported) {
			t.Fatalf("window resolution returned scope=%+v err=%v, want unsupported", scope, err)
		}
	})

	t.Run("fresh window resolver", func(t *testing.T) {
		backend := newCore()
		session := installRestrictedAccessibilityBackend(t, backend, windowManager)
		scope, err := session.Accessibility.WindowRootFresh(ctx, "window-1")
		if scope.WindowID != "" || scope.Root != (accessibility.NodeID{}) || !errors.Is(err, accessibility.ErrUnsupported) {
			t.Fatalf("fresh window resolution returned scope=%+v err=%v, want unsupported", scope, err)
		}
	})

	t.Run("application finder", func(t *testing.T) {
		backend := newCore()
		session := installRestrictedAccessibilityBackend(t, backend, windowManager)
		app, err := session.Accessibility.FindApplication(ctx, accessibility.ApplicationFilter{})
		if app.Node.ID != (accessibility.NodeID{}) || app.Name != "" || !errors.Is(err, accessibility.ErrUnsupported) {
			t.Fatalf("application lookup returned app=%+v err=%v, want unsupported", app, err)
		}
	})

	t.Run("fresh application finder", func(t *testing.T) {
		backend := newCore()
		finder := &applicationFinderOnlyBundleBackend{restrictedBundleAccessibilityBackend: backend}
		session := installRestrictedAccessibilityBackend(t, finder, windowManager)
		app, err := session.Accessibility.FindApplicationFresh(ctx, accessibility.ApplicationFilter{})
		if app.Node.ID != (accessibility.NodeID{}) || app.Name != "" || !errors.Is(err, accessibility.ErrUnsupported) || finder.calls != 0 {
			t.Fatalf("fresh application lookup returned app=%+v err=%v calls=%d, want unsupported without fallback", app, err, finder.calls)
		}
	})

	t.Run("event source", func(t *testing.T) {
		backend := newCore()
		session := installRestrictedAccessibilityBackend(t, backend, windowManager)
		stream, err := session.Accessibility.Events(ctx, accessibility.EventOptions{})
		if stream != nil || !errors.Is(err, accessibility.ErrUnsupported) {
			t.Fatalf("event subscription returned stream=%v err=%v, want unsupported", stream, err)
		}
	})

	t.Run("automation", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			call func(*AccessibilityBundle) error
		}{
			{"invoke action", func(b *AccessibilityBundle) error { return b.InvokeAction(ctx, accessibility.NodeID{}, 0) }},
			{"default action", func(b *AccessibilityBundle) error {
				_, err := b.InvokeDefaultAction(ctx, accessibility.NodeID{})
				return err
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				backend := newCore("invoke-action", "invoke-default-action")
				session := installRestrictedAccessibilityBackend(t, backend, windowManager)
				if err := tc.call(session.Accessibility); !errors.Is(err, accessibility.ErrUnsupported) {
					t.Fatalf("automation call error = %v, want unsupported", err)
				}
			})
		}
	})
}

type closingOutlineReadBackend struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *closingOutlineReadBackend) SupportedOperations() []string {
	return append(b.bundleAccessibilityFake.SupportedOperations(), "outline")
}
func (b *closingOutlineReadBackend) Outline(context.Context, accessibility.NodeID, accessibility.SnapshotOptions, accessibility.OutlineOptions) (accessibility.Outline, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Outline{Warnings: []string{"private outline"}}, nil
}

type closingOutlineFallbackBackend struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *closingOutlineFallbackBackend) SupportedOperations() []string {
	return append(b.bundleAccessibilityFake.SupportedOperations(), "outline")
}
func (b *closingOutlineFallbackBackend) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Snapshot{Nodes: []accessibility.Node{{Name: "private node"}}}, nil
}

// Neither a provider-supplied outline nor an outline built from a snapshot
// may escape after the owning session has closed during backend execution.
func TestAccessibilityOutlineDiscardsResultAfterSessionClosure(t *testing.T) {
	for _, name := range []string{"provider", "fallback"} {
		t.Run(name, func(t *testing.T) {
			var backend accessibility.Backend
			var after *func()
			var calls *int
			if name == "provider" {
				probe := &closingOutlineReadBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
				backend = probe
				after = &probe.after
				calls = &probe.calls
			} else {
				probe := &closingOutlineFallbackBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
				backend = probe
				after = &probe.after
				calls = &probe.calls
			}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			*after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			result, err := session.Accessibility.Outline(context.Background(), accessibility.NodeID{}, accessibility.SnapshotOptions{}, accessibility.OutlineOptions{})
			if !errors.Is(err, ErrSessionClosed) || len(result.Warnings) != 0 || len(result.Root.Children) != 0 || *calls != 1 {
				t.Fatalf("outline after closure: result=%+v error=%v backend calls=%d", result, err, *calls)
			}
		})
	}
}

type precanceledAccessibilityBackend struct {
	*bundleAccessibilityFake
	calls int
}

func (b *precanceledAccessibilityBackend) SupportedOperations() []string {
	return append(b.bundleAccessibilityFake.SupportedOperations(), "outline")
}

func (b *precanceledAccessibilityBackend) Applications(ctx context.Context) ([]accessibility.Application, error) {
	b.calls++
	return b.bundleAccessibilityFake.Applications(ctx)
}
func (b *precanceledAccessibilityBackend) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.calls++
	return b.bundleAccessibilityFake.Snapshot(ctx, root, opts)
}
func (b *precanceledAccessibilityBackend) Focused(ctx context.Context, opts accessibility.SnapshotOptions) (accessibility.Node, error) {
	b.calls++
	return b.bundleAccessibilityFake.Focused(ctx, opts)
}
func (b *precanceledAccessibilityBackend) AtPoint(ctx context.Context, x, y int) (accessibility.Node, error) {
	b.calls++
	return b.bundleAccessibilityFake.AtPoint(ctx, x, y)
}
func (b *precanceledAccessibilityBackend) ResolveWindow(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	b.calls++
	return b.bundleAccessibilityFake.ResolveWindow(ctx, target)
}
func (b *precanceledAccessibilityBackend) ResolveWindowFresh(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	b.calls++
	return b.bundleAccessibilityFake.ResolveWindowFresh(ctx, target)
}
func (b *precanceledAccessibilityBackend) FindApplication(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.calls++
	return b.bundleAccessibilityFake.FindApplication(ctx, filter)
}
func (b *precanceledAccessibilityBackend) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	b.calls++
	return b.bundleAccessibilityFake.FindApplicationFresh(ctx, filter)
}

// An already-canceled read must not even access a backend that ignores the
// caller's cancellation, regardless of whether its output is later discarded.
func TestAccessibilityReadsNeverDispatchPreCanceledRequests(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.example", ObjectPath: "/app", Generation: 1}
	tests := []struct {
		name string
		read func(*AccessibilityBundle, context.Context) error
	}{
		{"applications", func(b *AccessibilityBundle, c context.Context) error { _, err := b.Applications(c); return err }},
		{"snapshot", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.Snapshot(c, root, accessibility.SnapshotOptions{})
			return err
		}},
		{"outline-fallback", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.Outline(c, root, accessibility.SnapshotOptions{}, accessibility.OutlineOptions{})
			return err
		}},
		{"find", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.Find(c, root, accessibility.Query{}, accessibility.SnapshotOptions{})
			return err
		}},
		{"find-one-selector", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.FindOneSelector(c, root, accessibility.Selector{}, accessibility.SnapshotOptions{})
			return err
		}},
		{"focused", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.Focused(c, accessibility.SnapshotOptions{})
			return err
		}},
		{"at-point", func(b *AccessibilityBundle, c context.Context) error { _, err := b.AtPoint(c, 2, 3); return err }},
		{"window", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.AccessibilityWindow(c, accessibility.WindowTarget{ID: "test"})
			return err
		}},
		{"fresh-window", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.accessibilityWindow(c, accessibility.WindowTarget{ID: "test"}, true)
			return err
		}},
		{"application", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.FindApplication(c, accessibility.ApplicationFilter{Name: "Test"})
			return err
		}},
		{"fresh-application", func(b *AccessibilityBundle, c context.Context) error {
			_, err := b.FindApplicationFresh(c, accessibility.ApplicationFilter{Name: "Test"})
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := &precanceledAccessibilityBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1, apps: []accessibility.Application{{Node: accessibility.Node{ID: root, Name: "Test"}}}}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := tc.read(session.Accessibility, ctx)
			if !errors.Is(err, context.Canceled) || backend.calls != 0 {
				t.Fatalf("pre-canceled %s read: err=%v backend calls=%d", tc.name, err, backend.calls)
			}
		})
	}
}

// These public wrappers must forward real session requests through the owner
// and return authoritative backend results, not just report availability.
func TestAccessibilityBundleSnapshotOutlineFocusedAndPointNormalAndFailure(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/app", Generation: 19}
	target := accessibility.NodeID{BusName: id.BusName, ObjectPath: "/app/save", Generation: 19}
	fake := &snapshotAccessibilityAutomationFake{
		accessibilityAutomationFake: &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 19}, accessibilityAutomationSpy: &accessibilityAutomationSpy{}},
		snapshot:                    accessibility.Snapshot{Root: accessibility.Node{ID: id, Role: "application", Name: "Editor", Children: []accessibility.NodeID{target}}, Nodes: []accessibility.Node{{ID: id, Role: "application", Name: "Editor", Children: []accessibility.NodeID{target}}, {ID: target, Parent: id, Role: "button", Name: "Save"}}, Generation: 19, Source: "synthetic-at-spi"},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	opts := accessibility.SnapshotOptions{MaxNodes: 8, MaxDepth: 3, MaxTextBytes: 256, Fresh: true}
	snap, err := session.Accessibility.Snapshot(context.Background(), id, opts)
	if err != nil || snap.Source != "synthetic-at-spi" || len(snap.Nodes) != 2 || fake.snapshotCalls != 1 {
		t.Fatalf("public snapshot returned %+v err=%v calls=%d", snap, err, fake.snapshotCalls)
	}
	outline, err := session.Accessibility.Outline(context.Background(), id, opts, accessibility.OutlineOptions{})
	if err != nil || fake.snapshotCalls != 2 || outline.Root.ID != id {
		t.Fatalf("public outline %+v err=%v calls=%d", outline, err, fake.snapshotCalls)
	}
	node, err := session.Accessibility.Focused(context.Background(), opts)
	if err != nil || node.Name != "focused" {
		t.Fatalf("public focused=%+v %v", node, err)
	}
	point, err := session.Accessibility.AtPoint(context.Background(), 13, 29)
	if err != nil || point.Name != "point" {
		t.Fatalf("public point=%+v %v", point, err)
	}
	failure := errors.New("synthetic accessibility provider refused observation")
	fake.snapshotErr = failure
	if _, err := session.Accessibility.Snapshot(context.Background(), id, opts); !errors.Is(err, failure) {
		t.Fatalf("snapshot provider error lost: %v", err)
	}
	if _, err := session.Accessibility.Outline(context.Background(), id, opts, accessibility.OutlineOptions{}); !errors.Is(err, failure) {
		t.Fatalf("outline snapshot provider error lost: %v", err)
	}
	if fake.snapshotCalls != 4 {
		t.Fatalf("snapshot calls %d, expected 4", fake.snapshotCalls)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		call func(context.Context) error
	}{
		{"snapshot", func(ctx context.Context) error { _, err := session.Accessibility.Snapshot(ctx, id, opts); return err }},
		{"outline", func(ctx context.Context) error {
			_, err := session.Accessibility.Outline(ctx, id, opts, accessibility.OutlineOptions{})
			return err
		}},
		{"focused", func(ctx context.Context) error { _, err := session.Accessibility.Focused(ctx, opts); return err }},
		{"atpoint", func(ctx context.Context) error { _, err := session.Accessibility.AtPoint(ctx, 13, 29); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation succeeded: %v", err)
			}
		})
	}
}
func TestAccessibilityBundleAutomationRejectsUnsupportedCapabilityAndClosedSession(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.synthetic.App", ObjectPath: "/button", Generation: 1}
	calls := []struct {
		name string
		call func(*AccessibilityBundle) error
	}{
		{"invoke index", func(b *AccessibilityBundle) error { return b.InvokeAction(context.Background(), id, 3) }},
		{"invoke by name", func(b *AccessibilityBundle) error {
			_, err := b.InvokeActionByName(context.Background(), id, "activate")
			return err
		}},
		{"default action", func(b *AccessibilityBundle) error {
			_, err := b.InvokeDefaultAction(context.Background(), id)
			return err
		}},
		{"scroll", func(b *AccessibilityBundle) error {
			return b.ScrollTo(context.Background(), id, accessibility.ScrollType(0))
		}},
		{"scroll point", func(b *AccessibilityBundle) error {
			return b.ScrollToPoint(context.Background(), id, accessibility.CoordType(0), 2, 3)
		}},
		{"position", func(b *AccessibilityBundle) error {
			return b.SetPosition(context.Background(), id, 3, 4, accessibility.CoordType(0))
		}},
		{"size", func(b *AccessibilityBundle) error { return b.SetSize(context.Background(), id, 20, 30) }},
		{"extents", func(b *AccessibilityBundle) error {
			return b.SetExtents(context.Background(), id, 1, 2, 3, 4, accessibility.CoordType(0))
		}},
		{"value", func(b *AccessibilityBundle) error { return b.SetValue(context.Background(), id, 1.5) }},
		{"insert", func(b *AccessibilityBundle) error { return b.InsertText(context.Background(), id, 0, "hello") }},
		{"delete", func(b *AccessibilityBundle) error { return b.DeleteText(context.Background(), id, 1, 4) }},
		{"copy", func(b *AccessibilityBundle) error { return b.CopyText(context.Background(), id, 1, 4) }},
		{"cut", func(b *AccessibilityBundle) error { return b.CutText(context.Background(), id, 1, 4) }},
		{"paste", func(b *AccessibilityBundle) error { return b.PasteText(context.Background(), id, 0) }},
		{"caret", func(b *AccessibilityBundle) error { return b.SetCaretOffset(context.Background(), id, 1) }},
		{"document selections", func(b *AccessibilityBundle) error { return b.SetTextSelections(context.Background(), id, nil) }},
		{"set selection", func(b *AccessibilityBundle) error { return b.SetTextSelection(context.Background(), id, 0, 0, 1) }},
		{"add selection", func(b *AccessibilityBundle) error { return b.AddTextSelection(context.Background(), id, 0, 1) }},
		{"remove selection", func(b *AccessibilityBundle) error { return b.RemoveTextSelection(context.Background(), id, 0) }},
		{"select child", func(b *AccessibilityBundle) error { return b.SelectChild(context.Background(), id, 1) }},
		{"deselect child", func(b *AccessibilityBundle) error { return b.DeselectChild(context.Background(), id, 1) }},
		{"select all", func(b *AccessibilityBundle) error { return b.SelectAll(context.Background(), id) }},
		{"clear selection", func(b *AccessibilityBundle) error { return b.ClearSelection(context.Background(), id) }},
		{"deselect selected", func(b *AccessibilityBundle) error { return b.DeselectSelectedChild(context.Background(), id) }},
		{"select row", func(b *AccessibilityBundle) error { return b.SelectRow(context.Background(), id, 1) }},
		{"deselect row", func(b *AccessibilityBundle) error { return b.DeselectRow(context.Background(), id, 1) }},
		{"select column", func(b *AccessibilityBundle) error { return b.SelectColumn(context.Background(), id, 1) }},
		{"deselect column", func(b *AccessibilityBundle) error { return b.DeselectColumn(context.Background(), id, 1) }},
		{"focus", func(b *AccessibilityBundle) error { return b.FocusNode(context.Background(), id) }},
		{"replace text", func(b *AccessibilityBundle) error { return b.ReplaceEditableText(context.Background(), id, "text") }},
	}
	// A read-only AT-SPI backend cannot silently fall back to physical input.
	readonly := NewSessionForTesting(nil, nil, nil, nil, nil, &bundleAccessibilityFake{gen: 1})
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(readonly.Accessibility)
			if err == nil || (!errors.Is(err, accessibility.ErrUnsupported) && !strings.Contains(err.Error(), "unsupported")) {
				t.Fatalf("read-only backend executed %s: %v", tc.name, err)
			}
		})
	}
	if err := readonly.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range calls {
		t.Run("closed-"+tc.name, func(t *testing.T) {
			if err := tc.call(readonly.Accessibility); err == nil {
				t.Fatalf("closed backend accepted %s", tc.name)
			}
		})
	}
}

type revokingAccessibilityReads struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *revokingAccessibilityReads) Applications(context.Context) ([]accessibility.Application, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return []accessibility.Application{{Node: accessibility.Node{Name: "private-app"}}}, nil
}
func (b *revokingAccessibilityReads) Snapshot(_ context.Context, _ accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Snapshot{Nodes: []accessibility.Node{{Name: "private-node"}}}, nil
}

func TestAccessibilityBundleReadsDiscardResultsAfterAuthorizationLoss(t *testing.T) {
	for _, operation := range []string{"applications", "snapshot"} {
		for _, mode := range []string{"pre-cancel", "late-cancel", "close"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				backend := &revokingAccessibilityReads{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
				session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
				t.Cleanup(func() { _ = session.Close() })
				wantErr := context.Canceled
				if mode == "pre-cancel" {
					cancel()
				} else if mode == "late-cancel" {
					backend.after = cancel
				} else {
					wantErr = ErrSessionClosed
					backend.after = func() {
						session.lifecycleMu.Lock()
						session.closed = true
						session.lifecycleMu.Unlock()
					}
				}
				if operation == "applications" {
					got, err := session.Accessibility.Applications(ctx)
					if !errors.Is(err, wantErr) || len(got) != 0 {
						t.Errorf("apps result leaked: %v %v", got, err)
					}
				} else {
					got, err := session.Accessibility.Snapshot(ctx, accessibility.NodeID{}, accessibility.SnapshotOptions{})
					if !errors.Is(err, wantErr) || len(got.Nodes) != 0 {
						t.Errorf("snapshot result leaked: %+v %v", got, err)
					}
				}
				calls := 1
				if mode == "pre-cancel" {
					calls = 0
				}
				if backend.calls != calls {
					t.Errorf("backend calls = %d, want %d", backend.calls, calls)
				}
			})
		}
	}
}

type accessibilityBundleContractProbe struct {
	*accessibilityAutomationFake
	calls       int
	snapshotErr error
}

func newAccessibilityBundleContractProbe() *accessibilityBundleContractProbe {
	return &accessibilityBundleContractProbe{
		accessibilityAutomationFake: &accessibilityAutomationFake{
			bundleAccessibilityFake:    &bundleAccessibilityFake{gen: 1},
			accessibilityAutomationSpy: &accessibilityAutomationSpy{},
		},
	}
}

func (b *accessibilityBundleContractProbe) record() {
	b.calls++
}

func (b *accessibilityBundleContractProbe) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.record()
	if b.snapshotErr != nil {
		return accessibility.Snapshot{}, b.snapshotErr
	}
	return b.bundleAccessibilityFake.Snapshot(ctx, root, opts)
}

type directOutlineCancellationProbe struct {
	*accessibilityBundleContractProbe
	cancel context.CancelFunc
}

func (b *directOutlineCancellationProbe) Outline(context.Context, accessibility.NodeID, accessibility.SnapshotOptions, accessibility.OutlineOptions) (accessibility.Outline, error) {
	b.record()
	b.cancel()
	return accessibility.Outline{Root: accessibility.OutlineNode{Name: "private outline"}, Warnings: []string{"private warning"}}, nil
}

func TestAccessibilityDirectOutlineDiscardsResultAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &directOutlineCancellationProbe{accessibilityBundleContractProbe: newAccessibilityBundleContractProbe(), cancel: cancel}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, probe)
	defer session.Close()
	owner := session.Accessibility.backend
	session.Accessibility.backend = probe
	defer func() { session.Accessibility.backend = owner }()

	outline, err := session.Accessibility.Outline(ctx, accessibility.NodeID{}, accessibility.SnapshotOptions{}, accessibility.OutlineOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Outline error = %v, want context cancellation", err)
	}
	if outline.Root.Name != "" || len(outline.Warnings) != 0 {
		t.Fatalf("late-canceled outline exposed provider result: %+v", outline)
	}
	if probe.calls != 1 {
		t.Fatalf("direct outline provider calls = %d, want one", probe.calls)
	}
}

func TestAccessibilityFindOperationsPreserveSnapshotProviderFailure(t *testing.T) {
	providerErr := errors.New("snapshot transport closed")
	root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/app", Generation: 1}
	for _, tc := range []struct {
		name string
		call func(*AccessibilityBundle) error
	}{
		{"find", func(b *AccessibilityBundle) error {
			_, err := b.Find(context.Background(), root, accessibility.Query{Name: "Save"}, accessibility.SnapshotOptions{})
			return err
		}},
		{"find one", func(b *AccessibilityBundle) error {
			_, err := b.FindOneSelector(context.Background(), root, accessibility.Selector{Name: "Save"}, accessibility.SnapshotOptions{})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := newAccessibilityBundleContractProbe()
			backend.snapshotErr = providerErr
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			defer session.Close()

			if err := tc.call(session.Accessibility); !errors.Is(err, providerErr) {
				t.Fatalf("find error = %v, want snapshot provider failure", err)
			}
			if backend.calls != 1 {
				t.Fatalf("snapshot provider calls = %d, want one", backend.calls)
			}
		})
	}
}

func TestAccessibilityWindowRootWrapsMissingManagedWindowCapability(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	_, err := session.Accessibility.WindowRoot(context.Background(), "window-1")
	var operationErr *OperationError
	if !errors.Is(err, ErrUnavailable) || !errors.As(err, &operationErr) || operationErr.Operation != "window-root" {
		t.Fatalf("WindowRoot without window backend error = %v, want unavailable window-root capability", err)
	}
}

func TestAccessibilitySelectorSemanticActionReturnsExactDispatchReceipt(t *testing.T) {
	spy := &accessibilityAutomationSpy{exactAction: accessibility.Action{Index: 7, Name: "press"}}
	session, root := semanticActionCoverageFixture(spy)
	defer session.Close()

	receipt, err := session.Accessibility.InvokeSelectorSemanticAction(
		context.Background(), root, accessibility.Selector{Name: "Save", Role: "button"}, "press", accessibility.SnapshotOptions{},
	)
	if err != nil {
		t.Fatalf("InvokeSelectorSemanticAction: %v", err)
	}
	if receipt.Node.Name != "Save" || receipt.Action != spy.exactAction || receipt.Dispatch != accessibility.DispatchAccepted || receipt.Generation != root.Generation {
		t.Fatalf("selector action receipt lost the selected node/action/dispatch: %+v", receipt)
	}
	if spy.invokeCount != 1 {
		t.Fatalf("provider action calls = %d, want one", spy.invokeCount)
	}
}

type lateCanceledDefaultActionProbe struct {
	*accessibilityBundleContractProbe
	cancel  context.CancelFunc
	failure error
}

func (b *lateCanceledDefaultActionProbe) InvokeDefaultAction(context.Context, accessibility.NodeID) (accessibility.Action, error) {
	b.record()
	b.cancel()
	return accessibility.Action{Name: "private action"}, b.failure
}

func TestAccessibilityDefaultActionDiscardsLateResultAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	providerErr := errors.New("provider completed after cancellation")
	probe := &lateCanceledDefaultActionProbe{accessibilityBundleContractProbe: newAccessibilityBundleContractProbe(), cancel: cancel, failure: providerErr}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, probe)
	defer session.Close()
	owner := session.Accessibility.backend
	session.Accessibility.backend = probe
	defer func() { session.Accessibility.backend = owner }()

	action, err := session.Accessibility.InvokeDefaultAction(ctx, accessibility.NodeID{})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, providerErr) {
		t.Fatalf("InvokeDefaultAction error = %v, want caller cancellation and provider cause", err)
	}
	if action != (accessibility.Action{}) {
		t.Fatalf("late-canceled default action exposed provider result: %+v", action)
	}
	if probe.calls != 1 {
		t.Fatalf("default action provider calls = %d, want one", probe.calls)
	}
}

type closingSemanticErrorActionBackend struct {
	*accessibilityAutomationFake
	after func()
	calls int
}

func (b *closingSemanticErrorActionBackend) InvokeActionByName(context.Context, accessibility.NodeID, string) (accessibility.Action, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Action{}, &accessibility.ActionInvocationError{
		Action:   accessibility.Action{Name: "private-action", Index: 7},
		Dispatch: accessibility.DispatchNotSent, Err: accessibility.ErrStaleGeneration,
	}
}
func (b *closingSemanticErrorActionBackend) InvokeDefaultAction(context.Context, accessibility.NodeID) (accessibility.Action, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Action{}, &accessibility.ActionInvocationError{
		Action:   accessibility.Action{Name: "private-default", Index: 0},
		Dispatch: accessibility.DispatchNotSent, Err: accessibility.ErrStaleGeneration,
	}
}

// A provider's dispatch error may embed sensitive node/action metadata. Once
// the session closes, even this failure receipt is no longer authorized.
func TestAccessibilitySemanticErrorReceiptSuppressesPrivateMetadataAfterSessionClosure(t *testing.T) {
	for _, name := range []string{"named", "default"} {
		t.Run(name, func(t *testing.T) {
			backend := &closingSemanticErrorActionBackend{accessibilityAutomationFake: &accessibilityAutomationFake{
				bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, accessibilityAutomationSpy: &accessibilityAutomationSpy{},
			}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			node := accessibility.Node{ID: accessibility.NodeID{BusName: "org.private", ObjectPath: "/secret", Generation: 1}, Name: "private-button"}
			actionName := "press"
			if name == "default" {
				actionName = ""
			}
			receipt, err := session.Accessibility.invokeSemanticActionOnNode(context.Background(), node, actionName)
			if !errors.Is(err, ErrSessionClosed) || backend.calls != 1 || receipt.Node.Name != "" || receipt.Action.Name != "" {
				t.Fatalf("%s leaked semantic receipt after closure: receipt=%+v err=%v calls=%d", name, receipt, err, backend.calls)
			}
		})
	}
}

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
	snapshot               accessibility.Snapshot
	snapshotOptions        accessibility.SnapshotOptions
	snapshotErr            error
	snapshotCalls          int
	eventCallsAtSnapshot   int
	eventOptionsAtSnapshot accessibility.EventOptions
}

func (f *snapshotAccessibilityAutomationFake) Snapshot(_ context.Context, _ accessibility.NodeID, options accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	f.snapshotOptions = options
	f.snapshotCalls++
	f.eventMu.Lock()
	f.eventCallsAtSnapshot = f.eventCalls
	f.eventOptionsAtSnapshot = f.eventOptions
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

func TestAccessibilityActionWaitPreflightsConditionCapabilitiesAndOptions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		condition Condition
		options   []WaitOption
		wantCap   Capability
		wantErr   error
	}{
		{
			name: "missing window capability",
			condition: WithConditionCapabilities(Predicate("window state", func(context.Context) (bool, error) {
				return true, nil
			}), CapabilityWindows),
			wantCap: CapabilityWindows,
		},
		{
			name: "missing screen capability",
			condition: WithConditionCapabilities(Predicate("screen state", func(context.Context) (bool, error) {
				return true, nil
			}), CapabilityScreen),
			wantCap: CapabilityScreen,
		},
		{
			name:      "invalid wait interval",
			condition: Predicate("ready", func(context.Context) (bool, error) { return true, nil }),
			options:   []WaitOption{WaitEvery(0)},
		},
		{
			name:      "nil window postcondition target",
			condition: WindowClosed(nil),
			wantErr:   ErrNilSession,
		},
		{
			name:      "nil predicate",
			condition: Predicate("invalid", nil),
			wantErr:   ErrInvalidArgument,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &accessibilityAutomationSpy{}
			fake := &accessibilityAutomationFake{
				bundleAccessibilityFake:    &bundleAccessibilityFake{gen: 3},
				accessibilityAutomationSpy: spy,
			}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
			defer session.Close()
			_, _, err := session.InvokeAccessibilityActionAndWait(
				context.Background(),
				accessibility.NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 3},
				accessibility.Query{Name: "Save"}, "press", accessibility.SnapshotOptions{}, tc.condition, tc.options...,
			)
			if err == nil {
				t.Fatal("expected preflight error")
			}
			if tc.wantCap != "" {
				var capabilityErr *CapabilityError
				if !errors.As(err, &capabilityErr) || capabilityErr.Capability != tc.wantCap {
					t.Fatalf("error = %v, want unavailable %s capability", err, tc.wantCap)
				}
			} else if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
			} else if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("error = %v, want ErrInvalidArgument", err)
			}
			if len(spy.calls) != 0 {
				t.Fatalf("preflight failure dispatched action: %v", spy.calls)
			}
		})
	}
}

func TestAccessibilityActionWaitTimeoutPreservesAcceptedReceiptWithoutReplay(t *testing.T) {
	spy := &accessibilityAutomationSpy{}
	fake := &accessibilityAutomationFake{
		bundleAccessibilityFake:    &bundleAccessibilityFake{gen: 3},
		accessibilityAutomationSpy: spy,
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	receipt, evidence, err := session.InvokeAccessibilityActionAndWait(
		ctx,
		accessibility.NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 3},
		accessibility.Query{Name: "Save"}, "press", accessibility.SnapshotOptions{},
		Predicate("never satisfied", func(context.Context) (bool, error) { return false, nil }),
		WaitEvery(time.Millisecond),
	)
	var verificationErr *ActionVerificationError
	if !errors.As(err, &verificationErr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want ActionVerificationError wrapping deadline", err)
	}
	if receipt.Dispatch != accessibility.DispatchAccepted || receipt.Outcome.Status != ActionOutcomeNotVerified {
		t.Fatalf("receipt = %+v, want accepted and not verified", receipt)
	}
	if len(spy.calls) != 1 || spy.calls[0] != "action-name" || evidence.Evaluations == 0 {
		t.Fatalf("calls=%v evidence=%+v, want one dispatch and fresh evaluations", spy.calls, evidence)
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
	eventCalls, _, _ := fake.eventObservationState()
	if eventCalls < 1 || fake.eventCallsAtSnapshot != 1 {
		t.Fatalf("event subscriptions=%d at snapshot=%d, want locator observation active before snapshot", eventCalls, fake.eventCallsAtSnapshot)
	}
	// Session.Wait starts its own general event subscription asynchronously.
	// Prove the locator's scoped subscription at the action's snapshot
	// boundary instead of checking the last (potentially unrelated) one.
	requireSemanticEventCoverage(t, fake.eventOptionsAtSnapshot)
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

// Selecting an action is itself a mutation. A provider that ignores the
// cancellation context must never receive a pre-canceled action request.
func TestAccessibilityActionReceiptsRejectPreCanceledDispatch(t *testing.T) {
	for _, name := range []string{"by-name", "default"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			backend := &closingActionReceiptBackend{accessibilityAutomationFake: &accessibilityAutomationFake{
				bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, accessibilityAutomationSpy: &accessibilityAutomationSpy{},
			}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			previous := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = previous })
			id := accessibility.NodeID{BusName: "org.synthetic", ObjectPath: "/button", Generation: 1}
			var receipt accessibility.Action
			var err error
			if name == "by-name" {
				receipt, err = session.Accessibility.InvokeActionByName(ctx, id, "press")
			} else {
				receipt, err = session.Accessibility.InvokeDefaultAction(ctx, id)
			}
			if !errors.Is(err, context.Canceled) || backend.calls != 0 || receipt.Name != "" {
				t.Fatalf("pre-canceled %s mutation dispatched: receipt=%+v err=%v calls=%d", name, receipt, err, backend.calls)
			}
		})
	}
}

// The compositor's native ID identifies the desired window, but does not
// itself authorize an application identity. Correlation must return a fully
// scoped, generation-bearing AT-SPI application and enforce name filters
// against that *resolved* application rather than unrelated running apps.
func TestAccessibilityFindApplicationWindowCorrelationRequiresFreshIdentityAndMatchingName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		app    accessibility.Application
		gen    uint64
		filter accessibility.ApplicationFilter
		want   error
	}{
		{name: "missing application identity generation", app: accessibility.Application{Node: accessibility.Node{Name: "Editor"}}, gen: 0, filter: accessibility.ApplicationFilter{WindowID: "desktop-window"}, want: accessibility.ErrUnsupportedCorrelation},
		{name: "wrong app name", app: accessibility.Application{Node: accessibility.Node{Name: "Unrelated Browser"}}, gen: 7, filter: accessibility.ApplicationFilter{WindowID: "desktop-window", Name: "Editor"}, want: accessibility.ErrNotFound},
		{name: "matching name case-insensitive", app: accessibility.Application{Node: accessibility.Node{Name: "Text Editor"}}, gen: 7, filter: accessibility.ApplicationFilter{WindowID: "desktop-window", Name: "EDITOR"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &accessibilityWindowManager{windows: []window.Info{{NativeID: "desktop-window", Title: "Editor Window", PID: 321}}}
			backend := &bundleAccessibilityFake{apps: []accessibility.Application{tc.app}, gen: tc.gen}
			session := NewSessionForTesting(nil, nil, manager, nil, nil, backend)
			defer session.Close()
			app, err := session.Accessibility.FindApplication(context.Background(), tc.filter)
			if tc.want != nil {
				if !errors.Is(err, tc.want) || app.ID.BusName != "" || app.Name != "" {
					t.Fatalf("unsafe correlation: app=%+v err=%v want %v", app, err, tc.want)
				}
			} else if err != nil || app.Name != "Text Editor" || app.ID.Generation != 7 {
				t.Fatalf("valid window correlation refused: app=%+v err=%v", app, err)
			}
			if len(backend.resolvedTargets) != 1 || backend.resolvedTargets[0].ID != "desktop-window" {
				t.Fatalf("incorrect compositor identity used for correlation: %+v", backend.resolvedTargets)
			}
		})
	}
}

func TestAccessibilityFindApplicationWindowResolverFailureCannotSelectAnotherApplication(t *testing.T) {
	manager := &accessibilityWindowManager{windows: []window.Info{{NativeID: "desktop-window", Title: "Editor Window", PID: 321}}}
	backend := &bundleAccessibilityFake{gen: 5}
	session := NewSessionForTesting(nil, nil, manager, nil, nil, backend)
	defer session.Close()
	app, err := session.Accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{WindowID: "desktop-window"})
	if !errors.Is(err, accessibility.ErrNotFound) || app.ID.BusName != "" || len(backend.resolvedTargets) != 1 {
		t.Fatalf("missing window application selected: app=%+v err=%v targets=%+v", app, err, backend.resolvedTargets)
	}
}

// After closing an accessibility session, no traversal, resolver, or
// read operation may consult the backend or disclose a stale observation.
func TestAccessibilityClosedSessionRejectsReadsWithoutBackendDispatch(t *testing.T) {
	backend := &bundleAccessibilityFake{gen: 4, apps: []accessibility.Application{{Node: accessibility.Node{Name: "private window"}}}}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id := accessibility.NodeID{BusName: "org.example", ObjectPath: "/private", Generation: 4}
	opts := accessibility.SnapshotOptions{}
	actions := []struct {
		name string
		run  func() error
	}{
		{"applications", func() error {
			got, err := session.Accessibility.Applications(ctx)
			if len(got) > 0 {
				t.Errorf("leaked applications: %v", got)
			}
			return err
		}},
		{"snapshot", func() error {
			got, err := session.Accessibility.Snapshot(ctx, id, opts)
			if len(got.Nodes) > 0 {
				t.Errorf("leaked snapshot: %v", got)
			}
			return err
		}},
		{"outline", func() error {
			_, err := session.Accessibility.Outline(ctx, id, opts, accessibility.OutlineOptions{})
			return err
		}},
		{"window scope", func() error {
			got, err := session.Accessibility.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: "private"})
			if got.WindowID != "" {
				t.Errorf("leaked window scope: %v", got)
			}
			return err
		}},
		{"find", func() error {
			got, err := session.Accessibility.Find(ctx, id, accessibility.Query{}, opts)
			if len(got) > 0 {
				t.Errorf("leaked matches: %v", got)
			}
			return err
		}},
		{"find one", func() error {
			_, err := session.Accessibility.FindOneSelector(ctx, id, accessibility.Selector{}, opts)
			return err
		}},
		{"focused", func() error {
			got, err := session.Accessibility.Focused(ctx, opts)
			if got.Name != "" {
				t.Errorf("leaked focus: %v", got)
			}
			return err
		}},
		{"at point", func() error {
			got, err := session.Accessibility.AtPoint(ctx, 10, 20)
			if got.Name != "" {
				t.Errorf("leaked hit: %v", got)
			}
			return err
		}},
		{"find application", func() error {
			got, err := session.Accessibility.FindApplication(ctx, accessibility.ApplicationFilter{Name: "private"})
			if got.Name != "" {
				t.Errorf("leaked application: %v", got)
			}
			return err
		}},
	}
	for _, tc := range actions {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrSessionClosed) {
				t.Fatalf("closed session did not reject %s: %v", tc.name, err)
			}
		})
	}
	if backend.findCalls != 0 || backend.freshApplicationFind != 0 || backend.freshWindowResolve != 0 || len(backend.resolvedTargets) > 0 {
		t.Fatalf("closed session dispatched to provider: find=%d fresh=%d windows=%v", backend.findCalls, backend.freshApplicationFind, backend.resolvedTargets)
	}
}

// Capability advertisements cannot authorize a stream from a backend that
// does not implement EventSource. A rejected event request must not alter the
// underlying provider or accidentally publish a channel with no event owner.
func TestAccessibilityBundleEventsRejectsUnsupportedAndUnavailableSources(t *testing.T) {
	var nilBundle *AccessibilityBundle
	if stream, err := nilBundle.Events(context.Background(), accessibility.EventOptions{}); stream != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil bundle admitted events: stream=%v err=%v", stream, err)
	}
	unavailable := NewSessionForTesting(nil, nil, nil, nil, nil)
	if stream, err := unavailable.Accessibility.Events(context.Background(), accessibility.EventOptions{}); stream != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing backend admitted events: stream=%v err=%v", stream, err)
	}
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}
	if stream, err := unavailable.Accessibility.Events(context.Background(), accessibility.EventOptions{}); stream != nil || !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session admitted events: stream=%v err=%v", stream, err)
	}

	provider := newOwnerLifecycleBackend(31, "events", "applications")
	session := NewSessionForTesting(nil, nil, nil, nil, nil, provider)
	t.Cleanup(func() { _ = session.Close() })
	original := session.Accessibility.backend
	session.Accessibility.backend = &coreOnlyAccessibilityBackend{Backend: provider}
	defer func() { session.Accessibility.backend = original }()
	stream, err := session.Accessibility.Events(context.Background(), accessibility.EventOptions{Buffer: 2})
	if stream != nil || !errors.Is(err, accessibility.ErrUnsupported) {
		t.Fatalf("provider without EventSource exposed stream=%v err=%v", stream, err)
	}
	if provider.eventCalls.Load() != 0 || provider.closeCalls.Load() != 0 {
		t.Fatalf("unsupported event source dispatched or closed provider: calls=%d closed=%d", provider.eventCalls.Load(), provider.closeCalls.Load())
	}
	session.Accessibility.backend = original
	if _, err := session.Accessibility.Applications(context.Background()); err != nil {
		t.Fatalf("rejected event subscription damaged live provider: %v", err)
	}
}

// Generation and invalidation are optional AT-SPI capabilities. A missing,
// typed-nil, or core-only provider must not pretend to expose a live handle
// epoch, and invalidation must never reach a hidden optional implementation.
func TestAccessibilityBundleGenerationAndInvalidationOptionalProviderBoundaries(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.example.Document", ObjectPath: "/document", Generation: 7}
	var absent *AccessibilityBundle
	if absent.Generation() != 0 {
		t.Fatal("nil bundle claimed live generation")
	}
	absent.Invalidate(id)

	var nilFake *bundleAccessibilityFake
	for _, tc := range []struct {
		name    string
		backend accessibility.Backend
	}{
		{name: "unconfigured"},
		{name: "typed nil", backend: nilFake},
		{name: "core only", backend: &coreOnlyAccessibilityBackend{Backend: &bundleAccessibilityFake{gen: 7}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := &AccessibilityBundle{backend: tc.backend}
			if got := bundle.Generation(); got != 0 {
				t.Fatalf("unsupported generation=%d, want zero", got)
			}
			bundle.Invalidate(id)
			if got := bundle.Generation(); got != 0 {
				t.Fatalf("unsupported invalidation advanced generation=%d", got)
			}
		})
	}
	provider := &bundleAccessibilityFake{gen: 7}
	bundle := &AccessibilityBundle{backend: provider}
	if got := bundle.Generation(); got != 7 {
		t.Fatalf("live provider generation=%d", got)
	}
	bundle.Invalidate(id)
	if got := bundle.Generation(); got != 8 {
		t.Fatalf("invalidation did not advance live generation=%d", got)
	}
}

// A closed session must not expose a live generation identity or let callers
// invalidate a provider that no longer belongs to their session.
func TestAccessibilityGenerationAndInvalidationRejectClosedSession(t *testing.T) {
	backend := &bundleAccessibilityFake{gen: 41}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	original := session.Accessibility.backend
	session.Accessibility.backend = backend
	defer func() { session.Accessibility.backend = original }()
	if got := session.Accessibility.Generation(); got != 41 {
		t.Fatalf("initial generation=%d", got)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	id := accessibility.NodeID{BusName: "org.example", ObjectPath: "/node", Generation: 41}
	if got := session.Accessibility.Generation(); got != 0 {
		t.Errorf("closed session advertised generation %d", got)
	}
	session.Accessibility.Invalidate(id)
	if backend.gen != 41 {
		t.Errorf("closed session invalidated retired provider to generation %d", backend.gen)
	}
}

// A missing accessibility service must never expose a value, even when callers
// invoke discovery operations directly on a nil bundle. The absence of any
// session also must not trigger a provider operation or panic.
func TestNilAccessibilityBundleReadsFailClosed(t *testing.T) {
	var bundle *AccessibilityBundle
	ctx := context.Background()
	root := accessibility.NodeID{BusName: "org.example.Application", ObjectPath: "/app", Generation: 7}
	selector := accessibility.Selector{Name: "Save", Role: "button"}
	query := accessibility.Query{Name: "Save", Role: "button"}
	for _, tc := range []struct {
		name string
		call func() (bool, error)
	}{
		{"applications", func() (bool, error) { v, e := bundle.Applications(ctx); return len(v) > 0, e }},
		{"snapshot", func() (bool, error) {
			v, e := bundle.Snapshot(ctx, root, accessibility.SnapshotOptions{})
			return len(v.Nodes) > 0 || v.Root.ID.BusName != "", e
		}},
		{"outline", func() (bool, error) {
			v, e := bundle.Outline(ctx, root, accessibility.SnapshotOptions{}, accessibility.OutlineOptions{})
			return v.Root.ID.BusName != "", e
		}},
		{"window", func() (bool, error) {
			v, e := bundle.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: "win"})
			return v.Root.BusName != "", e
		}},
		{"fresh-window", func() (bool, error) {
			v, e := bundle.accessibilityWindow(ctx, accessibility.WindowTarget{ID: "win"}, true)
			return v.Root.BusName != "", e
		}},
		{"find", func() (bool, error) {
			v, e := bundle.Find(ctx, root, query, accessibility.SnapshotOptions{})
			return len(v) > 0, e
		}},
		{"find-one", func() (bool, error) {
			v, e := bundle.FindOne(ctx, root, query, accessibility.SnapshotOptions{})
			return v.ID.BusName != "", e
		}},
		{"find-one-selector", func() (bool, error) {
			v, e := bundle.FindOneSelector(ctx, root, selector, accessibility.SnapshotOptions{})
			return v.ID.BusName != "", e
		}},
		{"focused", func() (bool, error) {
			v, e := bundle.Focused(ctx, accessibility.SnapshotOptions{})
			return v.ID.BusName != "", e
		}},
		{"at-point", func() (bool, error) { v, e := bundle.AtPoint(ctx, 1, 2); return v.ID.BusName != "", e }},
		{"application", func() (bool, error) {
			v, e := bundle.FindApplication(ctx, accessibility.ApplicationFilter{Name: "Editor"})
			return v.ID.BusName != "", e
		}},
		{"fresh-application", func() (bool, error) {
			v, e := bundle.FindApplicationFresh(ctx, accessibility.ApplicationFilter{Name: "Editor"})
			return v.ID.BusName != "", e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exposed, err := tc.call()
			if exposed || err == nil || err.Error() != "session is nil" {
				t.Fatalf("nil backend read: exposed=%t err=%v", exposed, err)
			}
		})
	}
}

// A reconnect request must not manufacture a backend from an absent, closed,
// or non-reconnectable accessibility provider. In particular, advertising the
// capability does not authorize casting an arbitrary backend to its owner.
func TestAccessibilityBundleReopenRejectsAbsentClosedOrUnownedBackend(t *testing.T) {
	var nilBundle *AccessibilityBundle
	if err := nilBundle.Reopen(context.Background()); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil bundle reopened: %v", err)
	}
	unavailable := NewSessionForTesting(nil, nil, nil, nil, nil)
	if err := unavailable.Accessibility.Reopen(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("absent provider reopened: %v", err)
	}
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}
	if err := unavailable.Accessibility.Reopen(context.Background()); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session reopened: %v", err)
	}

	owned := newOwnerLifecycleBackend(8, "applications", "reopen")
	session := NewSessionForTesting(nil, nil, nil, nil, nil, owned)
	t.Cleanup(func() { _ = session.Close() })
	owner := session.Accessibility.backend
	direct := &coreOnlyAccessibilityBackend{Backend: owned}
	session.Accessibility.backend = direct
	defer func() { session.Accessibility.backend = owner }()
	if err := session.Accessibility.Reopen(context.Background()); !errors.Is(err, accessibility.ErrUnsupported) {
		t.Fatalf("unowned backend reconnect: %v", err)
	}
	if owned.closeCalls.Load() != 0 {
		t.Fatal("unsupported reconnect unexpectedly closed live owner backend")
	}
	if !session.Capability(CapabilityAccessibility).Available {
		t.Fatal("unsupported reconnect disabled still-owned capability")
	}
	session.Accessibility.backend = owner
	if _, err := session.Accessibility.Applications(context.Background()); err != nil {
		t.Fatalf("unsupported request corrupted original backend: %v", err)
	}
}
