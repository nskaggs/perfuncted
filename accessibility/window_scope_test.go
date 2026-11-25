package accessibility

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"math"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Fresh window correlation is a snapshot transaction: provider changes that
// can invalidate the selected application's identity or observed candidates
// must force retry, while changes on unrelated applications must not.
func TestFreshWindowResolutionObservationChangeRelevanceMatrix(t *testing.T) {
	desktop := objectIdentity{busName: registryName, objectPath: "/desktop"}
	appID := objectIdentity{busName: "org.example.App", objectPath: "/app"}
	windowID := objectIdentity{busName: appID.busName, objectPath: "/window"}
	foreignID := objectIdentity{busName: "org.foreign.App", objectPath: "/other"}
	apps := []Application{{Node: Node{ID: NodeID{BusName: appID.busName, ObjectPath: appID.objectPath, Generation: 4}}}}
	observed := map[objectIdentity]Node{appID: apps[0].Node, windowID: {ID: NodeID{BusName: windowID.busName, ObjectPath: windowID.objectPath, Generation: 4}}}
	for _, tc := range []struct {
		name     string
		change   observationChange
		rejected bool
	}{
		{"unknown provider identity", observationChange{unknown: true}, true},
		{"desktop identity", observationChange{node: desktop}, true},
		{"desktop parent", observationChange{parent: desktop}, true},
		{"observed application", observationChange{node: appID}, true},
		{"observed window", observationChange{node: windowID}, true},
		{"new unknown child of application", observationChange{cacheAdd: true, node: objectIdentity{busName: appID.busName, objectPath: "/new-child"}, parent: appID}, true},
		{"removed observed window", observationChange{childrenRemove: true, removedChild: windowID, node: foreignID}, true},
		{"window creation same application", observationChange{kind: "org.a11y.atspi.Event.Window:Create", node: objectIdentity{busName: appID.busName, objectPath: "/new-window"}}, true},
		{"foreign unrelated property", observationChange{kind: "org.a11y.atspi.Event.Object:PropertyChange", node: foreignID}, false},
		{"foreign unrelated cache addition", observationChange{cacheAdd: true, node: foreignID}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := windowResolutionChangeError(tc.change, desktop, apps, observed)
			if tc.rejected && !errors.Is(err, ErrObservationChanged) {
				t.Fatalf("stale window resolution was not rejected: %v", err)
			}
			if !tc.rejected && err != nil {
				t.Fatalf("unrelated application change rejected: %v", err)
			}
		})
	}
}

func TestFreshApplicationResolutionObservationChangeRelevanceMatrix(t *testing.T) {
	desktop := NodeID{BusName: registryName, ObjectPath: "/desktop", Generation: 4}
	app := Node{ID: NodeID{BusName: "org.example.App", ObjectPath: "/app", Generation: 4}}
	unrelated := objectIdentity{busName: "org.foreign.App", objectPath: "/unrelated"}
	for _, tc := range []struct {
		name   string
		change observationChange
		reject bool
	}{
		{"unknown identity", observationChange{unknown: true}, true},
		{"desktop itself", observationChange{node: objectIdentity{busName: desktop.BusName, objectPath: desktop.ObjectPath}}, true},
		{"desktop as parent", observationChange{parent: objectIdentity{busName: desktop.BusName, objectPath: desktop.ObjectPath}}, true},
		{"application itself", observationChange{node: objectIdentity{busName: app.ID.BusName, objectPath: app.ID.ObjectPath}}, true},
		{"application removed child", observationChange{removedChild: objectIdentity{busName: app.ID.BusName, objectPath: app.ID.ObjectPath}}, true},
		{"unknown removal within observed app", observationChange{cacheRemove: true, node: objectIdentity{busName: app.ID.BusName, objectPath: "/other"}}, true},
		{"foreign removal", observationChange{cacheRemove: true, node: unrelated}, false},
		{"unrelated property", observationChange{node: unrelated}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			change := tc.change
			change.revision = 2
			b := &dbusBackend{generation: 4, observationRevision: 2, observationHistory: []observationChange{change}}
			err := b.applicationResolutionChangesError(1, desktop, []Node{app})
			if tc.reject && !errors.Is(err, ErrObservationChanged) {
				t.Fatalf("relevant application change accepted: %v", err)
			}
			if !tc.reject && err != nil {
				t.Fatalf("unrelated provider change rejected: %v", err)
			}
		})
	}
	b := &dbusBackend{generation: 4, observationRevision: 2, observationFloor: 2}
	if err := b.applicationResolutionChangesError(1, desktop, []Node{app}); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("expired observation history accepted: %v", err)
	}
	var missing *dbusBackend
	if err := missing.applicationResolutionChangesError(1, desktop, []Node{app}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil backend returned %v", err)
	}
}

// Process ownership must be verified before fresh window correlation. A
// desktop child on an absent service is not an application with the requested
// PID, even if the reference itself has a syntactically valid object path.
func TestFreshWindowScopeRejectsUnavailableProcessOwner(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
		root.children = []objectRef{{BusName: ":1.999999", ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/vanished")}}
	})
	apps, err := backend.windowApplicationsFresh(context.Background(), WindowTarget{ID: "managed-window", PID: 48123})
	if err == nil || len(apps) != 0 || !strings.Contains(err.Error(), "resolve window application PID") {
		t.Fatalf("unverified process owner admitted: apps=%+v err=%v", apps, err)
	}
}

