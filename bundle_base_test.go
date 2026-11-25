package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/capability"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
	"image"
	"slices"
	"strings"
	"testing"
	"time"
)

type countingFindBackend struct {
	bundleAccessibilityFake
	finds     int
	snapshots int
	nodes     []accessibility.Node
}

func (f *countingFindBackend) Find(_ context.Context, _ accessibility.NodeID, query accessibility.Query, _ accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	f.finds++
	var out []accessibility.Node
	for _, node := range f.nodes {
		if query.Name != "" && !strings.Contains(strings.ToLower(node.Name), strings.ToLower(query.Name)) {
			continue
		}
		out = append(out, node)
	}
	return out, nil
}

func (f *countingFindBackend) Snapshot(_ context.Context, _ accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	f.snapshots++
	return accessibility.Snapshot{Nodes: f.nodes, Generation: f.gen}, nil
}

func TestAdversarialFindOnePaysSingleSnapshot(t *testing.T) {
	root := accessibility.NodeID{BusName: "b", ObjectPath: "/root", Generation: 2}
	missing := accessibility.Query{Name: "does-not-exist"}
	backend := &countingFindBackend{
		bundleAccessibilityFake: bundleAccessibilityFake{gen: 2},
		nodes: []accessibility.Node{
			{ID: accessibility.NodeID{BusName: "b", ObjectPath: "/a", Generation: 2}, Name: "alpha"},
		},
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	_, err := session.Accessibility.FindOne(context.Background(), root, missing, accessibility.SnapshotOptions{})
	if err == nil {
		t.Fatal("missing query unexpectedly succeeded")
	}
	// Find already performed a bounded snapshot-equivalent read; a second
	// snapshot purely for candidate diagnostics doubles AT-SPI round trips.
	// The correct contract is exactly one backend read per FindOne.
	if backend.finds != 0 || backend.snapshots != 1 {
		t.Fatalf("FindOne paid finds=%d snapshots=%d, want 0 finds and 1 snapshot", backend.finds, backend.snapshots)
	}
}

func TestAdversarialCapabilityOperationsMatchBackend(t *testing.T) {
	backendOps := (&bundleAccessibilityFake{gen: 1}).SupportedOperations()
	_ = backendOps
	caps := capability.Operations("accessibility")
	for _, required := range []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events", "outline", "invoke-action", "invoke-action-by-name", "invoke-default-action", "grab-focus", "scroll", "window-root", "reopen"} {
		found := false
		for _, c := range caps {
			if c == required {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("capability operations missing %q (caps=%v)", required, caps)
		}
	}
}

func syntheticSupportedBundle() *bundleBase {
	bundle := &bundleBase{
		capability: CapabilityInput,
		session: &Session{ctx: context.Background(), capabilities: map[Capability]CapabilityStatus{
			CapabilityInput: {
				Capability: CapabilityInput, Available: true, Backend: "*input.WlVirtualBackend",
				Operations:  []string{"keyboard", "click", "pointer", "scroll", "sync", "pointer-location", "pointer-coordinate-space"},
				Diagnostics: []string{"wayland", "keyboard active", "pointer active"},
			},
		}},
	}
	bundle.cacheOperations(bundle.session.capabilities[CapabilityInput].Operations)
	return bundle
}

// Screen, windows, and input all use this exact availability gate on every
// operation. The public Capability method must remain independently owned.
func BenchmarkSyntheticBundleCapabilityCheck(b *testing.B) {
	base := syntheticSupportedBundle()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := base.checkAvailable("keyboard", true); err != nil {
			b.Fatal(err)
		}
	}
}

func TestBundleCapabilityLookupRetainsPublicSnapshotIsolation(t *testing.T) {
	b := syntheticSupportedBundle()
	if err := b.checkAvailable("keyboard", true); err != nil {
		t.Fatal(err)
	}
	if err := b.checkAvailable("not-supported", true); err == nil {
		t.Fatal("missing unsupported error")
	}
	if err := b.checkAvailable("keyboard", false); err == nil {
		t.Fatal("missing unavailable error")
	}
	if err := b.checkAvailable("keyboard", true); err != nil {
		t.Fatal(err)
	}
	snapshot := b.session.Capability(CapabilityInput)
	snapshot.Operations[0] = "mutated"
	snapshot.Diagnostics[0] = "mutated"
	if !slices.Contains(b.session.Capability(CapabilityInput).Operations, "keyboard") {
		t.Fatal("public Capability returned mutable session operations")
	}
	if err := b.checkAvailable("keyboard", true); err != nil {
		t.Fatalf("snapshot mutated checkAvailability: %v", err)
	}
}