// A frame can disappear between discovery and GetApplication. An inaccessible
// provider method must not populate the toolkit cache with an invented name.
func TestToolkitForVanishedNodeDoesNotCacheProviderIdentity(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackend(t, false)
	vanished := backend.refID(objectRef{BusName: id.BusName, ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/vanished")})
	name, err := backend.toolkitForNode(context.Background(), vanished)
	if err == nil || name != "" || !strings.Contains(err.Error(), "get application") || errors.Is(err, ErrStaleNode) {
		t.Fatalf("vanished provider accepted as toolkit: name=%q err=%v", name, err)
	}
	backend.mu.RLock()
	cached := len(backend.toolkits)
	backend.mu.RUnlock()
	if cached != 0 {
		t.Fatalf("vanished application cached toolkit identity: %d", cached)
	}
}

// Fresh desktop application enumeration is an authorization precondition for
// window correlation. Cancellation at any observed boundary must never leak a
// partially enumerated set of application identities.
func TestWindowApplicationsFreshCancellationBoundaryMatrix(t *testing.T) {
	backend, _, target := newSyntheticATSPIBackend(t, false)
	base, stop := context.WithCancel(context.Background())
	baseline := &cancelAtAccessibilityContextRead{Context: base, cancel: stop, threshold: 10000}
	apps, err := backend.windowApplicationsFresh(baseline, target)
	stop()
	if err != nil || len(apps) != 1 {
		t.Fatalf("baseline applications=%+v err=%v", apps, err)
	}
	checks := baseline.calls.Load()
	if checks < 2 {
		t.Fatalf("too few cancellation boundaries: %d", checks)
	}
	for index := int32(1); index <= checks; index++ {
		ctxBase, cancel := context.WithCancel(context.Background())
		ctx := &cancelAtAccessibilityContextRead{Context: ctxBase, cancel: cancel, threshold: index}
		got, readErr := backend.windowApplicationsFresh(ctx, target)
		canceledDuringRead := ctxBase.Err() != nil
		cancel()
		if canceledDuringRead && (!errors.Is(readErr, context.Canceled) || len(got) != 0) {
			t.Fatalf("cancellation on check %d/%d released applications=%+v err=%v", index, checks, got, readErr)
		}
	}
}

// Cached window application discovery still requires a live caller at each
// application boundary. Cancellation must not return a partial owner list.
func TestWindowApplicationsCachedCancellationBoundaryMatrix(t *testing.T) {
	backend, _, target := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
		root.children = append(root.children, root.children[0])
	})
	base, stop := context.WithCancel(context.Background())
	baseline := &cancelAtAccessibilityContextRead{Context: base, cancel: stop, threshold: 10000}
	apps, err := backend.windowApplications(baseline, target)
	stop()
	if err != nil || len(apps) != 2 {
		t.Fatalf("baseline cached owners=%+v err=%v", apps, err)
	}
	checks := baseline.calls.Load()
	if checks < 2 {
		t.Fatalf("too few cancellation boundaries: %d", checks)
	}
	for index := int32(1); index <= checks; index++ {
		live, cancel := context.WithCancel(context.Background())
		ctx := &cancelAtAccessibilityContextRead{Context: live, cancel: cancel, threshold: index}
		result, readErr := backend.windowApplications(ctx, target)
		canceledDuringRead := live.Err() != nil
		cancel()
		if canceledDuringRead && (!errors.Is(readErr, context.Canceled) || len(result) != 0) {
			t.Fatalf("cancel on boundary %d/%d returned owners=%+v err=%v", index, checks, result, readErr)
		}
	}
}

// Application references recovered before a provider reconnect cannot be
// promoted into a fresh window scope after the backend generation changes.
// Even an otherwise matching PID must not authorize a stale application.
func TestFreshWindowCandidatesRejectOldApplicationGeneration(t *testing.T) {
	backend := &dbusBackend{generation: 8}
	stale := Application{Node: Node{ID: NodeID{BusName: "org.example.Editor", ObjectPath: "/application", Generation: 7}}, PID: 456}
	candidates, observed, err := backend.windowCandidatesFresh(context.Background(), WindowTarget{ID: "managed-editor", PID: 456}, []Application{stale})
	if !errors.Is(err, ErrStaleGeneration) || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("stale app authorized fresh window correlation: candidates=%v observed=%v err=%v", candidates, observed, err)
	}
	// A caller's cancellation is authoritative even when no provider child
	// needs to be enumerated and no candidate can be returned.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	candidates, observed, err = backend.windowCandidatesFresh(ctx, WindowTarget{ID: "managed-editor"}, nil)
	if !errors.Is(err, context.Canceled) || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("canceled correlation returned candidates=%v observed=%v err=%v", candidates, observed, err)
	}
	// No caller context must not implicitly authorize new provider reads.
	candidates, observed, err = backend.windowCandidatesFresh(nil, WindowTarget{ID: "managed-editor"}, nil)
	if err == nil || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("nil-context correlation accepted: candidates=%v observed=%v err=%v", candidates, observed, err)
	}
	// Matching-generation empty results remain a valid observation, rather
	// than being mistaken for a stale provider or a canceled lookup.
	candidates, observed, err = backend.windowCandidatesFresh(context.Background(), WindowTarget{ID: "managed-editor"}, nil)
	if err != nil || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("empty current-generation correlation failed: candidates=%v observed=%v err=%v", candidates, observed, err)
	}
}

// When multiple accessible frames have identical authoritative evidence,
// the ambiguity report must enumerate all co-best candidates. Omitting the
// later matches hides other potential targets and makes the diagnostic depend
// on enumeration order.
func TestWindowCandidateAmbiguityReportsEveryBestCandidate(t *testing.T) {
	candidates := []windowCandidate{
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/one", Generation: 1}, Name: "Editor"}, score: 90},
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/low", Generation: 1}, Name: "Other"}, score: 20},
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/two", Generation: 1}, Name: "Editor"}, score: 90},
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/three", Generation: 1}, Name: "Editor"}, score: 90},
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/four", Generation: 1}, Name: "Editor"}, score: 90},
	}
	selected, matches, ambiguous := chooseWindowCandidate(candidates)
	if selected != nil || !ambiguous || len(matches) != 4 {
		t.Fatalf("ambiguous selection: selected=%+v ambiguous=%v candidates=%+v, want all four co-best", selected, ambiguous, matches)
	}
	for i, path := range []string{"/one", "/two", "/three", "/four"} {
		if matches[i].ID.ObjectPath != path {
			t.Fatalf("candidate %d = %+v, want path %s", i, matches[i], path)
		}
	}
}

// A matching application identity cannot establish window ownership when its
// provider is disconnected. Cached correlation skips the unreadable children;
// fresh correlation must fail closed without publishing a candidate scope.
func TestWindowCorrelationRejectsUnreadableMatchedApplicationChildren(t *testing.T) {
	backend := &dbusBackend{generation: 9}
	app := Application{Node: Node{ID: NodeID{BusName: "org.example.PrivateEditor", ObjectPath: "/app", Generation: 9}}, PID: 321}
	target := WindowTarget{ID: "managed-private-editor", PID: 321}
	if !windowAppMatches(app, target) {
		t.Fatal("test application did not match target ownership")
	}
	if got := backend.windowCandidates(context.Background(), target, []Application{app}); len(got) != 0 {
		t.Fatalf("disconnected provider yielded cached window candidates: %+v", got)
	}
	candidates, observed, err := backend.windowCandidatesFresh(context.Background(), target, []Application{app})
	if !errors.Is(err, ErrDisconnected) || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("disconnected provider authorized fresh window: candidates=%+v observed=%+v err=%v", candidates, observed, err)
	}
}

// Once an application has been correlated to a managed process, fresh window
// resolution must still refuse an incomplete or unbounded child enumeration.
// A private AT-SPI frame acts as the application object for this direct
// candidate-enumeration boundary test; no host accessibility service is used.
func TestFreshWindowCandidatesRejectFaultedAndOversizedChildEnumeration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*syntheticATSPINode)
		want      error
	}{
		{"provider read failure", func(frame *syntheticATSPINode) {
			frame.childrenFailure = "permission revoked during window enumeration"
		}, nil},
		{"oversized window child list", func(frame *syntheticATSPINode) {
			frame.children = make([]objectRef, maxApplications+1)
			for i := range frame.children {
				frame.children[i] = objectRef{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/editor"}
			}
		}, ErrIncompleteSnapshot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, id, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) { tc.configure(frame) })
			app := Application{Node: Node{ID: id}, PID: 4123}
			target := WindowTarget{ID: "managed-frame", PID: 4123}
			if !windowAppMatches(app, target) {
				t.Fatal("test application not matched to authoritative process")
			}
			candidates, observed, err := backend.windowCandidatesFresh(context.Background(), target, []Application{app})
			if err == nil || len(candidates) != 0 || len(observed) != 0 {
				t.Fatalf("bad child enumeration admitted candidates: candidates=%+v observed=%+v err=%v", candidates, observed, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
		})
	}
}

// Cancel every observed boundary in fresh candidate enumeration against an
// isolated AT-SPI provider. No partial candidate set or observed private node
// may be released once the caller's scope authority has been revoked.
func TestFreshWindowCandidateEnumerationCancellationBoundaryMatrix(t *testing.T) {
	backend, frameID, _ := newSyntheticATSPIBackend(t, false)
	app := Application{Node: Node{ID: frameID}, PID: 7391}
	target := WindowTarget{ID: "managed-7391", PID: 7391}
	base, stop := context.WithCancel(context.Background())
	probe := &cancelAtAccessibilityContextRead{Context: base, cancel: stop, threshold: 10000}
	candidates, observed, err := backend.windowCandidatesFresh(probe, target, []Application{app})
	stop()
	if err != nil || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("baseline fresh candidate enumeration: candidates=%+v observed=%+v err=%v", candidates, observed, err)
	}
	checks := probe.calls.Load()
	if checks < 3 {
		t.Fatalf("insufficient candidate cancellation boundaries: %d", checks)
	}
	for index := int32(1); index <= checks; index++ {
		live, cancel := context.WithCancel(context.Background())
		ctx := &cancelAtAccessibilityContextRead{Context: live, cancel: cancel, threshold: index}
		got, seen, readErr := backend.windowCandidatesFresh(ctx, target, []Application{app})
		canceledDuringRead := live.Err() != nil
		cancel()
		if canceledDuringRead && (!errors.Is(readErr, context.Canceled) || len(got) != 0 || len(seen) != 0) {
			t.Fatalf("cancel at check %d/%d published candidates=%+v observed=%+v err=%v", index, checks, got, seen, readErr)
		}
	}
}

// After an authorized application has exposed its children, cancellation may
// race with the first child read. It must reject the entire candidate set,
// never publish a partial window identity.
func TestFreshWindowChildIterationCancellationBoundaryMatrix(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/editor")}}
	})
	app := Application{Node: Node{ID: id}, PID: 4754}
	target := WindowTarget{ID: "managed-4754", PID: 4754, Title: "Editor"}
	probeBase, probeCancel := context.WithCancel(context.Background())
	probe := &cancelAtAccessibilityContextRead{Context: probeBase, cancel: probeCancel, threshold: 10000}
	candidates, observed, err := backend.windowCandidatesFresh(probe, target, []Application{app})
	probeCancel()
	if err != nil || len(observed) != 1 || len(candidates) != 1 {
		t.Fatalf("baseline window candidates=%+v observed=%+v err=%v", candidates, observed, err)
	}
	checks := probe.calls.Load()
	if checks < 3 {
		t.Fatalf("not enough candidate admission checks: %d", checks)
	}
	for index := int32(1); index <= checks; index++ {
		base, cancel := context.WithCancel(context.Background())
		ctx := &cancelAtAccessibilityContextRead{Context: base, cancel: cancel, threshold: index}
		got, seen, readErr := backend.windowCandidatesFresh(ctx, target, []Application{app})
		duringRead := base.Err() != nil
		cancel()
		if duringRead && (!errors.Is(readErr, context.Canceled) || len(got) != 0 || len(seen) != 0) {
			t.Fatalf("canceled at check %d/%d returned candidates=%+v observed=%+v err=%v", index, checks, got, seen, readErr)
		}
	}
}

// Applications can publish child references to objects that disappear before
// the compositor resolves their identity. Fresh correlation cannot silently
// discard these failures and publish an incomplete set of candidate windows.
func TestFreshWindowCandidateEnumerationRejectsVanishedChildObject(t *testing.T) {
	backend, frameID, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/vanished_child")}}
	})
	app := Application{Node: Node{ID: frameID}, PID: 4317}
	candidates, observed, err := backend.windowCandidatesFresh(context.Background(), WindowTarget{ID: "managed-4317", PID: 4317}, []Application{app})
	if err == nil || len(candidates) != 0 || len(observed) != 0 || !strings.Contains(err.Error(), "resolve fresh window candidate") {
		t.Fatalf("vanished child authorized incomplete candidates: candidates=%+v observed=%+v err=%v", candidates, observed, err)
	}
}

// A fresh window correlation must not publish identity gathered across a
// desktop observation-history rollover. One rollover permits a new lookup;
// repeated rollovers must exhaust the two-attempt retry budget.
func TestFreshWindowScopeBoundsObservationHistoryRetries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rollovers int
		wantError bool
	}{
		{name: "single rollover retries", rollovers: 1},
		{name: "repeated rollovers fail closed", rollovers: 2, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var live atomic.Pointer[dbusBackend]
			var reads atomic.Int32
			backend, _, target := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
				root.childrenHook = func() {
					count := reads.Add(1)
					if b := live.Load(); b != nil && count <= int32(tc.rollovers) {
						b.mu.Lock()
						b.observationRevision++
						b.observationFloor = b.observationRevision
						b.mu.Unlock()
					}
				}
			})
			live.Store(backend)
			scope, err := backend.ResolveWindowFresh(context.Background(), target)
			if tc.wantError {
				if !errors.Is(err, ErrObservationChanged) || scope.Root.valid() {
					t.Fatalf("incomplete observation history exposed scope=%+v err=%v", scope, err)
				}
			} else if err != nil || !scope.Root.valid() || scope.Generation != backend.Generation() {
				t.Fatalf("fresh retry failed: scope=%+v err=%v", scope, err)
			}
			if got := reads.Load(); got != 2 {
				t.Fatalf("desktop reads=%d; want bounded two-attempt lookup", got)
			}
		})
	}
}

// Fresh window correlation must refuse incomplete provider identity, even
// when the window's name and process ownership were otherwise plausible.
// Exercise independently failed properties and methods over private AT-SPI.
func TestFreshWindowCandidatesRejectPartialProviderIdentity(t *testing.T) {
	for _, tc := range []struct {
		name            string
		fail            func(*syntheticATSPINode)
		needDescription bool
	}{
		{"description unavailable", func(n *syntheticATSPINode) { n.propertyFailure = "Description" }, true},
		{"role unavailable", func(n *syntheticATSPINode) { n.roleFailure = "role disconnected" }, true},
		{"states unavailable", func(n *syntheticATSPINode) { n.stateFailure = "states disconnected" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, id, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) { tc.fail(frame) })
			parent := NodeID{BusName: id.BusName, ObjectPath: "/org/a11y/atspi/accessible/app", Generation: id.Generation}
			got, err := backend.readWindowCandidateFresh(context.Background(), id, parent, tc.needDescription)
			if err == nil || got.ID.valid() || got.Name != "" || got.Role != "" {
				t.Fatalf("partial provider identity admitted: node=%+v err=%v", got, err)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "provider refused") && !strings.Contains(err.Error(), "disconnected") {
				t.Fatalf("provider failure not preserved: %v", err)
			}
		})
	}
}