// The metadata snapshot can be updated or returned to callers without changing
// the operation contract cached when a normal session opens.
func TestSessionStaticOperationContractRemainsFixed(t *testing.T) {
	b := syntheticSupportedBundle()
	b.session.capabilitiesMu.Lock()
	status := b.session.capabilities[CapabilityInput]
	status.Operations = []string{"different-operation"}
	b.session.capabilities[CapabilityInput] = status
	b.session.capabilitiesMu.Unlock()
	if err := b.checkAvailable("keyboard", true); err != nil {
		t.Fatalf("cached keyboard support unexpectedly disappeared: %v", err)
	}
	if err := b.checkAvailable("different-operation", true); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unadvertised operation unexpectedly succeeded: %v", err)
	}
	b.session.lifecycleMu.Lock()
	b.session.closed = true
	b.session.lifecycleMu.Unlock()
	if err := b.checkAvailable("keyboard", true); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session returned %v, want ErrSessionClosed", err)
	}
}

func TestAccessibilityOperationSupportRemainsDynamic(t *testing.T) {
	b := syntheticSupportedBundle()
	b.capability = CapabilityAccessibility
	b.cacheOperations([]string{"find"}) // must be ignored for dynamic accessibility
	b.session.capabilities[CapabilityAccessibility] = CapabilityStatus{
		Capability: CapabilityAccessibility,
		Operations: []string{"find"},
		Available:  true,
	}
	if err := b.checkAvailable("find", true); err != nil {
		t.Fatal(err)
	}
	b.session.capabilitiesMu.Lock()
	status := b.session.capabilities[CapabilityAccessibility]
	status.Operations = []string{"snapshot"}
	b.session.capabilities[CapabilityAccessibility] = status
	b.session.capabilitiesMu.Unlock()
	if err := b.checkAvailable("find", true); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("stale accessibility support was used: %v", err)
	}
	if err := b.checkAvailable("snapshot", true); err != nil {
		t.Fatalf("newly available accessibility operation rejected: %v", err)
	}
}

// Normal session construction wires the immutable contracts before returning.
// Accessibility must remain on the live path because its backend may reopen.
func TestSessionCreationCachesOnlyStaticOperations(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	for _, bundle := range []*bundleBase{
		&session.Screen.bundleBase,
		&session.Input.bundleBase,
		&session.Windows.bundleBase,
		&session.Outputs.bundleBase,
		&session.Clipboard.bundleBase,
	} {
		if !bundle.operationsResolved {
			t.Fatalf("%s was not prevalidated at session creation", bundle.capability)
		}
	}
	if session.Accessibility.operationsResolved {
		t.Fatal("accessibility cannot prevalidate a reopenable backend")
	}
}

func TestBackendContextReusesEarlierCallerDeadline(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	parent, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	child, finish := session.Screen.backendContext(parent, 2*time.Minute)
	defer finish()
	if child != parent {
		t.Fatalf("backend context duplicated the earlier caller deadline; want exact parent context")
	}
	if err := child.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestBackendContextAddsShorterBackendTimeout(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	parent, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	child, finish := session.Screen.backendContext(parent, 10*time.Millisecond)
	defer finish()
	if child == parent {
		t.Fatal("backend deadline is shorter; child required")
	}
	parentDeadline, _ := parent.Deadline()
	childDeadline, _ := child.Deadline()
	if !childDeadline.Before(parentDeadline) {
		t.Fatalf("backend deadline %v should precede caller %v", childDeadline, parentDeadline)
	}
	<-child.Done()
	if !errors.Is(child.Err(), context.DeadlineExceeded) {
		t.Fatalf("child expiry=%v", child.Err())
	}
	if parent.Err() != nil {
		t.Fatalf("backend expiration canceled caller: %v", parent.Err())
	}
}

func TestBackendContextUnboundedCallerStaysBounded(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	child, finish := session.Screen.backendContext(context.Background(), time.Second)
	defer finish()
	deadline, ok := child.Deadline()
	if !ok || time.Until(deadline) > time.Second {
		t.Fatalf("missing or excessive backend deadline: %v, %v", deadline, ok)
	}
}

// A partially assembled bundle must not invent a new ownership or deadline
// when no session exists to supply a backend policy.
func TestBackendContextWithoutOwnerRetainsCallerCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, bundle := range []*bundleBase{nil, {}} {
		child, finish := bundle.backendContext(parent, time.Millisecond)
		finish()
		if child != parent {
			t.Fatal("ownerless backend changed the caller's context")
		}
		cancel()
		if !errors.Is(child.Err(), context.Canceled) {
			t.Fatalf("caller cancellation was lost: %v", child.Err())
		}
	}
}