// Window identity cannot depend on signed-integer wraparound in coordinates
// or area. AT-SPI rectangle values are untrusted provider data.
func TestWindowCandidateGeometryRejectsOverflowDrivenFalseNegatives(t *testing.T) {
	cases := []struct {
		name         string
		node, target Rect
	}{
		{"far positive endpoint", Rect{X: 40, Y: 20, Width: 20, Height: 10}, Rect{X: 30, Y: 20, Width: math.MaxInt, Height: 10}},
		{"far negative endpoint", Rect{X: -40, Y: 20, Width: 20, Height: 10}, Rect{X: math.MinInt + 4, Y: 20, Width: math.MaxInt, Height: 10}},
		{"area multiplication", Rect{X: 0, Y: 0, Width: 1 << 40, Height: 1 << 30}, Rect{X: 0, Y: 0, Width: 1 << 40, Height: 1 << 30}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rectOverlap(tc.node, tc.target); got <= 0 {
				t.Errorf("actual positive overlap rejected, got=%d", got)
			}
			target := WindowTarget{ID: "managed", Bounds: tc.target}
			node := Node{Name: "Editor", Role: "frame", HasBounds: true, Bounds: tc.node}
			if score := windowCandidateScore(node, target); score == 0 {
				t.Error("valid geometric window correlation was rejected")
			}
		})
	}
}

func TestWindowCandidateGeometryOverflowNeverInventsIntersection(t *testing.T) {
	cases := []struct {
		name string
		a, b Rect
		want int
	}{
		{"ordinary overlap", Rect{X: 2, Y: 3, Width: 8, Height: 10}, Rect{X: 5, Y: 9, Width: 10, Height: 6}, 20},
		{"adjacent rectangles", Rect{X: 0, Y: 0, Width: 10, Height: 10}, Rect{X: 10, Y: 0, Width: 10, Height: 10}, 0},
		{"separated extremes", Rect{X: math.MinInt, Y: 0, Width: 2, Height: 10}, Rect{X: math.MaxInt - 2, Y: 0, Width: 2, Height: 10}, 0},
		{"invalid width", Rect{X: 0, Y: 0, Width: -1, Height: 10}, Rect{X: 0, Y: 0, Width: 10, Height: 10}, 0},
		{"invalid height", Rect{X: 0, Y: 0, Width: 10, Height: 0}, Rect{X: 0, Y: 0, Width: 10, Height: 10}, 0},
		{"saturating area", Rect{X: 0, Y: 0, Width: 1 << 40, Height: 1 << 30}, Rect{X: 0, Y: 0, Width: 1 << 40, Height: 1 << 30}, math.MaxInt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rectOverlap(tc.a, tc.b); got != tc.want {
				t.Fatalf("overlap=%d, want=%d", got, tc.want)
			}
			if got := rectOverlap(tc.b, tc.a); got != tc.want {
				t.Fatalf("reversed overlap=%d, want=%d", got, tc.want)
			}
		})
	}
}

// A candidate object can vanish after its application has been enumerated.
// A fresh window resolution must reject missing provider properties rather
// than promoting the application's PID or a partial candidate to authority.
func TestWindowCandidateFreshRejectsVanishedObjectProperties(t *testing.T) {
	backend, frameID, _ := newSyntheticATSPIBackend(t, false)
	parent := NodeID{BusName: frameID.BusName, ObjectPath: "/org/a11y/atspi/accessible/app", Generation: frameID.Generation}
	vanished := NodeID{BusName: frameID.BusName, ObjectPath: "/org/a11y/atspi/accessible/removed", Generation: frameID.Generation}
	for _, needDescription := range []bool{false, true} {
		got, err := backend.readWindowCandidateFresh(context.Background(), vanished, parent, needDescription)
		if err == nil || got.ID.valid() || got.Name != "" || got.Role != "" {
			t.Fatalf("removed AT-SPI object became a window: description=%v candidate=%+v err=%v", needDescription, got, err)
		}
	}
}

// A caller may cancel after the desktop application inventory was read but
// before fresh candidate traversal begins. Even an empty candidate inventory
// must respect cancellation rather than masquerade as a successful read.
func TestFreshWindowCandidatesRefuseCanceledEmptyApplicationInventory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &dbusBackend{generation: 3}
	candidates, observed, err := backend.windowCandidatesFresh(ctx, WindowTarget{ID: "private", Title: "Console"}, nil)
	if !errors.Is(err, context.Canceled) || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("canceled scope candidate traversal returned candidates=%+v observed=%+v err=%v", candidates, observed, err)
	}
}

// PIDHint is still an ownership boundary when the compositor cannot supply a
// primary PID. Neither a matching title nor an application name may promote
// a window from a different process into an authorized candidate.
func TestWindowApplicationPIDHintDoesNotDowngradeOwnershipProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		app    Application
		target WindowTarget
		want   bool
	}{
		{"matching PID hint with divergent app id", Application{Node: Node{Name: "Firefox"}, PID: 42}, WindowTarget{PIDHint: 42, AppID: "org.mozilla.Firefox", Title: "Browser Console"}, true},
		{"mismatching PID hint even with title", Application{Node: Node{Name: "Browser Console"}, PID: 43}, WindowTarget{PIDHint: 42, Title: "Browser Console"}, false},
		{"missing PID cannot satisfy hint", Application{Node: Node{Name: "Browser Console"}}, WindowTarget{PIDHint: 42, Title: "Browser Console"}, false},
		{"primary PID overrides conflicting hint", Application{Node: Node{Name: "Firefox"}, PID: 44}, WindowTarget{PID: 44, PIDHint: 42, Title: "Browser Console"}, true},
		{"primary PID rejects matching hint only", Application{Node: Node{Name: "Firefox"}, PID: 42}, WindowTarget{PID: 44, PIDHint: 42, Title: "Browser Console"}, false},
		{"app ID alone requires identity match", Application{Node: Node{Name: "Firefox", Description: "org.mozilla.Firefox"}, PID: 42}, WindowTarget{AppID: "org.mozilla.Firefox"}, true},
		{"app ID alone rejects unrelated name", Application{Node: Node{Name: "Text Editor"}, PID: 42}, WindowTarget{AppID: "org.mozilla.Firefox"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := windowAppMatches(tc.app, tc.target); got != tc.want {
				t.Fatalf("app=%+v target=%+v matched=%v want=%v", tc.app, tc.target, got, tc.want)
			}
		})
	}
}

// Candidate discovery must reject mismatching process ownership before
// contacting any accessibility backend, including for an active/title-matched
// window that otherwise appears highly credible.
func TestWindowCandidatesSkipUnownedApplicationsBeforeBackendAccess(t *testing.T) {
	var backend *dbusBackend
	apps := []Application{
		{Node: Node{ID: NodeID{BusName: "org.unknown", ObjectPath: "/application", Generation: 1}, Name: "Browser Console"}, PID: 99},
		{Node: Node{ID: NodeID{BusName: "org.missing", ObjectPath: "/application", Generation: 1}, Name: "Browser Console"}, PID: 0},
	}
	target := WindowTarget{ID: "window1", PIDHint: 42, Title: "Browser Console", Active: true}
	candidates := backend.windowCandidates(context.Background(), target, apps)
	if len(candidates) != 0 {
		t.Fatalf("unowned candidates exposed: %+v", candidates)
	}
}

// A window target identified by application identity alone requires actual
// provider-reported app name/description before narrowing candidates. Neither
// fresh nor cached correlation may assume an application name from its bus ID.
func TestPrivateATSPIFreshApplicationIdentityReadsProviderMetadata(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackend(t, false)
	ctx := context.Background()
	target := WindowTarget{ID: "managed-editor", AppID: "editor"}
	fresh, err := backend.windowApplicationsFresh(ctx, target)
	if err != nil || len(fresh) != 1 || !strings.EqualFold(fresh[0].Name, "Editor") {
		t.Fatalf("fresh application identity incomplete: apps=%+v err=%v", fresh, err)
	}
	cached, err := backend.windowApplications(ctx, target)
	if err != nil || len(cached) != 1 || !strings.EqualFold(cached[0].Name, "Editor") {
		t.Fatalf("cached application identity incomplete: apps=%+v err=%v", cached, err)
	}
	if !windowAppMatches(fresh[0], target) {
		t.Fatal("matching provider name rejected for app-only correlation")
	}
	if windowAppMatches(fresh[0], WindowTarget{ID: target.ID, AppID: "not-this-application"}) {
		t.Fatal("unrelated AppID matched only by provider bus address")
	}
}

// A retired or disconnected accessibility provider cannot supply an inferred
// window identity when the node has no cached properties.
func TestWindowCandidateIdentityReadRejectsDisconnectedProvider(t *testing.T) {
	backend := &dbusBackend{generation: 12}
	id := NodeID{BusName: "org.test.Private", ObjectPath: "/window", Generation: 12}
	parent := NodeID{BusName: id.BusName, ObjectPath: "/app", Generation: 12}
	node, err := backend.readWindowCandidate(context.Background(), id, parent)
	if !errors.Is(err, ErrDisconnected) || node.ID != (NodeID{}) || node.Name != "" {
		t.Fatalf("disconnected provider returned candidate %+v err=%v", node, err)
	}
	fresh, err := backend.readWindowCandidateFresh(context.Background(), id, parent, true)
	if !errors.Is(err, ErrDisconnected) || fresh.ID != (NodeID{}) || fresh.Name != "" {
		t.Fatalf("disconnected provider returned fresh candidate %+v err=%v", fresh, err)
	}
}

// Exercise genuine private AT-SPI D-Bus identity reads, never a host desktop.
// Correlation must not trust unrelated roles, guessed PIDs, or incomplete
// application enumeration. Both cached and fresh paths remain distinguishable.
func TestPrivateATSPIWindowScopeIdentityAdversarialMatrix(t *testing.T) {
	for _, tc := range []struct {
		name       string
		editNode   func(*syntheticATSPINode, *syntheticATSPINode)
		editTarget func(*WindowTarget)
		wantErr    error
		freshOnly  bool
	}{
		{name: "matching frame"},
		{name: "window role", editNode: func(_ *syntheticATSPINode, f *syntheticATSPINode) { f.role = 69 }},
		{name: "unrelated child role", editNode: func(_ *syntheticATSPINode, f *syntheticATSPINode) { f.role = 43 }, wantErr: ErrNotFound},
		{name: "missing title", editNode: func(_ *syntheticATSPINode, f *syntheticATSPINode) { f.name = ""; f.description = "" }, wantErr: ErrNotFound},
		{name: "different title", editNode: func(_ *syntheticATSPINode, f *syntheticATSPINode) {
			f.name = "Other application"
			f.description = "Other"
		}, wantErr: ErrNotFound},
		{name: "PID hint", editTarget: func(target *WindowTarget) { target.PIDHint = target.PID; target.PID = 0 }},
		{name: "foreign PID", editTarget: func(target *WindowTarget) { target.PID = int32(os.Getpid() + 99999) }, wantErr: ErrNotFound},
		{name: "unsupported ID only", editTarget: func(target *WindowTarget) { *target = WindowTarget{ID: target.ID} }, wantErr: ErrUnsupportedCorrelation},
		{name: "empty ID", editTarget: func(target *WindowTarget) { target.ID = " \t " }, wantErr: ErrNotFound},
		{name: "oversized desktop child list", editNode: func(root, _ *syntheticATSPINode) {
			ref := root.children[0]
			root.children = make([]objectRef, maxApplications+1)
			for i := range root.children {
				root.children[i] = ref
			}
		}, wantErr: ErrIncompleteSnapshot, freshOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, frameID, target := newSyntheticATSPIBackendWithFrameSetup(t, false, tc.editNode)
			if tc.editTarget != nil {
				tc.editTarget(&target)
			}
			for _, fresh := range []bool{false, true} {
				if tc.freshOnly && !fresh {
					continue
				}
				variant := "cached"
				if fresh {
					variant = "fresh"
				}
				t.Run(variant, func(t *testing.T) {
					var scope WindowScope
					var err error
					if fresh {
						scope, err = backend.ResolveWindowFresh(context.Background(), target)
					} else {
						scope, err = backend.ResolveWindow(context.Background(), target)
					}
					if tc.wantErr != nil {
						if !errors.Is(err, tc.wantErr) || scope.Root.BusName != "" {
							t.Fatalf("untrusted correlation scope=%+v error=%v, want %v", scope, err, tc.wantErr)
						}
					} else if err != nil || scope.Root != frameID || scope.ApplicationRoot.BusName == "" {
						t.Fatalf("authorized target did not resolve to its frame: %+v error=%v", scope, err)
					}
				})
			}
		})
	}
}

func TestWindowFreshIdentityRejectsStaleApplicationAndNilContext(t *testing.T) {
	backend := &dbusBackend{generation: 7}
	target := WindowTarget{ID: "win-7", PID: 42, Title: "Private"}
	app := Application{Node: Node{ID: NodeID{BusName: "org.example.App", ObjectPath: "/app", Generation: 6}}, PID: 42}
	if _, _, err := backend.windowCandidatesFresh(context.Background(), target, []Application{app}); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale app generation accepted: %v", err)
	}
	var absentContext context.Context
	if _, err := backend.windowApplicationsFresh(absentContext, target); err == nil {
		t.Fatal("nil fresh identity read context accepted")
	}
	if _, err := backend.windowApplications(absentContext, target); err == nil {
		t.Fatal("nil cached identity read context accepted")
	}
}