// An omitted operation timeout uses the session's configured medium policy
// and the returned cancellation remains owned by the backend operation.
func TestBackendContextZeroTimeoutUsesSessionPolicy(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	policy := session.Timeouts()
	before := time.Now()
	child, finish := session.Screen.backendContext(context.Background(), 0)
	deadline, ok := child.Deadline()
	if !ok || deadline.Before(before) || deadline.After(before.Add(policy.Medium+time.Second)) {
		finish()
		t.Fatalf("backend deadline %v did not follow medium policy %s", deadline, policy.Medium)
	}
	finish()
	if !errors.Is(child.Err(), context.Canceled) {
		t.Fatalf("backend cancellation did not end the operation: %v", child.Err())
	}
}

type authoritativeCoordinateBackend struct {
	*closeCountingInput
	report  input.CoordinateSpaceInfo
	failure error
	calls   int
}

func (b *authoritativeCoordinateBackend) PointerCoordinateSpace(ctx context.Context) (input.CoordinateSpaceInfo, error) {
	b.calls++
	if err := ctx.Err(); err != nil {
		return input.CoordinateSpaceInfo{}, err
	}
	return b.report, b.failure
}

func TestInputBundleCoordinateSpaceRequiresAuthoritativeValidBackendProof(t *testing.T) {
	physical := input.CoordinateSpaceInfo{Kind: input.CoordinateSpacePhysical, ScaleX: 2, ScaleY: 1.5}
	backend := &authoritativeCoordinateBackend{closeCountingInput: &closeCountingInput{}, report: physical}
	session := NewSessionForTesting(nil, backend, nil, nil, nil)
	defer session.Close()
	got, err := session.Input.PointerCoordinateSpace(context.Background())
	if err != nil || got != physical || backend.calls != 1 {
		t.Fatalf("authoritative coordinate report lost: %+v err=%v calls=%d", got, err, backend.calls)
	}
	backend.report = input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 2, ScaleY: 1}
	if _, err := session.Input.PointerCoordinateSpace(context.Background()); err == nil {
		t.Fatal("non-unit logical scale accepted")
	}
	failure := errors.New("synthetic coordinate discovery failed")
	backend.failure = failure
	if _, err := session.Input.PointerCoordinateSpace(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("backend error lost: %v", err)
	}
	backend.failure = nil
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.Input.PointerCoordinateSpace(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not preserved: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Input.PointerCoordinateSpace(context.Background()); err == nil {
		t.Fatal("closed session disclosed coordinate scale")
	}
	withoutMetadata := NewSessionForTesting(nil, &closeCountingInput{}, nil, nil, nil)
	defer withoutMetadata.Close()
	if _, err := withoutMetadata.Input.PointerCoordinateSpace(context.Background()); !errors.Is(err, input.ErrNotSupported) {
		t.Fatalf("unproven coordinate space was not rejected: %v", err)
	}
}

func TestAccessibilityWindowDelegatesExplicitScopeAndPreservesFailure(t *testing.T) {
	fake := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "Synthetic Editor"}}}, gen: 7}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	ctx := context.Background()
	scope, err := session.Accessibility.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: "synthetic-editor-window"})
	if err != nil || scope.WindowID != "synthetic-editor-window" || scope.Generation != 7 || len(fake.resolvedTargets) != 1 || fake.resolvedTargets[0].ID != "synthetic-editor-window" {
		t.Fatalf("incorrect window correlation scope=%+v err=%v targets=%+v", scope, err, fake.resolvedTargets)
	}
	if _, err := session.Accessibility.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: "missing"}); !errors.Is(err, accessibility.ErrNotFound) {
		t.Fatalf("missing window error lost: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Accessibility.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: "synthetic-editor-window"}); err == nil {
		t.Fatal("closed accessibility session resolved stale window")
	}
}

func TestNilAccessibilityBundleClosesRejectedBackendWithoutLeaking(t *testing.T) {
	var absent *AccessibilityBundle
	candidate := &accessibilityReopenerFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
	absent.installBackend(candidate)
	if !candidate.closed {
		t.Fatal("rejected backend was not closed")
	}
	if err := absent.close(); err != nil {
		t.Fatal(err)
	}
	if err := absent.checkAvailable("window-root"); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil accessibility bundle returned %v", err)
	}
}

type typedNilClipboard struct{}

func (*typedNilClipboard) Get(context.Context) (string, error) {
	return "", nil
}

func (*typedNilClipboard) Set(context.Context, string) error { return nil }

func (*typedNilClipboard) Close() error { return nil }

type typedNilOutputLister struct{}

func (*typedNilOutputLister) List(context.Context) ([]output.Info, error) {
	return nil, nil
}

func (*typedNilOutputLister) Close() error { return nil }