// The AT-SPI provider is allowed to answer GetChildren after the caller
// cancels. A window scope must not be published from that stale reply.
func TestSyntheticATSPICorrelationRejectsCancellationDuringDesktopChildrenRead(t *testing.T) {
	for _, fresh := range []bool{false, true} {
		name := "cached"
		if fresh {
			name = "fresh"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend, _, target := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, frame *syntheticATSPINode) {
				root.childrenHook = cancel
			})
			var err error
			var scope WindowScope
			if fresh {
				scope, err = backend.ResolveWindowFresh(ctx, target)
			} else {
				scope, err = backend.ResolveWindow(ctx, target)
			}
			if !errors.Is(err, context.Canceled) || scope.Root.BusName != "" {
				t.Fatalf("canceled provider response published scope %+v with err %v", scope, err)
			}
		})
	}
}

// Provider-reported application identity is required before AppID-only
// matching. Neither a missing name nor a missing description is permission
// to invent a window owner. Cached candidate resolution must also discard
// a frame when its role cannot be read.
func TestWindowIdentityProviderPropertyFailuresCannotAuthorizeOwnership(t *testing.T) {
	for _, key := range []string{"Name", "Description"} {
		t.Run("application "+key, func(t *testing.T) {
			backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, frame *syntheticATSPINode) {
				root.children = []objectRef{{BusName: registryName, ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/editor")}}
				frame.propertyFailure = key
			})
			apps, err := backend.windowApplicationsFresh(context.Background(), WindowTarget{ID: "compositor-window", AppID: "Editor"})
			if err == nil || len(apps) != 0 || !strings.Contains(err.Error(), "read window application ") {
				t.Fatalf("missing provider %s admitted application identity: apps=%+v err=%v", key, apps, err)
			}
		})
	}
	t.Run("cached candidate role", func(t *testing.T) {
		backend, id, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) { frame.roleFailure = "role no longer available" })
		parent := NodeID{BusName: id.BusName, ObjectPath: "/org/a11y/atspi/accessible/app", Generation: id.Generation}
		node, err := backend.readWindowCandidate(context.Background(), id, parent)
		if err == nil || node.ID.valid() || node.Role != "" {
			t.Fatalf("missing provider role admitted frame: node=%+v err=%v", node, err)
		}
	})
}

func TestWindowCandidateContextUsesCallerDeadlineOrFallback(t *testing.T) {
	t.Run("caller policy deadline", func(t *testing.T) {
		deadline := time.Now().Add(time.Minute)
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()

		readCtx, readCancel := windowCandidateContext(ctx)
		defer readCancel()
		got, ok := readCtx.Deadline()
		if !ok || !got.Equal(deadline) {
			t.Fatalf("candidate deadline = %v, present=%v, want caller deadline %v", got, ok, deadline)
		}
	})

	t.Run("direct caller fallback", func(t *testing.T) {
		readCtx, cancel := windowCandidateContext(context.Background())
		defer cancel()

		deadline, ok := readCtx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining <= 0 || remaining > windowCandidateReadTimeout {
			t.Fatalf("candidate deadline remaining = %v, present=%v, want positive and <= %v", remaining, ok, windowCandidateReadTimeout)
		}
	})
}

func TestWindowCandidateCorrelationUsesIndependentEvidence(t *testing.T) {
	target := WindowTarget{ID: "window-2", Title: "Editor", PID: 44, Bounds: Rect{X: 100, Y: 100, Width: 400, Height: 300}, Active: true}
	match := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/window-2", Generation: 3}, Role: "frame", Name: "Editor", Bounds: target.Bounds, HasBounds: true, Showing: true, Visible: true}
	other := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/window-1", Generation: 3}, Role: "frame", Name: "Preferences", Bounds: Rect{X: 700, Y: 100, Width: 300, Height: 200}, HasBounds: true, Showing: true, Visible: true}
	if windowCandidateScore(match, target) <= windowCandidateScore(other, target) {
		t.Fatalf("title/geometry evidence did not prefer matching window: match=%d other=%d", windowCandidateScore(match, target), windowCandidateScore(other, target))
	}
	if windowCandidateScore(other, target) != 0 {
		t.Fatalf("non-overlapping/title-mismatched window scored as a match: %d", windowCandidateScore(other, target))
	}
}

func TestWindowCorrelationWithIDOnlyRefusesToGuess(t *testing.T) {
	target := WindowTarget{ID: "window-1"}
	first := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/one", Generation: 1}, Role: "frame", Name: "One", Visible: true, Showing: true}
	second := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/two", Generation: 1}, Role: "dialog", Name: "Two", Visible: true, Showing: true}
	if windowCandidateScore(first, target) != windowCandidateScore(second, target) {
		t.Fatal("ID-only target unexpectedly gained evidence from unrelated metadata")
	}
	if windowCandidateScore(first, target) == 0 {
		t.Fatal("visible top-level candidate was discarded before ambiguity handling")
	}
	if !errors.Is((&MatchError{Err: ErrAmbiguous}), ErrAmbiguous) {
		t.Fatal("MatchError does not preserve ambiguity category")
	}
}

func TestWindowCorrelationAcceptsTitleWithCompositorAppIDWithoutPID(t *testing.T) {
	target := WindowTarget{ID: "window-1", Title: "Browser Console", AppID: "org.mozilla.firefox"}
	if !windowTargetHasAuthoritativeEvidence(target) {
		t.Fatal("title plus compositor AppID was treated as unsupported correlation")
	}
	if windowTargetHasAuthoritativeEvidence(WindowTarget{ID: "window-1", AppID: "org.mozilla.firefox"}) {
		t.Fatal("AppID-only target unexpectedly gained authoritative correlation evidence")
	}
}

func TestChooseWindowCandidateRefusesSameProcessAmbiguity(t *testing.T) {
	candidates := []windowCandidate{
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/parent", Generation: 1}, Role: "frame", Name: "Editor"}, score: 16},
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/dialog", Generation: 1}, Role: "dialog", Name: "Editor"}, score: 16},
	}
	selected, context, ambiguous := chooseWindowCandidate(candidates)
	if selected != nil || !ambiguous || len(context) != 2 {
		t.Fatalf("same-process candidates selected=%+v context=%+v ambiguous=%v", selected, context, ambiguous)
	}
}

func TestWindowAppMatchingUsesPIDBeforeCompositorAppIDSpelling(t *testing.T) {
	app := Application{Node: Node{Name: "zenity"}, PID: 42}
	if !windowAppMatches(app, WindowTarget{PID: 42, AppID: "org.gnome.Zenity"}) {
		t.Fatal("PID-correlated application rejected due to compositor/AT-SPI app-id spelling")
	}
	if windowAppMatches(app, WindowTarget{AppID: "org.gnome.Zenity"}) {
		t.Fatal("app-id-only target matched unrelated application")
	}
}