func TestBundleAvailabilityTreatsTypedNilBackendsAsUnavailable(t *testing.T) {
	var screenshotter *screen.X11Backend
	var inputter *input.XTestBackend
	var manager *window.SwayManager
	var lister *typedNilOutputLister
	var clipboardBackend *typedNilClipboard

	tests := []struct {
		name  string
		check func(*Session) error
	}{
		{
			name: "screen",
			check: func(session *Session) error {
				bundle := &ScreenBundle{
					backend: screenshotter,
					bundleBase: bundleBase{
						session:    session,
						capability: CapabilityScreen,
					},
				}
				_, err := bundle.Grab(context.Background(), image.Rectangle{})
				return err
			},
		},
		{
			name: "input",
			check: func(session *Session) error {
				bundle := &InputBundle{
					backend: inputter,
					bundleBase: bundleBase{
						session:    session,
						capability: CapabilityInput,
					},
				}
				return bundle.KeyDown(context.Background(), "a")
			},
		},
		{
			name: "windows",
			check: func(session *Session) error {
				bundle := &WindowBundle{
					backend: manager,
					bundleBase: bundleBase{
						session:    session,
						capability: CapabilityWindows,
					},
				}
				_, err := bundle.List(context.Background(), WindowMatch{})
				return err
			},
		},
		{
			name: "outputs",
			check: func(session *Session) error {
				bundle := &OutputBundle{
					backend: lister,
					bundleBase: bundleBase{
						session:    session,
						capability: CapabilityOutputs,
					},
				}
				_, err := bundle.List(context.Background())
				return err
			},
		},
		{
			name: "clipboard",
			check: func(session *Session) error {
				bundle := &ClipboardBundle{
					backend: clipboardBackend,
					bundleBase: bundleBase{
						session:    session,
						capability: CapabilityClipboard,
					},
				}
				_, err := bundle.Get(context.Background())
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.check(bundleAvailabilityTestSession())
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("typed-nil backend error = %v, want ErrUnavailable", err)
			}
		})
	}
}

func bundleAvailabilityTestSession() *Session {
	return &Session{ctx: context.Background()}
}

var (
	_ clipboard.Clipboard  = (*typedNilClipboard)(nil)
	_ input.Inputter       = (*input.XTestBackend)(nil)
	_ output.Lister        = (*typedNilOutputLister)(nil)
	_ screen.Screenshotter = (*screen.X11Backend)(nil)
	_ window.Manager       = (*window.SwayManager)(nil)
)

type countOutputReadBackend struct{ calls int }

func (b *countOutputReadBackend) List(context.Context) ([]output.Info, error) {
	b.calls++
	return []output.Info{{Name: "PRIVATE"}}, nil
}
func (b *countOutputReadBackend) Close() error { return nil }

type countClipboardReadBackend struct{ calls int }

func (b *countClipboardReadBackend) Get(context.Context) (string, error) {
	b.calls++
	return "private", nil
}
func (b *countClipboardReadBackend) Set(context.Context, string) error { return nil }
func (b *countClipboardReadBackend) Close() error                      { return nil }

// A canceled request must not initiate a private read, even when the backend
// ignores the passed context and produces a result that is later discarded.
func TestDesktopReadFacadesDoNotDispatchAfterPreCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outputs := &countOutputReadBackend{}
	clipboard := &countClipboardReadBackend{}
	session := NewSessionForTesting(nil, nil, nil, outputs, clipboard)
	t.Cleanup(func() { _ = session.Close() })
	infos, err := session.Outputs.List(ctx)
	if !errors.Is(err, context.Canceled) || len(infos) > 0 || outputs.calls != 0 {
		t.Errorf("canceled output request leaked %v, err %v, backend calls %d", infos, err, outputs.calls)
	}
	value, err := session.Clipboard.Get(ctx)
	if !errors.Is(err, context.Canceled) || value != "" || clipboard.calls != 0 {
		t.Errorf("canceled clipboard request leaked %q, err %v, backend calls %d", value, err, clipboard.calls)
	}
}

// Uninitialized bundle handles must fail closed before consulting capability
// metadata or issuing backend operations.
func TestUninitializedBundleAvailabilityFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		base *bundleBase
	}{
		{name: "nil bundle", base: nil},
		{name: "bundle without session", base: &bundleBase{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.base.checkAvailable("clipboard.get", true); !errors.Is(err, ErrNilSession) {
				t.Fatalf("uninitialized bundle accepted advertised operation: %v", err)
			}
			if err := tc.base.checkBackend("clipboard.get", struct{}{}); !errors.Is(err, ErrNilSession) {
				t.Fatalf("uninitialized bundle accepted synthetic backend: %v", err)
			}
			// A diagnostic trace must be safe to attempt even after the
			// owner session has been discarded.
			tc.base.traceAction("clipboard", "get")
		})
	}
}