func TestWindowCandidateScoreRejectsChangedTitleAndGeometry(t *testing.T) {
	target := WindowTarget{Title: "Editor", Bounds: Rect{X: 10, Y: 20, Width: 300, Height: 200}}
	node := Node{Name: "Editor", Role: "frame", Bounds: target.Bounds, HasBounds: true, Showing: true}
	if score := windowCandidateScore(node, target); score <= 0 {
		t.Fatalf("matching candidate score = %d, want positive", score)
	}

	changedTitle := node
	changedTitle.Name = "Preferences"
	if score := windowCandidateScore(changedTitle, target); score != 0 {
		t.Fatalf("changed-title candidate score = %d, want zero", score)
	}

	changedGeometry := node
	changedGeometry.Bounds.X += target.Bounds.Width + 1
	if score := windowCandidateScore(changedGeometry, target); score != 0 {
		t.Fatalf("non-overlapping candidate score = %d, want zero", score)
	}
}

func TestWindowCandidateScoreAcceptsAccessibleShortTitle(t *testing.T) {
	target := WindowTarget{Title: "Browser Console - Mozilla Firefox", Bounds: Rect{X: 10, Y: 20, Width: 300, Height: 200}}
	node := Node{Name: "Browser Console", Role: "frame", Bounds: target.Bounds, HasBounds: true, Showing: true}
	if score := windowCandidateScore(node, target); score <= 0 {
		t.Fatalf("short accessible title score = %d, want positive", score)
	}
}

func TestReadWindowCandidateUsesCurrentCacheMetadata(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 4}
	child := NodeID{BusName: root.BusName, ObjectPath: "/console", Generation: root.Generation}
	backend := &dbusBackend{
		generation: root.Generation,
		cacheItems: make(map[NodeID]cacheItem),
		cacheApps:  map[string]bool{root.BusName: true},
	}
	backend.cacheItems[child] = cacheItem{
		Object:      cacheObjectRef{BusName: child.BusName, ObjectPath: dbus.ObjectPath(child.ObjectPath)},
		Parent:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)},
		Name:        "Parent process Browser Console",
		Description: "Firefox",
		Role:        23,
		States:      []uint32{(1 << 8) | (1 << 11) | (1 << 25) | (1 << 30)},
	}

	node, err := backend.readWindowCandidate(context.Background(), child, root)
	if err != nil {
		t.Fatalf("readWindowCandidate: %v", err)
	}
	if node.Name != "Parent process Browser Console" || node.Role != "frame" {
		t.Fatalf("cached candidate = %+v", node)
	}
	if !node.Enabled || !node.Showing || !node.Visible {
		t.Fatalf("cached candidate states = %+v", node.States)
	}
}

func TestRoleNameIncludesATSPIWindowRole(t *testing.T) {
	if got := roleName(69); got != "window" {
		t.Fatalf("AT-SPI role 69 = %q, want window", got)
	}
	if !isWindowRole(roleName(69)) {
		t.Fatalf("AT-SPI role 69 was not accepted as a top-level window")
	}
}

func TestFirefoxEmbeddedConsoleRoleIsAcceptedAsWindowScope(t *testing.T) {
	if got := roleName(78); got != "embedded" {
		t.Fatalf("AT-SPI role 78 = %q, want embedded", got)
	}
	if !isWindowRole("embedded") {
		t.Fatal("Firefox embedded Browser Console role was not accepted as a top-level window")
	}
}

func TestChooseWindowCandidateSelectsUniqueStrongestEvidence(t *testing.T) {
	weak := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/weak", Generation: 1}, Role: "frame", Name: "Editor"}
	strong := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/strong", Generation: 1}, Role: "frame", Name: "Editor"}
	selected, candidates, ambiguous := chooseWindowCandidate([]windowCandidate{
		{node: weak, score: 4},
		{node: strong, score: 12},
	})
	if ambiguous || selected == nil || selected.ID != strong.ID {
		t.Fatalf("selected=%+v candidates=%+v ambiguous=%v", selected, candidates, ambiguous)
	}
	if len(candidates) != 1 || candidates[0].ID != strong.ID {
		t.Fatalf("best candidates = %+v, want strongest candidate only", candidates)
	}
}

func TestWindowTitleMatchRejectsPrefixSubstring(t *testing.T) {
	// "Edit" is a prefix of "Editor - Preferences" but not a word-boundary
	// match; correlation must fail closed.
	if _, ok := windowTitleMatchScore("editor - preferences", "edit", ""); ok {
		t.Fatal("title substring prefix was accepted without word-boundary check")
	}
}

func TestWindowTitleMatchAcceptsWordBoundaryPrefix(t *testing.T) {
	// "Editor" occurs at a word boundary in "Editor - Preferences".
	score, ok := windowTitleMatchScore("editor", "editor - preferences", "")
	if !ok || score <= 0 {
		t.Fatalf("word-boundary prefix match: score=%d ok=%v", score, ok)
	}
}

func TestWindowTitleMatchAcceptsSeparatorDelimitedMatch(t *testing.T) {
	score, ok := windowTitleMatchScore("console", "browser console - mozilla firefox", "")
	if !ok || score <= 0 {
		t.Fatalf("separator-delimited match: score=%d ok=%v", score, ok)
	}
}

func TestWindowTitleMatchRejectsUnicodeFalseBoundary(t *testing.T) {
	// Unicode letters should be treated as word characters, so "edit" must
	// not match "Editorédité" (where "édité" continues the word).
	if _, ok := windowTitleMatchScore("editorédité", "edit", ""); ok {
		t.Fatal("unicode-adjacent substring was accepted as word boundary")
	}
	if _, ok := windowTitleMatchScore("editor", "éeditor - preferences", ""); ok {
		t.Fatal("substring following a Unicode letter was accepted as word boundary")
	}
}

func TestWindowTitleMatchAcceptsUnicodeExactMatch(t *testing.T) {
	score, ok := windowTitleMatchScore("éditeur", "éditeur", "")
	if !ok || score != 100 {
		t.Fatalf("unicode exact match: score=%d ok=%v", score, ok)
	}
}

func TestWindowTitleMatchUsesRuneCountsForMinimumLengths(t *testing.T) {
	tests := []struct {
		name        string
		windowName  string
		title       string
		description string
	}{
		{
			name:       "single-character title",
			windowName: "a - settings",
			title:      "a",
		},
		{
			name:       "short Unicode title",
			windowName: "éé - settings",
			title:      "éé",
		},
		{
			name:       "short Unicode window name",
			windowName: "éééé",
			title:      "éééé - browser",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := windowTitleMatchScore(tt.windowName, tt.title, tt.description); ok {
				t.Fatalf("short Unicode evidence matched: window=%q title=%q", tt.windowName, tt.title)
			}
		})
	}
}

func TestWindowTitleMatchDescriptionRequiresWordBoundary(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		description string
		wantScore   int
		wantOK      bool
	}{
		{
			name:        "rejects title prefix inside a word",
			title:       "edit",
			description: "editor preferences",
		},
		{
			name:        "accepts separator-delimited title",
			title:       "editor",
			description: "open editor settings",
			wantScore:   25,
			wantOK:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, ok := windowTitleMatchScore("preferences", tt.title, tt.description)
			if score != tt.wantScore || ok != tt.wantOK {
				t.Fatalf("description match: score=%d ok=%v, want score=%d ok=%v", score, ok, tt.wantScore, tt.wantOK)
			}
		})
	}
}

func TestWindowTitleMatchSameTitleSiblingsAmbiguous(t *testing.T) {
	candidates := []windowCandidate{
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/a", Generation: 1}, Role: "frame", Name: "Editor"}, score: 100},
		{node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/b", Generation: 1}, Role: "frame", Name: "Editor"}, score: 100},
	}
	selected, _, ambiguous := chooseWindowCandidate(candidates)
	if !ambiguous || selected != nil {
		t.Fatalf("same-title siblings: selected=%+v ambiguous=%v", selected, ambiguous)
	}
}

// A mismatched application must not be queried for window children: besides
// wasting a provider round-trip it can expose another application's private
// accessibility tree to the window correlation request.
func TestFreshWindowCorrelationSkipsUnrelatedApplicationWithoutBusAccess(t *testing.T) {
	const bus = "org.example.Unrelated"
	b := &dbusBackend{generation: 9}
	app := Application{Node: Node{ID: NodeID{BusName: bus, ObjectPath: "/app", Generation: 9, Incarnation: 1}}, PID: 6001}
	target := WindowTarget{ID: "selected-window", PID: 6002}
	candidates, observed, err := b.windowCandidatesFresh(context.Background(), target, []Application{app})
	if err != nil || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("unrelated provider was observed: candidates=%v observed=%v err=%v", candidates, observed, err)
	}
	// The non-fresh path must enforce the same PID ownership filter before
	// attempting to use any cached or live provider child identities.
	if got := b.windowCandidates(context.Background(), target, []Application{app}); len(got) != 0 {
		t.Fatalf("unrelated cached app was inspected: %+v", got)
	}
}

// A stale app may not establish scope even if its PID matches: generation
// checks precede every provider or cache lookup in fresh correlation.
func TestFreshWindowCorrelationRejectsRetiredApplicationBeforeProbing(t *testing.T) {
	b := &dbusBackend{generation: 9}
	app := Application{Node: Node{ID: NodeID{BusName: "org.example.Editor", ObjectPath: "/app", Generation: 8, Incarnation: 1}}, PID: 6002}
	target := WindowTarget{ID: "selected-window", PID: 6002}
	candidates, observed, err := b.windowCandidatesFresh(context.Background(), target, []Application{app})
	if !errors.Is(err, ErrStaleGeneration) || len(candidates) != 0 || len(observed) != 0 {
		t.Fatalf("retired app authorized fresh lookup: candidates=%v observed=%v err=%v", candidates, observed, err)
	}
}

// An active-only window target cannot be matched to an AT-SPI frame merely
// because the application matches and only one top-level frame is available.
// The candidate must actually report focused/active state.
func TestPrivateATSPIActiveOnlyCorrelationCannotGuessInactiveFrame(t *testing.T) {
	backend, _, target := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) {
		frame.states = nil
	})
	target.PID = 0
	target.PIDHint = 0
	target.Title = ""
	target.Bounds = Rect{}
	target.AppID = "Editor"
	target.Active = true
	target.Focused = false
	for _, fresh := range []bool{false, true} {
		label := "cached"
		if fresh {
			label = "fresh"
		}
		t.Run(label, func(t *testing.T) {
			var scope WindowScope
			var err error
			if fresh {
				scope, err = backend.ResolveWindowFresh(context.Background(), target)
			} else {
				scope, err = backend.ResolveWindow(context.Background(), target)
			}
			if !errors.Is(err, ErrNotFound) || scope.Root.BusName != "" {
				t.Fatalf("active-only correlation guessed unobserved focus: scope=%+v err=%v", scope, err)
			}
		})
	}
}

// Geometry alone is authoritative correlation evidence only if the AT-SPI
// window's own bounds have actually been verified. A matched application name
// and a single frame cannot substitute for absent component geometry.
func TestPrivateATSPIGeometryOnlyCorrelationCannotGuessUnverifiedFrame(t *testing.T) {
	backend, _, target := newSyntheticATSPIBackend(t, false)
	target.PID = 0
	target.PIDHint = 0
	target.Title = ""
	target.Active = false
	target.Focused = false
	target.AppID = "Editor"
	target.Bounds = Rect{X: 20000, Y: 20000, Width: 400, Height: 300}
	for _, fresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "cached", true: "fresh"}[fresh], func(t *testing.T) {
			var scope WindowScope
			var err error
			if fresh {
				scope, err = backend.ResolveWindowFresh(context.Background(), target)
			} else {
				scope, err = backend.ResolveWindow(context.Background(), target)
			}
			if !errors.Is(err, ErrNotFound) || scope.Root.BusName != "" {
				t.Fatalf("geometry-only correlation guessed unrelated frame: scope=%+v error=%v", scope, err)
			}
		})
	}
}

// Positive evidence still correlates: the fail-closed checks apply to missing
// evidence, not legitimately observed geometry or focus state.
func TestWindowEvidenceChecksPreserveVerifiedGeometryAndFocus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		node   Node
		target WindowTarget
	}{
		{name: "verified overlap", node: Node{Role: "frame", HasBounds: true, Bounds: Rect{X: 50, Y: 30, Width: 200, Height: 150}}, target: WindowTarget{ID: "private", AppID: "Editor", Bounds: Rect{X: 100, Y: 70, Width: 100, Height: 100}}},
		{name: "verified focus", node: Node{Role: "frame", Focused: true}, target: WindowTarget{ID: "private", AppID: "Editor", Active: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if score := windowCandidateScore(tc.node, tc.target); score <= 1 {
				t.Fatalf("verified window evidence failed to contribute to correlation: %d", score)
			}
		})
	}
}

// A managed-window identifier is not an AT-SPI identity. Both cached and
// freshly observed correlation must reject requests without independent
// evidence before making any provider calls or claiming a scope root.
func TestWindowCorrelationRefusesAbsentAndUncorroboratedIdentityWithoutProvider(t *testing.T) {
	backend := &dbusBackend{generation: 13}
	for _, tc := range []struct {
		name   string
		target WindowTarget
		want   error
	}{
		{"missing identity", WindowTarget{Title: "Editor", PID: 42}, ErrNotFound},
		{"whitespace identity", WindowTarget{ID: " \t", Title: "Editor", PID: 42}, ErrNotFound},
		{"ID alone", WindowTarget{ID: "managed-window"}, ErrUnsupportedCorrelation},
		{"ID and app name only", WindowTarget{ID: "managed-window", AppID: "org.example.Editor"}, ErrUnsupportedCorrelation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, fresh := range []bool{false, true} {
				var scope WindowScope
				var err error
				if fresh {
					scope, err = backend.ResolveWindowFresh(context.Background(), tc.target)
				} else {
					scope, err = backend.ResolveWindow(context.Background(), tc.target)
				}
				if !errors.Is(err, tc.want) || scope.Root.BusName != "" || scope.Root.ObjectPath != "" {
					t.Fatalf("fresh=%v target=%+v scope=%+v err=%v want=%v", fresh, tc.target, scope, err, tc.want)
				}
			}
		})
	}
}
