package accessibility

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/capability"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestSnapshotOptionsNormalizeBounds(t *testing.T) {
	got := (SnapshotOptions{MaxDepth: 1000, MaxNodes: 1 << 30, MaxTextBytes: 1 << 30}).normalized()
	if got.MaxDepth != absMaxDepth || got.MaxNodes != absMaxNodes || got.MaxTextBytes != absMaxText {
		t.Fatalf("normalized limits = %+v, want hard limits", got)
	}
	defaults := (SnapshotOptions{}).normalized()
	if defaults.MaxDepth != defaultMaxDepth || defaults.MaxNodes != defaultMaxNodes || defaults.MaxTextBytes != defaultMaxText {
		t.Fatalf("defaults = %+v", defaults)
	}
	roles := (SnapshotOptions{SkipRoles: []string{" landmark ", "LANDMARK", ""}}).normalized().SkipRoles
	if len(roles) != 1 || roles[0] != "landmark" {
		t.Fatalf("normalized skip roles = %#v, want [landmark]", roles)
	}
}

func TestApplyStatesDecodesATSPIStateBitmask(t *testing.T) {
	backend := &dbusBackend{}
	node := Node{}
	backend.applyStates(
		[]uint32{
			(uint32(1) << 8) | (uint32(1) << 12) | (uint32(1) << 30),
			uint32(1) << 2,
		},
		&node,
	)

	for _, want := range []string{"enabled", "focused", "visible", "protected"} {
		if !contains(node.States, want) {
			t.Fatalf("decoded states = %v, missing %q", node.States, want)
		}
	}
	if !node.Enabled || !node.Focused || !node.Visible {
		t.Fatalf("decoded state flags = enabled=%t focused=%t visible=%t", node.Enabled, node.Focused, node.Visible)
	}
}

func TestRoleNameDecodesFirefoxSemanticRoles(t *testing.T) {
	for role, want := range map[uint32]string{
		69:  "window",
		73:  "paragraph",
		78:  "embedded",
		79:  "entry",
		82:  "document-frame",
		83:  "heading",
		85:  "section",
		88:  "link",
		98:  "list-box",
		110: "landmark",
		129: "push-button",
	} {
		if got := roleName(role); got != want {
			t.Errorf("roleName(%d) = %q, want %q", role, got, want)
		}
	}
	if got := roleName(999); got != "role-999" {
		t.Fatalf("unknown role name = %q, want role-999", got)
	}
}

func TestNodeIDValidation(t *testing.T) {
	if (NodeID{}).valid() {
		t.Fatal("zero NodeID is valid")
	}
	if (NodeID{BusName: "org.example", ObjectPath: string(nullObjectPath)}).valid() {
		t.Fatal("null AT-SPI object is valid")
	}
	if !(NodeID{BusName: "org.example", ObjectPath: "/org/example/node", Generation: 1}).valid() {
		t.Fatal("ordinary AT-SPI object is invalid")
	}
}

func TestSnapshotPublicationRejectsInvalidatedRead(t *testing.T) {
	backend := &dbusBackend{generation: 4}
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 4}
	snapshot := Snapshot{
		Root:       Node{ID: root},
		Nodes:      []Node{{ID: root}},
		Generation: 4,
		CapturedAt: time.Now(),
	}
	backend.Invalidate(root)
	if err := backend.publishSnapshot(snapshotKey(root, SnapshotOptions{}), root.Generation, 0, root, true, snapshot); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale snapshot publication error = %v, want ErrStaleGeneration", err)
	}
	if len(backend.cache) != 0 {
		t.Fatalf("stale snapshot was cached: %+v", backend.cache)
	}
}

func TestSnapshotKeySeparatesGenerations(t *testing.T) {
	first := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 1}
	second := first
	second.Generation++
	if snapshotKey(first, SnapshotOptions{}) == snapshotKey(second, SnapshotOptions{}) {
		t.Fatal("snapshot cache key reused across generations")
	}
}

func TestAccessibilityBusAddressMethod(t *testing.T) {
	if busAddressMethod != "org.a11y.Bus.GetAddress" {
		t.Fatalf("accessibility bus address method = %q", busAddressMethod)
	}
}

func TestCacheItemWireSignatureMatchesATSPICache(t *testing.T) {
	if got := dbus.SignatureOf([]cacheItem{}).String(); got != "a((so)(so)(so)iiassusau)" {
		t.Fatalf("cache GetItems signature = %q", got)
	}
}

func TestDocumentTextSelectionWireSignature(t *testing.T) {
	if got := dbus.SignatureOf([]documentTextSelectionWire{}).String(); got != "a((so)i(so)ib)" {
		t.Fatalf("document selection signature = %q, want a((so)i(so)ib)", got)
	}
}

func TestCoordTypeWireValues(t *testing.T) {
	if CoordTypeScreen != 0 || CoordTypeWindow != 1 || CoordTypeParent != 2 {
		t.Fatalf("coordinate wire values = %d,%d,%d; want 0,1,2", CoordTypeScreen, CoordTypeWindow, CoordTypeParent)
	}
}

func TestSnapshotRootSelectionDoesNotWidenMalformedScope(t *testing.T) {
	if (NodeID{ObjectPath: "/only-path"}).valid() {
		t.Fatal("partial root considered valid")
	}
	if (NodeID{BusName: "only-bus"}).valid() {
		t.Fatal("partial root considered valid")
	}
}

func TestEventOptionsNormalizeAndSignalConversion(t *testing.T) {
	if got := (EventOptions{}).normalized().Buffer; got != defaultEventBuffer {
		t.Fatalf("default event buffer = %d", got)
	}
	if got := (EventOptions{Buffer: 99999}).normalized().Buffer; got != 4096 {
		t.Fatalf("capped event buffer = %d", got)
	}
	e := signalEvent(nil)
	if e.Timestamp.IsZero() {
		t.Fatal("nil signal did not get timestamp")
	}
	if !hasSensitiveState([]string{"visible", "protected"}) || hasSensitiveState([]string{"visible"}) {
		t.Fatal("sensitive state detection incorrect")
	}
	e = signalEvent(&dbus.Signal{Name: "org.a11y.atspi.Event.Object:PropertyChange", Sender: "org.test.App", Path: dbus.ObjectPath("/node"), Body: []any{"Name", "Save"}})
	if e.Node.BusName != "org.test.App" || e.Node.ObjectPath != "/node" || e.Property != "Name" || e.Value != "Save" {
		t.Fatalf("signal conversion = %+v", e)
	}
	e = signalEvent(&dbus.Signal{Name: "org.a11y.atspi.Event.Object:PropertyChange", Sender: "org.test.App", Path: dbus.ObjectPath("/password"), Body: []any{"Text", "secret"}})
	if e.Value != "" {
		t.Fatalf("sensitive event value leaked: %+v", e)
	}
}

func TestEventCoalescingTracksDuplicateInvalidations(t *testing.T) {
	base := time.Now()
	first := Event{Kind: "focus", Node: NodeID{BusName: "app", ObjectPath: "/node", Generation: 1}, Timestamp: base}
	lastKey := ""
	var lastAt time.Time
	if coalesceEvent(&lastKey, &lastAt, first) {
		t.Fatal("first event was coalesced")
	}
	if !coalesceEvent(&lastKey, &lastAt, Event{Kind: first.Kind, Node: first.Node, Timestamp: base.Add(time.Millisecond)}) {
		t.Fatal("duplicate event was not coalesced")
	}
	if coalesceEvent(&lastKey, &lastAt, Event{Kind: first.Kind, Node: first.Node, Timestamp: base.Add(eventCoalesceWindow + time.Millisecond)}) {
		t.Fatal("event outside coalescing window was coalesced")
	}
}

type recordingEventRegistrar struct {
	methods []string
	events  []string
}

type selectiveEventRegistrar struct {
	fail map[string]bool
}

func (r selectiveEventRegistrar) CallWithContext(_ context.Context, _ string, _ dbus.Flags, args ...any) *dbus.Call {
	eventType, _ := args[0].(string)
	if r.fail[eventType] {
		return &dbus.Call{Err: fmt.Errorf("unsupported event family %s", eventType)}
	}
	return &dbus.Call{}
}

func TestRegisterEventFamiliesKeepsPartialSuccessTolerant(t *testing.T) {
	registered, err := registerEventFamilies(context.Background(), selectiveEventRegistrar{fail: map[string]bool{"unsupported": true}}, "", []string{"working", "unsupported", "also-working"})
	if err != nil {
		t.Fatalf("partial event registration error = %v", err)
	}
	if !reflect.DeepEqual(registered, []string{"working", "also-working"}) {
		t.Fatalf("registered families = %v, want only successful families", registered)
	}
}

func TestRegisterEventFamiliesFailsWhenNoFamilyWorks(t *testing.T) {
	_, err := registerEventFamilies(context.Background(), selectiveEventRegistrar{fail: map[string]bool{"unsupported": true}}, "", []string{"unsupported"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("all-failed event registration error = %v, want ErrUnsupported", err)
	}
}

func TestSemanticEventCoverageRejectsMissingCriticalFamily(t *testing.T) {
	registered := append([]string(nil), semanticObservationEventFamilies[:]...)
	registered = append(registered, "window:activate", "window:deactivate")
	if err := validateSemanticEventCoverage(registered); err != nil {
		t.Fatalf("complete semantic event coverage: %v", err)
	}
	for _, missing := range semanticObservationEventFamilies {
		partial := make([]string, 0, len(registered)-1)
		for _, family := range registered {
			if family != missing {
				partial = append(partial, family)
			}
		}
		if err := validateSemanticEventCoverage(partial); !errors.Is(err, ErrUnsupported) {
			t.Errorf("semantic coverage without %s = %v, want ErrUnsupported", missing, err)
		}
	}
}

func TestSemanticSubscriberRejectsRunningPartialEventStream(t *testing.T) {
	generalStream := make(chan Event, 1)
	backend := &dbusBackend{
		access:          &dbus.Conn{},
		generation:      1,
		subscribers:     map[uint64]*eventSubscriber{1: {out: generalStream}},
		nextSubscriber:  1,
		eventCancel:     func() {},
		eventDone:       make(chan struct{}),
		eventRegistered: []string{"object:property-change"},
	}

	stream, err := backend.Events(context.Background(), EventOptions{RequireSemanticCoverage: true})
	if stream != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("semantic subscription on partial dispatcher = %v, %v; want nil stream and ErrUnsupported", stream, err)
	}
	if len(backend.subscribers) != 1 || backend.subscribers[1].out != generalStream {
		t.Fatalf("rejected semantic subscription changed existing subscribers: %+v", backend.subscribers)
	}
	select {
	case _, open := <-generalStream:
		if !open {
			t.Fatal("rejecting semantic subscription closed the existing general stream")
		}
	default:
	}
}

type testEventRegistry struct {
	mu           sync.Mutex
	failEvent    string
	registered   []string
	deregistered []string
}

func (r *testEventRegistry) RegisterEvent(eventType string, _ []string, _ string) *dbus.Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registered = append(r.registered, eventType)
	if eventType == r.failEvent {
		return dbus.NewError("org.freedesktop.DBus.Error.NotSupported", []any{"unsupported test event"})
	}
	return nil
}

func (r *testEventRegistry) DeregisterEvent(eventType string) *dbus.Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deregistered = append(r.deregistered, eventType)
	return nil
}

func (r *testEventRegistry) registrations() (registered, deregistered []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.registered...), append([]string(nil), r.deregistered...)
}

func TestSemanticCoverageSetupDeregistersPartialRegistrations(t *testing.T) {
	const rejectedFamily = "window:create"

	address := startTestDBus(t)
	server, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatalf("connect test registry bus: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	registry := &testEventRegistry{failEvent: rejectedFamily}
	if exportErr := server.Export(registry, registryPath, registryName); exportErr != nil {
		t.Fatalf("export test AT-SPI registry: %v", exportErr)
	}
	reply, err := server.RequestName(registryName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own test AT-SPI registry name: reply %d, err %v", reply, err)
	}
	access, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatalf("connect event client: %v", err)
	}
	backend := &dbusBackend{access: access, generation: 1, subscribers: make(map[uint64]*eventSubscriber)}
	t.Cleanup(func() { _ = backend.Close() })

	stream, err := backend.Events(context.Background(), EventOptions{RequireSemanticCoverage: true})
	if stream != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("semantic subscription with rejected family = %v, %v; want nil stream and ErrUnsupported", stream, err)
	}
	if len(backend.subscribers) != 0 || backend.eventCancel != nil || backend.eventRegistered != nil {
		t.Fatalf("failed semantic setup retained dispatcher state: subscribers=%d cancel=%t registered=%v", len(backend.subscribers), backend.eventCancel != nil, backend.eventRegistered)
	}

	registered, deregistered := registry.registrations()
	wantRegistered := []string{
		"object:property-change", "object:state-changed", "object:children-changed", "object:text-changed",
		"object:visible-data-changed", "focus:focus", "window:activate", "window:deactivate", "window:create", "window:destroy",
	}
	wantDeregistered := make([]string, 0, len(wantRegistered)-1)
	for _, family := range wantRegistered {
		if family != rejectedFamily {
			wantDeregistered = append(wantDeregistered, family)
		}
	}
	if !reflect.DeepEqual(registered, wantRegistered) {
		t.Fatalf("registered families = %v, want %v", registered, wantRegistered)
	}
	if !reflect.DeepEqual(deregistered, wantDeregistered) {
		t.Fatalf("cleanup deregistered families = %v, want %v", deregistered, wantDeregistered)
	}
}

func (r *recordingEventRegistrar) CallWithContext(_ context.Context, method string, _ dbus.Flags, args ...any) *dbus.Call {
	r.methods = append(r.methods, method)
	if len(args) > 0 {
		if eventType, ok := args[0].(string); ok {
			r.events = append(r.events, eventType)
		}
	}
	return &dbus.Call{}
}

func TestDeregisterEventsCleansEveryRegistration(t *testing.T) {
	registrar := &recordingEventRegistrar{}
	registered := []string{"object:property-change", "window:activate"}
	deregisterEvents(context.Background(), registrar, registered)
	if len(registrar.methods) != len(registered) || len(registrar.events) != len(registered) {
		t.Fatalf("deregister calls = methods %v events %v, want %d", registrar.methods, registrar.events, len(registered))
	}
	for i, method := range registrar.methods {
		if method != registryName+".DeregisterEvent" || registrar.events[i] != registered[i] {
			t.Fatalf("deregister call %d = %s %q, want %s %q", i, method, registrar.events[i], registryName+".DeregisterEvent", registered[i])
		}
	}
}

func TestEventSetupContextPreservesCallerPolicyDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	setupCtx, setupCancel := eventSetupContext(ctx)
	defer setupCancel()
	got, ok := setupCtx.Deadline()
	if !ok || !got.Equal(deadline) {
		t.Fatalf("event setup deadline = %v, present=%v, want caller policy deadline %v", got, ok, deadline)
	}
}

func TestEventStartCallerCancellationDoesNotPoisonHealthyWaiter(t *testing.T) {
	state := &eventStart{done: make(chan struct{})}
	result := make(chan struct {
		retry bool
		err   error
	}, 1)
	go func() {
		retry, err := waitForEventStart(context.Background(), state, nil, 0)
		result <- struct {
			retry bool
			err   error
		}{retry: retry, err: err}
	}()
	state.err = context.Canceled
	state.callerCanceled = true
	close(state.done)
	select {
	case got := <-result:
		if !got.retry || got.err != nil {
			t.Fatalf("healthy waiter result = retry %v, err %v; want one retry", got.retry, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("healthy waiter did not observe canceled setup")
	}

	second := &eventStart{done: make(chan struct{})}
	close(second.done)
	if retry, err := waitForEventStart(context.Background(), second, nil, 1); retry || err != nil {
		t.Fatalf("bounded retry completion = retry %v, err %v; want success", retry, err)
	}
}

func TestCloneSnapshotDeepCopiesAndPreservesMetadata(t *testing.T) {
	at := time.Now()
	in := Snapshot{Root: Node{ID: NodeID{BusName: "b", ObjectPath: "/r", Generation: 1}}, Nodes: []Node{{ID: NodeID{BusName: "b", ObjectPath: "/r", Generation: 1}, Attributes: map[string]string{"value": "x"}, Children: []NodeID{{BusName: "b", ObjectPath: "/c", Generation: 1}}, Warnings: []string{"optional Text unavailable"}}}, Generation: 4, CapturedAt: at, Source: "at-spi"}
	out := cloneSnapshot(in)
	out.Nodes[0].Attributes["value"] = "changed"
	out.Nodes[0].Children[0].ObjectPath = "/changed"
	out.Nodes[0].Warnings[0] = "changed"
	if in.Nodes[0].Attributes["value"] != "x" || in.Nodes[0].Children[0].ObjectPath != "/c" || in.Nodes[0].Warnings[0] != "optional Text unavailable" {
		t.Fatal("clone shares mutable node state")
	}
	if out.Generation != 4 || out.Source != "at-spi" || !out.CapturedAt.Equal(at) {
		t.Fatalf("metadata not preserved: %+v", out)
	}
}

func TestSnapshotKeySeparatesSecurityAndLimits(t *testing.T) {
	root := NodeID{BusName: "b", ObjectPath: "/r", Generation: 1}
	if snapshotKey(root, SnapshotOptions{MaxDepth: 1}) == snapshotKey(root, SnapshotOptions{MaxDepth: 2}) {
		t.Fatal("depth not part of snapshot key")
	}
	if snapshotKey(root, SnapshotOptions{AllowSensitive: true}) == snapshotKey(root, SnapshotOptions{}) {
		t.Fatal("redaction policy not part of snapshot key")
	}
	if snapshotKey(root, SnapshotOptions{VisibleOnly: true}) == snapshotKey(root, SnapshotOptions{}) {
		t.Fatal("visibility policy not part of snapshot key")
	}
	if snapshotKey(root, SnapshotOptions{SkipRoles: []string{"landmark"}}) == snapshotKey(root, SnapshotOptions{}) {
		t.Fatal("role pruning policy not part of snapshot key")
	}
}

func TestTruncateUTF8HonorsByteLimit(t *testing.T) {
	value, truncated := truncateUTF8("a€😀", 4)
	if value != "a€" || !truncated || len(value) != 4 {
		t.Fatalf("truncateUTF8 = %q, truncated=%v, bytes=%d", value, truncated, len(value))
	}
	if !utf8.ValidString(value) {
		t.Fatal("truncateUTF8 returned invalid UTF-8")
	}
}

func TestBoundSnapshotResponsePrunesDanglingChildren(t *testing.T) {
	rootID := NodeID{BusName: "b", ObjectPath: "/root", Generation: 1}
	childID := NodeID{BusName: "b", ObjectPath: "/child", Generation: 1}
	root := Node{ID: rootID, Children: []NodeID{childID}}
	child := Node{ID: childID, Parent: rootID}
	snapshot := Snapshot{Root: root, Nodes: []Node{root, child}, Generation: 1, CapturedAt: time.Now(), Source: "test"}
	limit := snapshotJSONSize(Snapshot{Root: root, Nodes: []Node{root}, Generation: snapshot.Generation, CapturedAt: snapshot.CapturedAt, Source: snapshot.Source})
	bounded, err := boundSnapshotResponse(snapshot, limit)
	if err != nil {
		t.Fatalf("boundSnapshotResponse: %v", err)
	}
	if len(bounded.Nodes) != 1 || bounded.Root.ID != rootID || len(bounded.Nodes[0].Children) != 0 {
		t.Fatalf("bounded snapshot = %+v, want a self-consistent root-only prefix", bounded)
	}
	for _, childRef := range bounded.Nodes[0].Children {
		for _, node := range bounded.Nodes {
			if childRef == node.ID {
				t.Fatalf("child reference %v unexpectedly remained after truncation", childRef)
			}
		}
	}
}

func TestBoundSnapshotResponseRejectsIrreduciblyTinyBudget(t *testing.T) {
	rootID := NodeID{BusName: "b", ObjectPath: "/root", Generation: 1}
	_, err := boundSnapshotResponse(Snapshot{Root: Node{ID: rootID}, Nodes: []Node{{ID: rootID}}, Generation: 1, CapturedAt: time.Now(), Source: "test"}, 1)
	if !errors.Is(err, ErrResponseBudget) {
		t.Fatalf("tiny budget error = %v, want ErrResponseBudget", err)
	}
}

func TestCacheItemsProvideDeterministicChildrenAndSignals(t *testing.T) {
	root := NodeID{BusName: "org.test.App", ObjectPath: "/root", Generation: 1}
	application := cacheObjectRef{BusName: root.BusName, ObjectPath: "/application"}
	rootItem := cacheItem{Object: cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)}, Application: application, Parent: application, ChildCount: 2}
	first := cacheItem{Object: cacheObjectRef{BusName: root.BusName, ObjectPath: "/first"}, Application: application, Parent: cacheObjectRef{BusName: root.BusName, ObjectPath: "/root"}, Index: 1}
	second := cacheItem{Object: cacheObjectRef{BusName: root.BusName, ObjectPath: "/second"}, Application: application, Parent: cacheObjectRef{BusName: root.BusName, ObjectPath: "/root"}, Index: 0}
	backend := &dbusBackend{
		generation: 1,
		cacheItems: map[NodeID]cacheItem{rootItem.nodeIDAt(1): rootItem, first.nodeIDAt(1): first, second.nodeIDAt(1): second},
		cacheApps:  map[string]bool{root.BusName: true},
	}
	children := backend.cachedChildren(root)
	if len(children) != 2 || children[0].ObjectPath != "/second" || children[1].ObjectPath != "/first" {
		t.Fatalf("children = %+v, want index order", children)
	}
	backend.prepareEvent(&dbus.Signal{Name: cacheIface + ":RemoveAccessible", Sender: root.BusName, Body: []any{cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/second")}}}, Event{Node: NodeID{BusName: root.BusName, ObjectPath: "/second"}})
	if got := len(backend.cachedChildren(root)); got != 1 {
		t.Fatalf("children after remove = %d, want 1", got)
	}
	if got := backend.cacheItems[rootItem.nodeIDAt(1)].ChildCount; got != 1 {
		t.Fatalf("cached child count after remove = %d, want 1", got)
	}
	added := cacheItem{Object: cacheObjectRef{BusName: root.BusName, ObjectPath: "/third"}, Application: application, Parent: cacheObjectRef{BusName: root.BusName, ObjectPath: "/root"}, Index: 0}
	backend.prepareEvent(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: root.BusName, Body: []any{added}}, Event{Node: NodeID{BusName: root.BusName, ObjectPath: "/third"}})
	if got := len(backend.cachedChildren(root)); got != 2 {
		t.Fatalf("children after add = %d, want 2", got)
	}
	if got := backend.cacheItems[rootItem.nodeIDAt(1)].ChildCount; got != 2 {
		t.Fatalf("cached child count after add = %d, want 2", got)
	}
}

func TestCompleteCacheReplacementRefreshesChildren(t *testing.T) {
	const busName = "org.test.App"
	root := NodeID{BusName: busName, ObjectPath: "/root", Generation: 1}
	application := cacheObjectRef{BusName: busName, ObjectPath: "/application"}
	rootItem := cacheItem{Object: cacheObjectRef{BusName: busName, ObjectPath: "/root"}, Application: application, Parent: application, ChildCount: 1}
	oldChild := cacheItem{Object: cacheObjectRef{BusName: busName, ObjectPath: "/old"}, Application: application, Parent: cacheObjectRef{BusName: busName, ObjectPath: "/root"}}
	backend := &dbusBackend{
		generation: 1,
		cacheItems: map[NodeID]cacheItem{rootItem.nodeIDAt(1): rootItem, oldChild.nodeIDAt(1): oldChild},
		cacheApps:  map[string]bool{busName: true},
	}
	if got := backend.cachedChildren(root); len(got) != 1 || got[0].ObjectPath != "/old" {
		t.Fatalf("initial children = %+v, want /old", got)
	}

	newChild := cacheItem{Object: cacheObjectRef{BusName: busName, ObjectPath: "/new"}, Application: application, Parent: cacheObjectRef{BusName: busName, ObjectPath: "/root"}}
	backend.mu.Lock()
	backend.replaceCompleteCacheLocked(busName, []cacheItem{rootItem, newChild})
	backend.mu.Unlock()
	if got := backend.cachedChildren(root); len(got) != 1 || got[0].ObjectPath != "/new" {
		t.Fatalf("children after complete cache replacement = %+v, want /new", got)
	}
}

func TestCacheAddReparentAdjustsBothParentChildCounts(t *testing.T) {
	const generation = 5
	app := cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/app")}
	oldParent := cacheItem{
		Object:      cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/old_parent")},
		Application: app, Parent: app, ChildCount: 1,
	}
	newParent := cacheItem{
		Object:      cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/new_parent")},
		Application: app, Parent: app, ChildCount: 0,
	}
	child := cacheItem{
		Object:      cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/child")},
		Application: app, Parent: cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/old_parent")},
	}
	backend := &dbusBackend{
		generation: generation,
		cacheItems: map[NodeID]cacheItem{
			oldParent.nodeIDAt(generation): oldParent,
			newParent.nodeIDAt(generation): newParent,
			child.nodeIDAt(generation):     child,
		},
		cacheApps: map[string]bool{"org.test": true},
	}
	updated := child
	updated.Parent = newParent.Object
	backend.prepareEvent(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: "org.test", Body: []any{updated}}, Event{Kind: cacheIface + ":AddAccessible"})
	if got := backend.cacheItems[oldParent.nodeIDAt(generation)].ChildCount; got != 0 {
		t.Fatalf("old parent child count = %d, want 0", got)
	}
	if got := backend.cacheItems[newParent.nodeIDAt(generation)].ChildCount; got != 1 {
		t.Fatalf("new parent child count = %d, want 1", got)
	}
	if got := backend.parents[objectIdentity{busName: "org.test", objectPath: "/child"}]; got != (objectIdentity{busName: "org.test", objectPath: "/new_parent"}) {
		t.Fatalf("recorded child parent = %+v, want /new_parent", got)
	}
}

func TestCacheChildCountUncertaintyInvalidatesApplicationCache(t *testing.T) {
	tests := []struct {
		name  string
		item  *cacheItem
		delta int64
	}{
		{name: "missing parent"},
		{name: "underflow", item: &cacheItem{ChildCount: 0}, delta: -1},
		{name: "overflow", item: &cacheItem{ChildCount: 1<<31 - 1}, delta: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ref := cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/parent")}
			backend := &dbusBackend{generation: 5, cacheItems: make(map[NodeID]cacheItem), cacheApps: map[string]bool{"org.test": true}}
			if test.item != nil {
				item := *test.item
				item.Object, item.Parent = ref, ref
				backend.cacheItems[item.nodeIDAt(5)] = item
			}
			backend.adjustCachedChildCountLocked(ref, test.delta)
			if backend.cacheApps["org.test"] {
				t.Fatal("application cache remained complete after uncertain child count")
			}
			if _, ok := backend.cacheItems[NodeID{BusName: ref.BusName, ObjectPath: string(ref.ObjectPath), Generation: 5, Incarnation: 1}]; ok {
				t.Fatal("uncertain parent remained in the application cache")
			}
		})
	}
}

func TestCacheReparentDoesNotRevokeMovedChildOrAdvanceBackendEpoch(t *testing.T) {
	const generation = 5
	app := cacheItem{
		Object:      cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/app")},
		Application: cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/app")},
	}
	oldParent := cacheItem{
		Object:      cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/old_parent")},
		Application: app.Object, Parent: app.Object,
	}
	newParent := cacheItem{
		Object:      cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/new_parent")},
		Application: app.Object, Parent: app.Object,
	}
	child := cacheItem{
		Object:      cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/child")},
		Application: app.Object, Parent: oldParent.Object,
	}
	oldID, newID, childID := oldParent.nodeIDAt(generation), newParent.nodeIDAt(generation), child.nodeIDAt(generation)
	backend := &dbusBackend{
		generation: generation,
		cacheItems: map[NodeID]cacheItem{app.nodeIDAt(generation): app, oldID: oldParent, newID: newParent, childID: child},
		cacheApps:  map[string]bool{"org.test": true},
		incarnations: map[objectIdentity]uint64{
			{busName: "org.test", objectPath: "/app"}:        1,
			{busName: "org.test", objectPath: "/old_parent"}: 1,
			{busName: "org.test", objectPath: "/new_parent"}: 1,
			{busName: "org.test", objectPath: "/child"}:      1,
		},
		parents: map[objectIdentity]objectIdentity{
			{busName: "org.test", objectPath: "/old_parent"}: {busName: "org.test", objectPath: "/app"},
			{busName: "org.test", objectPath: "/new_parent"}: {busName: "org.test", objectPath: "/app"},
			{busName: "org.test", objectPath: "/child"}:      {busName: "org.test", objectPath: "/new_parent"},
		},
	}
	backend.prepareEvent(&dbus.Signal{
		Name: cacheIface + ":RemoveAccessible", Sender: "org.test",
		Body: []any{[]any{"org.test", dbus.ObjectPath("/old_parent")}},
	}, Event{Kind: cacheIface + ":RemoveAccessible"})
	if backend.Generation() != generation {
		t.Fatalf("backend generation = %d, want unchanged %d", backend.Generation(), generation)
	}
	if err := backend.validateHandle(oldID); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("removed parent handle validation = %v, want ErrStaleNode", err)
	}
	if err := backend.validateHandle(childID); err != nil {
		t.Fatalf("moved child handle was revoked with old parent: %v", err)
	}
	if backend.cacheApps["org.test"] {
		t.Fatal("cache remained complete after cached and observed ancestry disagreed")
	}
	if _, ok := backend.cacheItems[childID]; ok {
		t.Fatal("uncertain application cache retained child topology")
	}
}

func TestFreshChildrenBypassATSPICacheItems(t *testing.T) {
	root := NodeID{BusName: "org.test.App", ObjectPath: "/root", Generation: 1}
	child := NodeID{BusName: root.BusName, ObjectPath: "/cached-child", Generation: 1}
	item := cacheItem{
		Object: cacheObjectRef{BusName: child.BusName, ObjectPath: dbus.ObjectPath(child.ObjectPath)},
		Parent: cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)},
	}
	backend := &dbusBackend{
		generation: 1,
		cacheItems: map[NodeID]cacheItem{child: item},
		cacheApps:  map[string]bool{root.BusName: true},
	}
	if children, err := backend.children(context.Background(), root); err != nil || len(children) != 1 {
		t.Fatalf("cached children = %+v, %v; want one cache item", children, err)
	}
	if _, err := backend.childrenFresh(context.Background(), root); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("fresh children error = %v, want direct-provider disconnected error", err)
	}
}

func TestFindMatchesStatesAndAttributes(t *testing.T) {
	if !matchesStates([]string{"enabled", "focused"}, []string{"focused"}) {
		t.Fatal("state match rejected present state")
	}
	if matchesStates([]string{"enabled"}, []string{"focused"}) {
		t.Fatal("state match accepted absent state")
	}
	if !matchesAttributes(map[string]string{"Kind": "Primary"}, map[string]string{"kind": "primary"}) {
		t.Fatal("attribute matching should be case-insensitive")
	}
}

func TestRedactSensitiveNodeKeepsUsefulSemantics(t *testing.T) {
	node := Node{Name: "Password", Role: "entry", Text: "secret", States: []string{"sensitive"}, Attributes: map[string]string{"value": "secret", "aria-secret": "secret", "aria-label": "Password", "class": "field"}}
	redactSensitiveNode(&node)
	if !node.Redacted || node.Text != "" {
		t.Fatalf("redacted node = %+v", node)
	}
	if _, ok := node.Attributes["value"]; ok {
		t.Fatal("value attribute leaked")
	}
	if _, ok := node.Attributes["aria-secret"]; ok {
		t.Fatal("secret attribute leaked")
	}
	if node.Attributes["aria-label"] != "Password" || node.Attributes["class"] != "field" {
		t.Fatalf("semantic attributes removed: %+v", node.Attributes)
	}
}

type walkerFake struct {
	nodes      map[NodeID]Node
	child      map[NodeID][]objectRef
	generation uint64
}

func (f walkerFake) readNode(_ context.Context, id, parent NodeID, _ int, _ bool) (Node, error) {
	node, ok := f.nodes[id]
	if !ok {
		return Node{}, fmt.Errorf("missing %s", id.ObjectPath)
	}
	node.Parent = parent
	return node, nil
}
func (f walkerFake) children(_ context.Context, id NodeID) ([]objectRef, error) {
	return f.child[id], nil
}
func (f walkerFake) refID(ref objectRef) NodeID {
	return NodeID{BusName: ref.BusName, ObjectPath: string(ref.ObjectPath), Generation: f.generation}
}

func TestSnapshotWalkerBoundsDepthNodesAndCycles(t *testing.T) {
	root := NodeID{BusName: "b", ObjectPath: "/root", Generation: 1}
	child := NodeID{BusName: "b", ObjectPath: "/child", Generation: 1}
	fake := walkerFake{
		nodes:      map[NodeID]Node{root: {ID: root, ChildCount: 1}, child: {ID: child, ChildCount: 1}},
		child:      map[NodeID][]objectRef{root: {{BusName: "b", ObjectPath: "/child"}}, child: {{BusName: "b", ObjectPath: "/root"}}},
		generation: root.Generation,
	}
	walker := snapshotWalker{backend: fake, opts: SnapshotOptions{MaxDepth: 1, MaxNodes: 10, MaxTextBytes: 10}, snapshot: Snapshot{}, seen: map[NodeID]struct{}{}}
	if _, err := walker.walk(context.Background(), root, NodeID{}, 0); err != nil {
		t.Fatalf("walk = %v", err)
	}
	if len(walker.snapshot.Nodes) != 2 || !walker.snapshot.Truncated {
		t.Fatalf("snapshot = %+v", walker.snapshot)
	}
	if len(walker.snapshot.Nodes[1].Children) != 0 {
		t.Fatalf("depth-limited child unexpectedly walked: %+v", walker.snapshot.Nodes)
	}
}

func TestSnapshotWalkerRecordsChildReadWarnings(t *testing.T) {
	root := NodeID{BusName: "b", ObjectPath: "/root", Generation: 1}
	fake := walkerFake{nodes: map[NodeID]Node{root: {ID: root, ChildCount: 1}}, child: map[NodeID][]objectRef{root: {{BusName: "b", ObjectPath: "/missing"}}}, generation: root.Generation}
	walker := snapshotWalker{backend: fake, opts: SnapshotOptions{MaxDepth: 4, MaxNodes: 4, MaxTextBytes: 10}, snapshot: Snapshot{}, seen: map[NodeID]struct{}{}}
	if _, err := walker.walk(context.Background(), root, NodeID{}, 0); err != nil {
		t.Fatalf("walk = %v", err)
	}
	if len(walker.snapshot.Warnings) != 1 {
		t.Fatalf("warnings = %+v", walker.snapshot.Warnings)
	}
}

func TestSnapshotWalkerRejectsShortChildListAsIncomplete(t *testing.T) {
	root := NodeID{BusName: "b", ObjectPath: "/root", Generation: 1}
	child := NodeID{BusName: "b", ObjectPath: "/child", Generation: 1}
	fake := walkerFake{
		nodes: map[NodeID]Node{
			root:  {ID: root, ChildCount: 2},
			child: {ID: child, Parent: root, Role: "button", Name: "Save"},
		},
		child:      map[NodeID][]objectRef{root: {{BusName: child.BusName, ObjectPath: dbus.ObjectPath(child.ObjectPath)}}},
		generation: root.Generation,
	}
	walker := snapshotWalker{backend: fake, opts: SnapshotOptions{MaxDepth: 4, MaxNodes: 4, MaxTextBytes: 32}, snapshot: Snapshot{}, seen: map[NodeID]struct{}{}}
	if _, err := walker.walk(context.Background(), root, NodeID{}, 0); err != nil {
		t.Fatalf("walk = %v", err)
	}
	if !walker.snapshot.Truncated {
		t.Fatalf("short child list was treated as complete: %+v", walker.snapshot)
	}
	if err := ValidateSemanticSnapshot(walker.snapshot, Selector{Name: "Save", Role: "button"}); !errors.Is(err, ErrIncompleteSnapshot) {
		t.Fatalf("ValidateSemanticSnapshot error = %v, want ErrIncompleteSnapshot", err)
	}
}

func TestSnapshotWalkerSkipsConfiguredRoleSubtree(t *testing.T) {
	root := NodeID{BusName: "b", ObjectPath: "/root", Generation: 1}
	landmark := NodeID{BusName: "b", ObjectPath: "/landmark", Generation: 1}
	entry := NodeID{BusName: "b", ObjectPath: "/entry", Generation: 1}
	fake := walkerFake{
		nodes: map[NodeID]Node{
			root:     {ID: root, ChildCount: 2},
			landmark: {ID: landmark, Role: "landmark", ChildCount: 1},
			entry:    {ID: entry, Role: "entry", Name: "command"},
		},
		child: map[NodeID][]objectRef{
			root:     {{BusName: "b", ObjectPath: "/landmark"}, {BusName: "b", ObjectPath: "/entry"}},
			landmark: {{BusName: "b", ObjectPath: "/entry"}},
		},
		generation: root.Generation,
	}
	walker := snapshotWalker{
		backend: fake,
		opts: SnapshotOptions{
			MaxDepth: 4, MaxNodes: 8, MaxTextBytes: 32, SkipRoles: []string{"landmark"},
		},
		snapshot: Snapshot{},
		seen:     map[NodeID]struct{}{},
	}
	if _, err := walker.walk(context.Background(), root, NodeID{}, 0); err != nil {
		t.Fatalf("walk = %v", err)
	}
	if len(walker.snapshot.Nodes) != 3 {
		t.Fatalf("snapshot nodes = %+v, want root, landmark, entry sibling", walker.snapshot.Nodes)
	}
	if !walker.snapshot.Truncated {
		t.Fatal("snapshot omitted a configured subtree without marking its target set incomplete")
	}
	for _, node := range walker.snapshot.Nodes {
		if node.ID == landmark && len(node.Children) != 0 {
			t.Fatalf("skipped landmark children = %+v", node.Children)
		}
	}
}

func TestSnapshotWalkerHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := NodeID{BusName: "b", ObjectPath: "/root", Generation: 1}
	walker := snapshotWalker{backend: walkerFake{nodes: map[NodeID]Node{root: {ID: root}}, generation: root.Generation}, opts: SnapshotOptions{MaxDepth: 1, MaxNodes: 1, MaxTextBytes: 1}, snapshot: Snapshot{}, seen: map[NodeID]struct{}{}}
	if _, err := walker.walk(ctx, root, NodeID{}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("walk error = %v", err)
	}
}

func TestSnapshotWalkerTagsChildrenWithCurrentGeneration(t *testing.T) {
	// Each iteration models a fresh snapshot after invalidation or explicit
	// reopen. Raw child refs carry no generation; the backend's refID method
	// must supply the current one for every traversal level.
	for _, generation := range []uint64{2, 9} {
		t.Run(fmt.Sprintf("generation-%d", generation), func(t *testing.T) {
			root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: generation}
			child := NodeID{BusName: "org.test", ObjectPath: "/child", Generation: generation}
			fake := walkerFake{
				nodes:      map[NodeID]Node{root: {ID: root, ChildCount: 1}, child: {ID: child}},
				child:      map[NodeID][]objectRef{root: {{BusName: root.BusName, ObjectPath: "/child"}}},
				generation: generation,
			}
			walker := snapshotWalker{backend: fake, opts: SnapshotOptions{MaxDepth: 4, MaxNodes: 4, MaxTextBytes: 32}, snapshot: Snapshot{}, seen: map[NodeID]struct{}{}}
			if _, err := walker.walk(context.Background(), root, NodeID{}, 0); err != nil {
				t.Fatalf("walk = %v", err)
			}
			if len(walker.snapshot.Nodes) != 2 || len(walker.snapshot.Nodes[0].Children) != 1 {
				t.Fatalf("snapshot = %+v", walker.snapshot)
			}
			if got := walker.snapshot.Nodes[0].Children[0]; got != child {
				t.Fatalf("child handle = %+v, want %+v", got, child)
			}
		})
	}
}

func TestOpenRuntimeReportsMissingSessionBus(t *testing.T) {
	_, err := OpenRuntime(env.FromEnviron([]string{}))
	if err == nil {
		t.Fatal("OpenRuntime unexpectedly succeeded without session bus")
	}
}

type testAccessibilityBusProvider struct {
	address string
}

func (p testAccessibilityBusProvider) GetAddress() (string, *dbus.Error) {
	return p.address, nil
}

func startTestDBus(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/var/tmp", "pf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "bus")
	address := "unix:path=" + path
	var stderr bytes.Buffer
	cmd := exec.Command("dbus-daemon", "--session", "--address="+address, "--nofork", "--nopidfile")
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dbus-daemon: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	// Creating the socket pathname does not mean dbus-daemon is listening
	// or has finished authentication setup. In a parallel race-test run
	// connecting at that point intermittently fails with ECONNREFUSED.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	var lastErr error
	for {
		if _, statErr := os.Stat(path); statErr == nil {
			probeCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			probe, connectErr := dbusutil.ConnectContext(probeCtx, address)
			cancel()
			if connectErr == nil {
				if closeErr := probe.Close(); closeErr != nil {
					t.Fatalf("close readiness probe: %v", closeErr)
				}
				return address
			}
			lastErr = connectErr
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("dbus-daemon not ready at %s: %v: %s", path, lastErr, strings.TrimSpace(stderr.String()))
		}
	}
}

func testDBusID(t *testing.T, conn *dbus.Conn) string {
	t.Helper()
	var id string
	if err := conn.BusObject().Call("org.freedesktop.DBus.GetId", 0).Store(&id); err != nil {
		t.Fatalf("get D-Bus ID: %v", err)
	}
	return id
}

func TestOpenRuntimeUsesSessionGetAddressWithoutOverride(t *testing.T) {
	sessionAddress := startTestDBus(t)
	accessibilityAddress := startTestDBus(t)

	sessionConn, err := dbusutil.SessionBusAddress(sessionAddress)
	if err != nil {
		t.Fatalf("connect test session bus: %v", err)
	}
	defer sessionConn.Close()
	provider := &testAccessibilityBusProvider{address: accessibilityAddress}
	exportErr := sessionConn.Export(provider, busPath, busService)
	if exportErr != nil {
		t.Fatalf("export org.a11y.Bus provider: %v", exportErr)
	}
	reply, err := sessionConn.RequestName(busService, dbus.NameFlagDoNotQueue)
	if err != nil {
		t.Fatalf("claim %s: %v", busService, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim %s reply = %d, want primary owner", busService, reply)
	}

	expectedAccess, err := dbusutil.ConnectContext(context.Background(), accessibilityAddress)
	if err != nil {
		t.Fatalf("connect expected accessibility bus: %v", err)
	}
	defer expectedAccess.Close()
	wantID := testDBusID(t, expectedAccess)

	backend, err := OpenRuntimeContext(context.Background(), env.FromEnviron([]string{
		"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress,
		// These host/X-root or legacy routes must not redirect the client when
		// the canonical managed override is absent.
		"AT_SPI_BUS=unix:path=/wrong/accessibility-bus",
		"AT_SPI_BUS_ADDRESS=unix:path=/wrong/legacy-accessibility-bus",
	}))
	if err != nil {
		t.Fatalf("OpenRuntime through session GetAddress: %v", err)
	}
	defer backend.Close()

	opened, ok := backend.(*dbusBackend)
	if !ok {
		t.Fatalf("OpenRuntime backend type = %T, want *dbusBackend", backend)
	}
	if got := testDBusID(t, opened.access); got != wantID {
		t.Fatalf("accessibility bus ID = %q, want session provider bus %q", got, wantID)
	}
	if got := opened.runtime.Get("ATSPI_BUS_ADDRESS"); got != "" {
		t.Fatalf("runtime unexpectedly supplied explicit ATSPI_BUS_ADDRESS %q", got)
	}
}

func TestReopenFailureLeavesBackendDisconnected(t *testing.T) {
	backend := &dbusBackend{runtime: env.FromEnviron([]string{}), generation: 4}
	_, err := backend.Reopen(context.Background())
	if err == nil {
		t.Fatal("Reopen unexpectedly succeeded without a target session bus")
	}
	if !errors.Is(backend.connected(), ErrDisconnected) {
		t.Fatalf("backend after failed reopen = %v, want disconnected", backend.connected())
	}
	if got := backend.Generation(); got != 5 {
		t.Fatalf("generation after failed reopen = %d, want 5", got)
	}
}

func TestSnapshotRequiresExplicitScope(t *testing.T) {
	backend := &dbusBackend{generation: 1, access: &dbus.Conn{}}
	_, err := backend.Snapshot(context.Background(), NodeID{}, SnapshotOptions{})
	if !errors.Is(err, ErrScope) {
		t.Fatalf("unscoped snapshot error = %v", err)
	}
	_, err = backend.Find(context.Background(), NodeID{}, Query{Name: "button"}, SnapshotOptions{})
	if !errors.Is(err, ErrScope) {
		t.Fatalf("unscoped find error = %v", err)
	}
}

func TestBuildOutlineAndCandidateContextPreserveGeneration(t *testing.T) {
	rootID := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 4}
	buttonID := NodeID{BusName: "org.test", ObjectPath: "/button", Generation: 4}
	snapshot := Snapshot{Root: Node{ID: rootID, Role: "frame", Children: []NodeID{buttonID}}, Nodes: []Node{
		{ID: rootID, Role: "frame", Name: "Editor", Children: []NodeID{buttonID}},
		{ID: buttonID, Parent: rootID, Role: "button", Name: "Save", Actions: []Action{{Index: 0, Name: "click"}}, Visible: true, Enabled: true},
	}, Generation: 4}
	outline := BuildOutline(snapshot, OutlineOptions{MaxDepth: 2, MaxNodes: 4})
	if outline.Root.ID != rootID || len(outline.Root.Children) != 1 || outline.Root.Children[0].ID.Generation != 4 {
		t.Fatalf("outline = %+v", outline)
	}
	candidates := CandidatesForQuery(snapshot, Query{Name: "save"}, 4)
	if len(candidates) != 1 || candidates[0].ID != buttonID || len(candidates[0].Breadcrumb) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
}

func TestBuildOutlinePreservesProviderIncompleteEvidence(t *testing.T) {
	rootID := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 4}
	snapshot := Snapshot{
		Root: Node{ID: rootID}, Nodes: []Node{{ID: rootID}}, Generation: 4,
		ProviderErrors: 1, Warnings: []string{"/child: child disappeared"},
	}
	outline := BuildOutline(snapshot, OutlineOptions{})
	if outline.Truncated || outline.ProviderErrors != 1 || len(outline.Warnings) != 1 {
		t.Fatalf("outline partial evidence = truncated:%t provider_errors:%d warnings:%v", outline.Truncated, outline.ProviderErrors, outline.Warnings)
	}
}

func TestTypedAutomationProtocolFixtureCoversMutations(t *testing.T) {
	type call struct {
		method string
		args   []any
	}
	var calls []call
	backend := &dbusBackend{generation: 1, callOverride: func(_ context.Context, _ NodeID, method string, args []any) (any, error) {
		calls = append(calls, call{method: method, args: args})
		if method == actionIface+".GetActions" {
			return []actionMetadataWire{{LocalizedName: "Activate", Description: "Activate this item", KeyBinding: "Enter"}, {LocalizedName: "Alternative", Description: "Run the alternative action", KeyBinding: "Alt+Enter"}}, nil
		}
		if method == actionIface+".GetName" {
			if len(args) > 0 {
				if idx, ok := args[0].(int32); ok {
					switch idx {
					case 0:
						return "activate", nil
					case 1:
						return "alternate", nil
					}
				}
			}
			return "", nil
		}
		return true, nil
	}}
	id := NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 1}
	ctx := context.Background()
	checks := []struct {
		name     string
		call     func() error
		expected []call
	}{
		{"action", func() error { return backend.InvokeAction(ctx, id, 0) }, []call{{actionIface + ".GetActions", nil}, {actionIface + ".DoAction", []any{int32(0)}}}},
		{"action-name", func() error { _, err := backend.InvokeActionByName(ctx, id, "alternate"); return err }, []call{{actionIface + ".GetActions", nil}, {actionIface + ".GetName", []any{int32(0)}}, {actionIface + ".GetName", []any{int32(1)}}, {actionIface + ".DoAction", []any{int32(1)}}}},
		{"default-action", func() error { _, err := backend.InvokeDefaultAction(ctx, id); return err }, []call{{actionIface + ".GetActions", nil}, {actionIface + ".GetName", []any{int32(0)}}, {actionIface + ".DoAction", []any{int32(0)}}}},
		{"focus", func() error { return backend.GrabFocus(ctx, id) }, []call{{componentIface + ".GrabFocus", nil}}},
		{"scroll", func() error { return backend.ScrollTo(ctx, id, ScrollAnyWhere) }, []call{{componentIface + ".ScrollTo", []any{uint32(ScrollAnyWhere)}}}},
		{"scroll-point", func() error { return backend.ScrollToPoint(ctx, id, CoordTypeWindow, 10, 20) }, []call{{componentIface + ".ScrollToPoint", []any{uint32(CoordTypeWindow), int32(10), int32(20)}}}},
		{"set-position", func() error { return backend.SetPosition(ctx, id, 11, 12, CoordTypeParent) }, []call{{componentIface + ".SetPosition", []any{int32(11), int32(12), uint32(CoordTypeParent)}}}},
		{"set-size", func() error { return backend.SetSize(ctx, id, 640, 480) }, []call{{componentIface + ".SetSize", []any{int32(640), int32(480)}}}},
		{"set-extents", func() error { return backend.SetExtents(ctx, id, 1, 2, 640, 480, CoordTypeScreen) }, []call{{componentIface + ".SetExtents", []any{int32(1), int32(2), int32(640), int32(480), uint32(CoordTypeScreen)}}}},
		{"value", func() error { return backend.SetValue(ctx, id, 0.5) }, []call{{propertiesIface + ".Set", []any{valueIface, "CurrentValue", dbus.MakeVariant(0.5)}}}},
		{"text", func() error { return backend.SetTextContents(ctx, id, "safe") }, []call{{editableTextIface + ".SetTextContents", []any{"safe"}}}},
		{"insert", func() error { return backend.InsertText(ctx, id, 0, "é😀") }, []call{{editableTextIface + ".InsertText", []any{int32(0), "é😀", int32(6)}}}},
		{"copy", func() error { return backend.CopyText(ctx, id, 0, 1) }, []call{{editableTextIface + ".CopyText", []any{int32(0), int32(1)}}}},
		{"cut", func() error { return backend.CutText(ctx, id, 0, 1) }, []call{{editableTextIface + ".CutText", []any{int32(0), int32(1)}}}},
		{"paste", func() error { return backend.PasteText(ctx, id, 0) }, []call{{editableTextIface + ".PasteText", []any{int32(0)}}}},
		{"caret", func() error { return backend.SetCaretOffset(ctx, id, 1) }, []call{{textIface + ".SetCaretOffset", []any{int32(1)}}}},
		{"selection", func() error { return backend.SetTextSelection(ctx, id, 0, 0, 1) }, []call{{textIface + ".SetSelection", []any{int32(0), int32(0), int32(1)}}}},
		{"add-selection", func() error { return backend.AddTextSelection(ctx, id, 0, 1) }, []call{{textIface + ".AddSelection", []any{int32(0), int32(1)}}}},
		{"remove-selection", func() error { return backend.RemoveTextSelection(ctx, id, 0) }, []call{{textIface + ".RemoveSelection", []any{int32(0)}}}},
		{"document-selections", func() error {
			return backend.SetTextSelections(ctx, id, []DocumentTextSelection{{StartObject: id, EndObject: id, StartOffset: 1, EndOffset: 2, StartIsActive: true}})
		}, []call{{documentIface + ".SetTextSelections", []any{[]documentTextSelectionWire{{StartObject: objectRef{BusName: id.BusName, ObjectPath: dbus.ObjectPath(id.ObjectPath)}, StartOffset: 1, EndObject: objectRef{BusName: id.BusName, ObjectPath: dbus.ObjectPath(id.ObjectPath)}, EndOffset: 2, StartIsActive: true}}}}}},
		{"select-child", func() error { return backend.SelectChild(ctx, id, 0) }, []call{{selectionIface + ".SelectChild", []any{int32(0)}}}},
		{"deselect-child", func() error { return backend.DeselectChild(ctx, id, 0) }, []call{{selectionIface + ".DeselectChild", []any{int32(0)}}}},
		{"select-all", func() error { return backend.SelectAll(ctx, id) }, []call{{selectionIface + ".SelectAll", nil}}},
		{"clear-selection", func() error { return backend.ClearSelection(ctx, id) }, []call{{selectionIface + ".ClearSelection", nil}}},
		{"deselect-selected-child", func() error { return backend.DeselectSelectedChild(ctx, id) }, []call{{selectionIface + ".DeselectSelectedChild", nil}}},
		{"select-row", func() error { return backend.SelectRow(ctx, id, 0) }, []call{{tableIface + ".AddRowSelection", []any{int32(0)}}}},
		{"deselect-row", func() error { return backend.DeselectRow(ctx, id, 0) }, []call{{tableIface + ".RemoveRowSelection", []any{int32(0)}}}},
		{"select-column", func() error { return backend.SelectColumn(ctx, id, 0) }, []call{{tableIface + ".AddColumnSelection", []any{int32(0)}}}},
		{"deselect-column", func() error { return backend.DeselectColumn(ctx, id, 0) }, []call{{tableIface + ".RemoveColumnSelection", []any{int32(0)}}}},
	}
	for _, check := range checks {
		calls = nil
		if err := check.call(); err != nil {
			t.Fatalf("%s: %v", check.name, err)
		}
		if !reflect.DeepEqual(calls, check.expected) {
			t.Fatalf("%s wire calls = %#v, want %#v", check.name, calls, check.expected)
		}
	}
}

func TestInsertTextWireLengthAdaptsKnownQtProvider(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		toolkit string
		want    int
	}{
		{name: "spec-compliant provider uses UTF-8 bytes", text: "é😀", toolkit: "GTK", want: 6},
		{name: "unknown provider uses UTF-8 bytes", text: "é😀", toolkit: "", want: 6},
		{name: "Qt uses UTF-16 units", text: "é😀", toolkit: "Qt", want: 3},
		{name: "Qt matching is case-insensitive", text: "東京", toolkit: "qt", want: 2},
		{name: "ASCII has the same count", text: "plain", toolkit: "Qt", want: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := insertTextWireLength(tt.text, tt.toolkit); got != tt.want {
				t.Fatalf("insertTextWireLength(%q, %q) = %d, want %d", tt.text, tt.toolkit, got, tt.want)
			}
		})
	}
}

func TestActionMetadataUsesGetActionsAndGetNameConsistently(t *testing.T) {
	id := NodeID{BusName: "org.test", ObjectPath: "/button", Generation: 1}
	var calls []string
	backend := &dbusBackend{generation: 1, callOverride: func(_ context.Context, _ NodeID, method string, args []any) (any, error) {
		calls = append(calls, method)
		switch method {
		case actionIface + ".GetActions":
			return []actionMetadataWire{{LocalizedName: "Activar", Description: "Guarda el documento", KeyBinding: "Ctrl+S"}}, nil
		case actionIface + ".GetName":
			if len(args) == 1 && args[0] == int32(0) {
				return "save", nil
			}
			return "", fmt.Errorf("unexpected GetName arguments: %v", args)
		case actionIface + ".DoAction":
			return true, nil
		default:
			return nil, fmt.Errorf("unexpected method %s", method)
		}
	}}

	got, err := backend.InvokeActionByName(context.Background(), id, "SAVE")
	want := Action{Index: 0, Name: "save", LocalizedName: "Activar", Description: "Guarda el documento", KeyBinding: "Ctrl+S"}
	if err != nil || got != want {
		t.Fatalf("InvokeActionByName = %+v, %v; want %+v", got, err, want)
	}
	wantCalls := []string{actionIface + ".GetActions", actionIface + ".GetName", actionIface + ".DoAction"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("named invocation calls = %v, want %v", calls, wantCalls)
	}

	calls = nil
	var node Node
	backend.readNodeOptional(context.Background(), id, []string{actionIface}, &node)
	if len(node.Actions) != 1 || node.Actions[0] != want {
		t.Fatalf("snapshot action = %+v, want %+v", node.Actions, []Action{want})
	}
	wantCalls = []string{actionIface + ".GetActions", actionIface + ".GetName"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("snapshot action calls = %v, want %v", calls, wantCalls)
	}
}

func TestNamedActionReturnsProviderErrorWhenMachineNameLookupFails(t *testing.T) {
	providerErr := errors.New("provider rejected GetName")
	backend := &dbusBackend{generation: 1, callOverride: func(_ context.Context, _ NodeID, method string, _ []any) (any, error) {
		switch method {
		case actionIface + ".GetActions":
			return []actionMetadataWire{{LocalizedName: "Abrir"}}, nil
		case actionIface + ".GetName":
			return nil, providerErr
		default:
			return true, nil
		}
	}}
	id := NodeID{BusName: "org.test", ObjectPath: "/button", Generation: 1}
	_, err := backend.InvokeActionByName(context.Background(), id, "open")
	if err == nil || errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), providerErr.Error()) {
		t.Fatalf("InvokeActionByName error = %v, want surfaced provider GetName error", err)
	}
}

func TestDocumentTextSelectionsRejectStaleEndpoints(t *testing.T) {
	backend := &dbusBackend{generation: 2, callOverride: func(context.Context, NodeID, string, []any) (any, error) {
		return true, nil
	}}
	id := NodeID{BusName: "org.test", ObjectPath: "/document", Generation: 2}
	stale := NodeID{BusName: "org.test", ObjectPath: "/text", Generation: 1}
	err := backend.SetTextSelections(context.Background(), id, []DocumentTextSelection{{StartObject: stale, EndObject: id}})
	if !errors.Is(err, ErrStaleNode) {
		t.Fatalf("stale document selection endpoint error = %v, want stale-node", err)
	}
}

func TestTypedAutomationRejectsStaleAndDisconnectedHandles(t *testing.T) {
	backend := &dbusBackend{generation: 2, callOverride: func(context.Context, NodeID, string, []any) (any, error) { return true, nil }}
	if err := backend.GrabFocus(context.Background(), NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 1}); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("stale mutation error = %v", err)
	}
	backend.markDisconnected()
	if err := backend.GrabFocus(context.Background(), NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 2}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("disconnected mutation error = %v", err)
	}
}

func TestTypedAutomationRejectsInvalidArguments(t *testing.T) {
	backend := &dbusBackend{generation: 1, callOverride: func(context.Context, NodeID, string, []any) (any, error) { return true, nil }}
	id := NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 1}
	ctx := context.Background()

	// PasteText with negative position.
	if err := backend.PasteText(ctx, id, -1); err == nil {
		t.Fatal("expected error for negative paste position")
	}
	// SetTextSelection with negative index.
	if err := backend.SetTextSelection(ctx, id, -1, 0, 1); err == nil {
		t.Fatal("expected error for negative selection index")
	}
	// SetTextSelection with end before start.
	if err := backend.SetTextSelection(ctx, id, 0, 5, 3); err == nil {
		t.Fatal("expected error for end before start")
	}
	// AddTextSelection with negative index.
	if err := backend.AddTextSelection(ctx, id, -1, 1); err == nil {
		t.Fatal("expected error for negative start")
	}
	// AddTextSelection with end before start.
	if err := backend.AddTextSelection(ctx, id, 5, 3); err == nil {
		t.Fatal("expected error for end before start")
	}
	// RemoveTextSelection with negative index.
	if err := backend.RemoveTextSelection(ctx, id, -1); err == nil {
		t.Fatal("expected error for negative selection")
	}
	// SelectChild with negative index.
	if err := backend.SelectChild(ctx, id, -1); err == nil {
		t.Fatal("expected error for negative child index")
	}
	// DeselectChild with negative index.
	if err := backend.DeselectChild(ctx, id, -1); err == nil {
		t.Fatal("expected error for negative child index")
	}
	// SelectRow with negative index.
	if err := backend.SelectRow(ctx, id, -1); err == nil {
		t.Fatal("expected error for negative row index")
	}
	// DeselectRow with negative index.
	if err := backend.DeselectRow(ctx, id, -1); err == nil {
		t.Fatal("expected error for negative row index")
	}
	// SelectColumn with negative index.
	if err := backend.SelectColumn(ctx, id, -1); err == nil {
		t.Fatal("expected error for negative column index")
	}
	// DeselectColumn with negative index.
	if err := backend.DeselectColumn(ctx, id, -1); err == nil {
		t.Fatal("expected error for negative column index")
	}
}

func TestTypedAutomationFailsClosedWithoutRetry(t *testing.T) {
	id := NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 1}
	tests := []struct {
		name      string
		result    any
		callError error
		want      error
	}{
		{name: "provider rejection", result: false, want: ErrMutationRejected},
		{name: "malformed boolean", result: "yes"},
		{name: "unsupported interface", callError: errors.New("org.freedesktop.DBus.Error.UnknownInterface"), want: ErrUnsupported},
		{name: "transport failure", callError: errors.New("transport closed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			backend := &dbusBackend{generation: 1, callOverride: func(context.Context, NodeID, string, []any) (any, error) {
				calls++
				return test.result, test.callError
			}}
			err := backend.GrabFocus(context.Background(), id)
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if test.want == nil && err == nil {
				t.Fatal("malformed/failed mutation unexpectedly succeeded")
			}
			if calls != 1 {
				t.Fatalf("mutation calls = %d, want one attempt", calls)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	backend := &dbusBackend{generation: 1, callOverride: func(context.Context, NodeID, string, []any) (any, error) {
		calls++
		return true, nil
	}}
	if err := backend.GrabFocus(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled mutation error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("cancelled mutation made %d provider calls", calls)
	}

	var disconnectingBackend *dbusBackend
	calls = 0
	disconnectingBackend = &dbusBackend{generation: 1, callOverride: func(context.Context, NodeID, string, []any) (any, error) {
		calls++
		disconnectingBackend.markDisconnected()
		return nil, errors.New("transport closed")
	}}
	if err := disconnectingBackend.GrabFocus(context.Background(), id); err == nil {
		t.Fatal("disconnecting mutation unexpectedly succeeded")
	}
	if calls != 1 || disconnectingBackend.Generation() != 2 {
		t.Fatalf("disconnecting mutation calls=%d generation=%d, want one call and generation 2", calls, disconnectingBackend.Generation())
	}
	if err := disconnectingBackend.GrabFocus(context.Background(), id); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("post-disconnect mutation error = %v, want disconnected", err)
	}
	if calls != 1 {
		t.Fatalf("post-disconnect mutation was replayed: %d calls", calls)
	}
}

func TestTypedActionFixtureRejectsMalformedAndUnsupportedReplies(t *testing.T) {
	id := NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 1}
	backend := &dbusBackend{generation: 1, callOverride: func(context.Context, NodeID, string, []any) (any, error) {
		return "not-actions", nil
	}}
	if _, err := backend.InvokeDefaultAction(context.Background(), id); err == nil {
		t.Fatal("malformed action metadata unexpectedly succeeded")
	}
	backend.callOverride = func(context.Context, NodeID, string, []any) (any, error) {
		return nil, errors.New("org.freedesktop.DBus.Error.UnknownMethod")
	}
	if _, err := backend.InvokeDefaultAction(context.Background(), id); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported action error = %v", err)
	}
}

func TestEventFanoutHasOneDeliveryPathAndBoundedDrops(t *testing.T) {
	backend := &dbusBackend{subscribers: map[uint64]*eventSubscriber{
		1: {out: make(chan Event, 1)},
		2: {out: make(chan Event, 1)},
	}}
	first := Event{Kind: "focus", Node: NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 1}, Timestamp: time.Now()}
	backend.deliverEvent(first)
	backend.deliverEvent(Event{Kind: "focus", Node: first.Node, Timestamp: first.Timestamp.Add(time.Second)})
	for id, subscriber := range backend.subscribers {
		got := <-subscriber.out
		if got.Kind != "focus" || got.Node.Generation != 1 {
			t.Fatalf("subscriber %d received %+v", id, got)
		}
		if subscriber.dropped != 1 {
			t.Fatalf("subscriber %d dropped=%d, want one bounded drop", id, subscriber.dropped)
		}
	}
}

func TestEventFanoutConcurrentSubscribersStayRaceFree(t *testing.T) {
	const eventCount = 256
	const subscriberCount = 3
	backend := &dbusBackend{subscribers: make(map[uint64]*eventSubscriber, subscriberCount)}
	for id := uint64(1); id <= subscriberCount; id++ {
		backend.subscribers[id] = &eventSubscriber{out: make(chan Event, eventCount)}
	}

	var writers sync.WaitGroup
	for i := 0; i < eventCount; i++ {
		writers.Add(1)
		go func(i int) {
			defer writers.Done()
			backend.deliverEvent(Event{Kind: "property", Node: NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 1}, Value: fmt.Sprintf("%d", i)})
		}(i)
	}
	writers.Wait()

	for id, subscriber := range backend.subscribers {
		if subscriber.dropped != 0 {
			t.Fatalf("subscriber %d dropped %d events despite a bounded available buffer", id, subscriber.dropped)
		}
		for i := 0; i < eventCount; i++ {
			select {
			case event := <-subscriber.out:
				if event.Node.Generation != 1 || event.Kind != "property" {
					t.Fatalf("subscriber %d received malformed event %+v", id, event)
				}
			case <-time.After(time.Second):
				t.Fatalf("subscriber %d received only %d/%d events", id, i, eventCount)
			}
		}
	}
}

func TestCacheSignalMutationDoesNotPerformSecondGenerationTransition(t *testing.T) {
	backend := &dbusBackend{generation: 5, cacheItems: make(map[NodeID]cacheItem)}
	item := cacheItem{Object: cacheObjectRef{BusName: "org.test", ObjectPath: "/node"}, Application: cacheObjectRef{BusName: "org.test", ObjectPath: "/app"}, Parent: cacheObjectRef{BusName: "org.test", ObjectPath: "/parent"}}
	backend.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: "org.test", Body: []any{item}})
	if got := backend.Generation(); got != 5 {
		t.Fatalf("cache signal changed generation before dispatcher invalidation: %d", got)
	}
	backend.Invalidate(item.nodeID())
	if got := backend.Generation(); got != 6 {
		t.Fatalf("single invalidation advanced generation to %d", got)
	}
}

func TestCacheAddDeltaDoesNotClaimCompleteApplicationCache(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 5}
	item := cacheItem{
		Object:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/new")},
		Application: cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/app")},
		Parent:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)},
	}
	backend := &dbusBackend{generation: 5}
	backend.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: root.BusName, Body: []any{item}})
	if backend.cacheApps[root.BusName] {
		t.Fatal("one AddAccessible delta was treated as a complete GetItems cache")
	}
	if children := backend.cachedChildren(root); children != nil {
		t.Fatalf("incomplete app cache returned %d children as authoritative", len(children))
	}
}

func TestCompleteCacheLoadPromotesDeltaAndKeepsAuthoritativeSiblings(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 5}
	backend := &dbusBackend{generation: 5}
	newItem := cacheItem{
		Object:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/new")},
		Application: cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/app")},
		Parent:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)},
		Index:       1,
	}
	backend.prepareEvent(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: root.BusName, Body: []any{newItem}}, Event{Kind: cacheIface + ":AddAccessible"})
	if backend.cacheApps[root.BusName] {
		t.Fatal("Cache:Add delta claimed full-cache completeness")
	}
	existing := cacheItem{
		Object:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/existing")},
		Application: cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/app")},
		Parent:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)},
		Index:       0,
	}
	if err := backend.publishLoadedCache(root.BusName, []cacheItem{existing, newItem}, backend.Generation(), backend.cacheRevision); err != nil {
		t.Fatalf("publish full GetItems result: %v", err)
	}
	children := backend.cachedChildren(root)
	if len(children) != 2 || children[0].ObjectPath != "/existing" || children[1].ObjectPath != "/new" {
		t.Fatalf("promoted complete cache children=%+v, want both authoritative siblings in index order", children)
	}
}

func TestCacheAddDuringGetItemsCannotPromotePartialCache(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 5}
	backend := &dbusBackend{generation: 5}
	expectedRevision := backend.cacheRevision
	item := cacheItem{
		Object:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/new")},
		Application: cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/app")},
		Parent:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)},
	}
	backend.prepareEvent(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: root.BusName, Body: []any{item}}, Event{Kind: cacheIface + ":AddAccessible"})
	if err := backend.publishLoadedCache(root.BusName, []cacheItem{item}, backend.Generation(), expectedRevision); err != nil {
		t.Fatalf("raced GetItems publication: %v", err)
	}
	if backend.cacheApps[root.BusName] {
		t.Fatal("in-flight GetItems published completeness after a Cache:Add revision")
	}
	if children := backend.cachedChildren(root); children != nil {
		t.Fatalf("raced partial cache returned %d authoritative children", len(children))
	}
}

func TestPreparedEventUsesPostInvalidationGeneration(t *testing.T) {
	backend := &dbusBackend{generation: 5, cacheItems: make(map[NodeID]cacheItem), cacheApps: map[string]bool{"org.test": true}}
	item := cacheItem{
		Object:      cacheObjectRef{BusName: "org.test", ObjectPath: "/node"},
		Application: cacheObjectRef{BusName: "org.test", ObjectPath: "/app"},
		Parent:      cacheObjectRef{BusName: "org.test", ObjectPath: "/parent"},
	}
	sig := &dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: "org.test", Body: []any{item}}
	event := backend.prepareEvent(sig, Event{Kind: sig.Name, Node: NodeID{BusName: "org.test", ObjectPath: "/node"}})
	if event.Node.Generation != 5 {
		t.Fatalf("event handle generation = %d, want unchanged epoch 5", event.Node.Generation)
	}
	if err := backend.validateHandle(event.Node); err != nil {
		t.Fatalf("prepared event handle is not current: %v", err)
	}
	if _, ok := backend.cachedItem(event.Node); !ok {
		t.Fatalf("cache item was not stored in event generation %d", event.Node.Generation)
	}
}

func TestCoalescedSignalStillTransitionsBackend(t *testing.T) {
	backend := &dbusBackend{
		generation: 5,
		cacheItems: map[NodeID]cacheItem{},
		cacheApps:  map[string]bool{"org.test": true},
	}
	base := time.Now()
	sig := func(at time.Time) (*dbus.Signal, Event) {
		s := &dbus.Signal{
			Name:   "org.a11y.atspi.Event.Object:PropertyChange",
			Sender: "org.test",
			Path:   dbus.ObjectPath("/node"),
			Body:   []any{"Name", "changed"},
		}
		return s, Event{Kind: s.Name, Node: NodeID{BusName: s.Sender, ObjectPath: string(s.Path)}, Property: "Name", Timestamp: at}
	}
	lastKey := ""
	var lastAt time.Time

	firstSig, firstEvent := sig(base)
	firstEvent = backend.prepareEvent(firstSig, firstEvent)
	if coalesceEvent(&lastKey, &lastAt, firstEvent) {
		t.Fatal("first signal was coalesced")
	}
	if backend.Generation() != 5 || firstEvent.Node.Generation != 5 || backend.observationRevision != 1 {
		t.Fatalf("first transition = generation %d/event %d/observation %d, want handle epoch 5 and observation 1", backend.Generation(), firstEvent.Node.Generation, backend.observationRevision)
	}

	// A caller may rebuild a snapshot here. The second physical signal must
	// still invalidate that newly rebuilt view even though its delivery is
	// coalesced with the first notification.
	secondSig, secondEvent := sig(base.Add(time.Millisecond))
	secondEvent = backend.prepareEvent(secondSig, secondEvent)
	if !coalesceEvent(&lastKey, &lastAt, secondEvent) {
		t.Fatal("duplicate signal was not delivery-coalesced")
	}
	if backend.Generation() != 5 || secondEvent.Node.Generation != 5 || backend.observationRevision != 2 {
		t.Fatalf("coalesced transition = generation %d/event %d/observation %d, want handle epoch 5 and observation 2", backend.Generation(), secondEvent.Node.Generation, backend.observationRevision)
	}
	if err := backend.validateHandle(firstEvent.Node); err != nil {
		t.Fatalf("property change invalidated object identity: %v", err)
	}
	if backend.cacheItems != nil || backend.cacheApps != nil {
		t.Fatalf("non-cache coalesced signal retained stale cache metadata: items=%v apps=%v", backend.cacheItems, backend.cacheApps)
	}
}

func TestCoalescedCacheSignalAppliesDelta(t *testing.T) {
	backend := &dbusBackend{generation: 5, cacheItems: make(map[NodeID]cacheItem)}
	item := cacheItem{
		Object:      cacheObjectRef{BusName: "org.test", ObjectPath: "/node"},
		Application: cacheObjectRef{BusName: "org.test", ObjectPath: "/app"},
		Parent:      cacheObjectRef{BusName: "org.test", ObjectPath: "/parent"},
	}
	base := time.Now()
	lastKey := ""
	var lastAt time.Time
	for i := 0; i < 2; i++ {
		sig := &dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: "org.test", Body: []any{item}}
		event := backend.prepareEvent(sig, Event{Kind: sig.Name, Node: item.nodeIDAt(backend.Generation()), Timestamp: base.Add(time.Duration(i) * time.Millisecond)})
		if coalesced := coalesceEvent(&lastKey, &lastAt, event); i == 0 && coalesced {
			t.Fatal("first cache signal was coalesced")
		} else if i == 1 && !coalesced {
			t.Fatal("second cache signal was not delivery-coalesced")
		}
		if event.Node.Generation != 5 || backend.Generation() != 5 {
			t.Fatalf("cache addition %d changed object handle epoch: generation %d/event %d, want 5", i, backend.Generation(), event.Node.Generation)
		}
		if _, ok := backend.cachedItem(NodeID{BusName: item.Object.BusName, ObjectPath: string(item.Object.ObjectPath), Generation: 5}); !ok {
			t.Fatalf("cache delta was lost at handle epoch 5")
		}
	}
}

func TestCacheSignalTransitionPreservesAddAndRemoveState(t *testing.T) {
	parent := cacheItem{
		Object:      cacheObjectRef{BusName: "org.test", ObjectPath: "/parent"},
		Application: cacheObjectRef{BusName: "org.test", ObjectPath: "/app"},
		Parent:      cacheObjectRef{BusName: "org.test", ObjectPath: "/app"},
	}
	backend := &dbusBackend{
		generation: 5,
		cacheItems: map[NodeID]cacheItem{parent.nodeIDAt(5): parent},
		cacheApps:  map[string]bool{"org.test": true},
	}
	item := cacheItem{
		Object:      cacheObjectRef{BusName: "org.test", ObjectPath: "/node"},
		Application: cacheObjectRef{BusName: "org.test", ObjectPath: "/app"},
		Parent:      cacheObjectRef{BusName: "org.test", ObjectPath: "/parent"},
	}
	added := backend.prepareEvent(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: "org.test", Body: []any{item}}, Event{Node: NodeID{BusName: "org.test", ObjectPath: "/node"}})
	if added.Node.Generation != 5 || backend.Generation() != 5 {
		t.Fatalf("cache add generation = event %d/backend %d, want stable handle epoch 5", added.Node.Generation, backend.Generation())
	}
	if _, ok := backend.cachedItem(added.Node); !ok {
		t.Fatal("cache add was lost after generation transition")
	}
	removed := backend.prepareEvent(&dbus.Signal{
		Name: cacheIface + ":RemoveAccessible", Sender: "org.test",
		Body: []any{[]any{item.Object.BusName, item.Object.ObjectPath}},
	}, Event{Node: added.Node})
	if removed.Node.Generation != 5 || backend.Generation() != 5 {
		t.Fatalf("cache remove changed global generation = event %d/backend %d, want 5", removed.Node.Generation, backend.Generation())
	}
	if err := backend.validateHandle(added.Node); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("cache remove left the removed object handle valid: %v", err)
	}
	if _, ok := backend.cachedItem(NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 5, Incarnation: 2}); ok {
		t.Fatal("cache remove left the removed object present")
	}
	if !backend.cacheApps["org.test"] {
		t.Fatal("cache application registration was lost during signal invalidation")
	}
	if got := backend.cacheItems[parent.nodeIDAt(5)].ChildCount; got != 0 {
		t.Fatalf("parent cached child count after add and remove = %d, want 0", got)
	}
}

func TestFreshSnapshotChecksCacheAddTopologyAgainstParent(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/window", Generation: 5}
	child := NodeID{BusName: "org.test", ObjectPath: "/window/new", Generation: 5}
	otherWindowChild := NodeID{BusName: "org.test", ObjectPath: "/other_window/new", Generation: 5}
	parent := cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)}
	backend := &dbusBackend{generation: 5, cacheItems: make(map[NodeID]cacheItem)}
	add := func(objectPath string, parent cacheObjectRef) {
		t.Helper()
		item := cacheItem{
			Object:      cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath(objectPath)},
			Application: cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/app")},
			Parent:      parent,
			Name:        "new",
			Role:        43,
		}
		backend.prepareEvent(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: "org.test", Body: []any{item}}, Event{Kind: cacheIface + ":AddAccessible"})
	}
	base := Snapshot{Root: Node{ID: root}, Nodes: []Node{{ID: root}}}
	add(child.ObjectPath, parent)
	if err := backend.observationChangesError(0, root, base, true); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("unobserved in-scope cache addition = %v, want ErrObservationChanged", err)
	}
	observed := Snapshot{
		Root:  Node{ID: root, Children: []NodeID{child}},
		Nodes: []Node{{ID: root, Children: []NodeID{child}}, {ID: child, Parent: root, Name: "new", RoleID: 43}},
	}
	if err := backend.observationChangesError(0, root, observed, true); err != nil {
		t.Fatalf("fresh snapshot that proves added child and parent = %v", err)
	}
	if err := backend.observationChangesError(0, root, observed, false); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("cached snapshot with concurrent cache addition = %v, want ErrObservationChanged", err)
	}

	outsideBackend := &dbusBackend{generation: 5, cacheItems: make(map[NodeID]cacheItem)}
	addOutside := cacheItem{
		Object:      cacheObjectRef{BusName: "org.other", ObjectPath: dbus.ObjectPath(otherWindowChild.ObjectPath)},
		Application: cacheObjectRef{BusName: "org.other", ObjectPath: dbus.ObjectPath("/other_app")},
		Parent:      cacheObjectRef{BusName: "org.other", ObjectPath: dbus.ObjectPath("/other_window")},
	}
	outsideBackend.prepareEvent(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: "org.other", Body: []any{addOutside}}, Event{Kind: cacheIface + ":AddAccessible"})
	if err := outsideBackend.observationChangesError(0, root, base, true); err != nil {
		t.Fatalf("cache addition on a distinct app bus invalidated this window snapshot: %v", err)
	}
}

func TestObservationHistoryEvictionFailsClosed(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/window", Generation: 5}
	backend := &dbusBackend{generation: root.Generation}
	for index := 0; index < observationHistoryLimit+1; index++ {
		backend.prepareEvent(nil, Event{Kind: "object:property-change", Node: NodeID{BusName: root.BusName, ObjectPath: "/observed"}})
	}
	if backend.observationRevision != observationHistoryLimit+1 || backend.observationFloor != 1 {
		t.Fatalf("observation revision/floor = %d/%d, want %d/1", backend.observationRevision, backend.observationFloor, observationHistoryLimit+1)
	}
	snapshot := Snapshot{Root: Node{ID: root}, Nodes: []Node{{ID: root}}}
	if err := backend.observationChangesError(0, root, snapshot, true); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("observation history eviction error = %v, want ErrObservationChanged", err)
	}
}

func TestFreshApplicationResolutionRejectsMixedIdentityObservations(t *testing.T) {
	desktop := NodeID{BusName: registryName, ObjectPath: string(desktopPath), Generation: 5, Incarnation: 1}
	app := NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 5, Incarnation: 1}
	backend := &dbusBackend{generation: 5}
	sig := &dbus.Signal{Name: "org.a11y.atspi.Event.Object:PropertyChange", Sender: app.BusName, Path: dbus.ObjectPath(app.ObjectPath)}
	backend.prepareEvent(sig, Event{Kind: sig.Name, Node: app})
	if err := backend.applicationResolutionChangesError(0, desktop, []Node{{ID: app, Name: "before"}}); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("application property change during fresh resolution = %v, want ErrObservationChanged", err)
	}

	unrelated := &dbusBackend{generation: 5}
	other := NodeID{BusName: "org.other", ObjectPath: "/unrelated", Generation: 5, Incarnation: 1}
	unrelatedSig := &dbus.Signal{Name: "org.a11y.atspi.Event.Object:PropertyChange", Sender: other.BusName, Path: dbus.ObjectPath(other.ObjectPath)}
	unrelated.prepareEvent(unrelatedSig, Event{Kind: unrelatedSig.Name, Node: other})
	if err := unrelated.applicationResolutionChangesError(0, desktop, []Node{{ID: app, Name: "stable"}}); err != nil {
		t.Fatalf("unrelated application property change invalidated fresh resolution: %v", err)
	}
}

func TestFreshWindowResolutionRejectsMixedCandidateObservations(t *testing.T) {
	app := Application{Node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 5, Incarnation: 1}}}
	window := Node{ID: NodeID{BusName: "org.test", ObjectPath: "/window", Generation: 5, Incarnation: 1}, Name: "before", Role: "frame"}
	backend := &dbusBackend{generation: 5}
	sig := &dbus.Signal{Name: "org.a11y.atspi.Event.Object:PropertyChange", Sender: window.ID.BusName, Path: dbus.ObjectPath(window.ID.ObjectPath)}
	backend.prepareEvent(sig, Event{Kind: sig.Name, Node: window.ID})
	desktop := NodeID{BusName: registryName, ObjectPath: string(desktopPath), Generation: 5, Incarnation: 1}
	if err := backend.windowResolutionChangesError(0, desktop, []Application{app}, []Node{window}); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("window property change during fresh resolution = %v, want ErrObservationChanged", err)
	}

	unrelated := &dbusBackend{generation: 5}
	other := NodeID{BusName: "org.other", ObjectPath: "/unrelated", Generation: 5, Incarnation: 1}
	unrelatedSig := &dbus.Signal{Name: "org.a11y.atspi.Event.Object:PropertyChange", Sender: other.BusName, Path: dbus.ObjectPath(other.ObjectPath)}
	unrelated.prepareEvent(unrelatedSig, Event{Kind: unrelatedSig.Name, Node: other})
	if err := unrelated.windowResolutionChangesError(0, desktop, []Application{app}, []Node{window}); err != nil {
		t.Fatalf("unrelated property change invalidated fresh window resolution: %v", err)
	}
}

func TestFreshResolversScopeUnknownCacheRemovalToApplicationBus(t *testing.T) {
	desktop := NodeID{BusName: registryName, ObjectPath: string(desktopPath), Generation: 5, Incarnation: 1}
	app := Application{Node: Node{ID: NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 5, Incarnation: 1}}}
	window := Node{ID: NodeID{BusName: app.ID.BusName, ObjectPath: "/window", Generation: 5, Incarnation: 1}, Role: "frame"}
	check := func(bus string, wantChanged bool) {
		t.Helper()
		backend := &dbusBackend{generation: 5}
		removed := NodeID{BusName: bus, ObjectPath: "/removed", Generation: 5, Incarnation: 1}
		sig := &dbus.Signal{
			Name:   cacheIface + ":RemoveAccessible",
			Sender: bus,
			Body:   []any{[]any{removed.BusName, dbus.ObjectPath(removed.ObjectPath)}},
		}
		backend.prepareEvent(sig, Event{Kind: sig.Name, Node: removed})
		appErr := backend.applicationResolutionChangesError(0, desktop, []Node{app.Node})
		windowErr := backend.windowResolutionChangesError(0, desktop, []Application{app}, []Node{window})
		if wantChanged {
			if !errors.Is(appErr, ErrObservationChanged) || !errors.Is(windowErr, ErrObservationChanged) {
				t.Fatalf("same-app unknown removal errors = application %v, window %v; want ErrObservationChanged", appErr, windowErr)
			}
			return
		}
		if appErr != nil || windowErr != nil {
			t.Fatalf("other-app unknown removal invalidated target: application %v, window %v", appErr, windowErr)
		}
	}
	check("org.other", false)
	check(app.ID.BusName, true)
}

func TestCacheAddRequiresEstablishedSameApplicationAncestry(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/window", Generation: 5}
	backend := &dbusBackend{generation: 5}
	item := cacheItem{
		Object:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/opaque_child")},
		Application: cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/app")},
		Parent:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/opaque_parent")},
	}
	sig := &dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: root.BusName, Body: []any{item}}
	backend.prepareEvent(sig, Event{Kind: sig.Name})
	if err := backend.observationChangesError(0, root, Snapshot{Root: Node{ID: root}, Nodes: []Node{{ID: root}}}, true); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("same-app addition with unknown ancestry = %v, want ErrObservationChanged", err)
	}
}

func TestFreshCacheAdditionRequiresChildParentAndMetadataAgreement(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 5}
	child := NodeID{BusName: "org.test", ObjectPath: "/child", Generation: 5}
	item := cacheItem{
		Object:      cacheObjectRef{BusName: child.BusName, ObjectPath: dbus.ObjectPath(child.ObjectPath)},
		Application: cacheObjectRef{BusName: child.BusName, ObjectPath: dbus.ObjectPath("/app")},
		Parent:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)},
		Name:        "button",
		Role:        43,
	}
	change := observationChange{
		cacheAdd:  true,
		node:      objectIdentity{busName: child.BusName, objectPath: child.ObjectPath},
		parent:    objectIdentity{busName: root.BusName, objectPath: root.ObjectPath},
		cacheItem: item,
	}
	valid := map[objectIdentity]Node{
		{busName: root.BusName, objectPath: root.ObjectPath}:   {ID: root, Children: []NodeID{child}},
		{busName: child.BusName, objectPath: child.ObjectPath}: {ID: child, Parent: root, Name: "button", RoleID: 43},
	}
	if err := cacheAdditionError(change, objectIdentity{busName: root.BusName, objectPath: root.ObjectPath}, valid, true); err != nil {
		t.Fatalf("complete observed addition proof = %v", err)
	}
	wrongParent := make(map[objectIdentity]Node, len(valid))
	for id, node := range valid {
		wrongParent[id] = node
	}
	wrongParent[objectIdentity{busName: child.BusName, objectPath: child.ObjectPath}] = Node{ID: child, Parent: NodeID{BusName: root.BusName, ObjectPath: "/elsewhere", Generation: 5}, Name: "button", RoleID: 43}
	if err := cacheAdditionError(change, objectIdentity{busName: root.BusName, objectPath: root.ObjectPath}, wrongParent, true); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("wrong child parent proof = %v, want ErrObservationChanged", err)
	}
	missingChildLink := make(map[objectIdentity]Node, len(valid))
	for id, node := range valid {
		missingChildLink[id] = node
	}
	parentNode := missingChildLink[objectIdentity{busName: root.BusName, objectPath: root.ObjectPath}]
	parentNode.Children = nil
	missingChildLink[objectIdentity{busName: root.BusName, objectPath: root.ObjectPath}] = parentNode
	if err := cacheAdditionError(change, objectIdentity{busName: root.BusName, objectPath: root.ObjectPath}, missingChildLink, true); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("missing parent's child reference = %v, want ErrObservationChanged", err)
	}
	metadataMismatch := make(map[objectIdentity]Node, len(valid))
	for id, node := range valid {
		metadataMismatch[id] = node
	}
	changedNode := metadataMismatch[objectIdentity{busName: child.BusName, objectPath: child.ObjectPath}]
	changedNode.Name = "different"
	metadataMismatch[objectIdentity{busName: child.BusName, objectPath: child.ObjectPath}] = changedNode
	if err := cacheAdditionError(change, objectIdentity{busName: root.BusName, objectPath: root.ObjectPath}, metadataMismatch, true); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("cache metadata mismatch = %v, want ErrObservationChanged", err)
	}
}

func TestChildrenChangedRemovalStalesTargetBeforeAction(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/window", Generation: 5}
	child := NodeID{BusName: "org.test", ObjectPath: "/window/child", Generation: 5}
	descendant := NodeID{BusName: "org.test", ObjectPath: "/descendant", Generation: 5}
	backend := &dbusBackend{
		generation: 5,
		parents: map[objectIdentity]objectIdentity{
			{busName: descendant.BusName, objectPath: descendant.ObjectPath}: {busName: child.BusName, objectPath: child.ObjectPath},
		},
	}
	signal := &dbus.Signal{
		Name:   "org.a11y.atspi.Event.Object:ChildrenChanged",
		Sender: root.BusName,
		Path:   dbus.ObjectPath(root.ObjectPath),
		Body: []any{
			"remove", int32(0), int32(0),
			dbus.MakeVariant([]any{child.BusName, dbus.ObjectPath(child.ObjectPath)}),
			map[string]dbus.Variant{},
		},
	}
	event := backend.prepareEvent(signal, signalEvent(signal))
	if err := backend.validateHandle(root); err != nil {
		t.Fatalf("child removal invalidated its surviving parent: %v", err)
	}
	if err := backend.validateHandle(child); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("removed child remained a valid action target: %v", err)
	}
	if err := backend.validateHandle(descendant); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("removed descendant remained a valid action target: %v", err)
	}
	actionCalls := 0
	backend.callOverride = func(context.Context, NodeID, string, []any) (any, error) {
		actionCalls++
		return []actionMetadataWire{{}}, nil
	}
	if _, err := backend.InvokeActionByName(context.Background(), child, "delete"); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("removed child action error = %v, want ErrStaleNode", err)
	}
	if actionCalls != 0 {
		t.Fatalf("removed child action made %d provider calls, want none", actionCalls)
	}
	before := Snapshot{Root: Node{ID: root}, Nodes: []Node{{ID: root}, {ID: child, Parent: root}}}
	if err := backend.observationChangesError(0, root, before, true); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("child removal did not invalidate the snapshot containing the target: %v", err)
	}
	if event.Node.Generation != root.Generation {
		t.Fatalf("ChildrenChanged event changed global handle epoch to %d, want %d", event.Node.Generation, root.Generation)
	}
	// A fresh tree after the removal is authoritative. A semantic locator sees
	// the child is absent instead of dispatching through the old snapshot.
	after := Snapshot{Root: Node{ID: root}, Nodes: []Node{{ID: root}}}
	if err := backend.observationChangesError(backend.observationRevision, root, after, true); err != nil {
		t.Fatalf("post-removal fresh resolution remained invalid: %v", err)
	}
	// Reuse of the same object path gets a new incarnation. The old ID must
	// remain stale even after the provider announces the replacement.
	item := cacheItem{
		Object:      cacheObjectRef{BusName: child.BusName, ObjectPath: dbus.ObjectPath(child.ObjectPath)},
		Application: cacheObjectRef{BusName: child.BusName, ObjectPath: dbus.ObjectPath("/application")},
		Parent:      cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)},
	}
	backend.prepareEvent(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: child.BusName, Body: []any{item}}, Event{Kind: cacheIface + ":AddAccessible"})
	replacement := backend.refID(objectRef{BusName: child.BusName, ObjectPath: dbus.ObjectPath(child.ObjectPath)})
	if replacement.Incarnation == child.Incarnation || backend.validateHandle(replacement) != nil {
		t.Fatalf("replacement ID=%+v was not issued as a current new incarnation", replacement)
	}
	if err := backend.validateHandle(child); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("path reuse revived old child handle: %v", err)
	}
}

func TestChildrenChangedRemovalRejectsWindowRootPublication(t *testing.T) {
	parent := NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 5, Incarnation: 1}
	window := NodeID{BusName: "org.test", ObjectPath: "/window", Generation: 5, Incarnation: 1}
	backend := &dbusBackend{generation: 5}
	sig := &dbus.Signal{
		Name:   "org.a11y.atspi.Event.Object:ChildrenChanged",
		Sender: parent.BusName,
		Path:   dbus.ObjectPath(parent.ObjectPath),
		Body: []any{
			"remove", int32(0), int32(0),
			dbus.MakeVariant([]any{window.BusName, dbus.ObjectPath(window.ObjectPath)}),
			map[string]dbus.Variant{},
		},
	}
	backend.prepareEvent(sig, Event{Kind: sig.Name, Node: parent})
	if backend.Generation() != window.Generation {
		t.Fatalf("scoped window removal changed global generation to %d, want %d", backend.Generation(), window.Generation)
	}
	if err := backend.validateHandle(parent); err != nil {
		t.Fatalf("window removal invalidated surviving application parent: %v", err)
	}
	if err := backend.validateHandle(window); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("removed window root remains valid: %v", err)
	}
	oldSnapshot := Snapshot{Root: Node{ID: window}, Nodes: []Node{{ID: window}}}
	if err := backend.observationChangesError(0, window, oldSnapshot, true); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("window-root removal escaped fresh snapshot consistency: %v", err)
	}
	if err := backend.publishSnapshot("window", window.Generation, 0, window, true, oldSnapshot); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("window-root removal published stale snapshot: %v", err)
	}
	if len(backend.cache) != 0 {
		t.Fatalf("window-root removal left %d stale snapshots cached", len(backend.cache))
	}
	replacement := backend.refID(objectRef{BusName: window.BusName, ObjectPath: dbus.ObjectPath(window.ObjectPath)})
	if replacement.Incarnation == window.Incarnation || snapshotKey(replacement, SnapshotOptions{}) == snapshotKey(window, SnapshotOptions{}) {
		t.Fatalf("same-path replacement reused snapshot identity: old=%+v new=%+v", window, replacement)
	}
}

func TestDefunctStateRevokesOnlyTheDefunctObject(t *testing.T) {
	parent := NodeID{BusName: "org.test", ObjectPath: "/parent", Generation: 8}
	target := NodeID{BusName: "org.test", ObjectPath: "/defunct", Generation: 8}
	backend := &dbusBackend{generation: 8}
	makeSignal := func(enabled int32) *dbus.Signal {
		return &dbus.Signal{
			Name:   "org.a11y.atspi.Event.Object:StateChanged",
			Sender: target.BusName,
			Path:   dbus.ObjectPath(target.ObjectPath),
			Body: []any{
				"defunct", enabled, int32(0), dbus.MakeVariant(int32(0)), map[string]dbus.Variant{},
			},
		}
	}
	backend.prepareEvent(makeSignal(0), signalEvent(makeSignal(0)))
	if err := backend.validateHandle(target); err != nil {
		t.Fatalf("defunct=false revoked object handle: %v", err)
	}
	backend.prepareEvent(makeSignal(1), signalEvent(makeSignal(1)))
	if err := backend.validateHandle(parent); err != nil {
		t.Fatalf("defunct state invalidated unrelated parent: %v", err)
	}
	if err := backend.validateHandle(target); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("defunct=true handle validation = %v, want ErrStaleNode", err)
	}
	malformed := makeSignal(1)
	malformed.Body = []any{"defunct", int32(1), int32(0), dbus.MakeVariant(int32(0))}
	before := backend.Generation()
	backend.prepareEvent(malformed, signalEvent(malformed))
	if backend.Generation() != before+1 {
		t.Fatalf("malformed defunct signal did not fail closed: generation=%d want %d", backend.Generation(), before+1)
	}
}

func TestCoalescedChildrenRemovalStillRevokesTarget(t *testing.T) {
	parent := NodeID{BusName: "org.test", ObjectPath: "/parent", Generation: 8}
	child := NodeID{BusName: "org.test", ObjectPath: "/child", Generation: 8}
	backend := &dbusBackend{generation: 8}
	newSignal := func() *dbus.Signal {
		return &dbus.Signal{
			Name:   "org.a11y.atspi.Event.Object:ChildrenChanged",
			Sender: parent.BusName,
			Path:   dbus.ObjectPath(parent.ObjectPath),
			Body: []any{
				"remove", int32(0), int32(0),
				dbus.MakeVariant([]any{child.BusName, dbus.ObjectPath(child.ObjectPath)}),
				map[string]dbus.Variant{},
			},
		}
	}
	lastKey := ""
	var lastAt time.Time
	base := time.Now()
	for i := 0; i < 2; i++ {
		sig := newSignal()
		event := backend.prepareEvent(sig, Event{Kind: sig.Name, Node: NodeID{BusName: sig.Sender, ObjectPath: string(sig.Path)}, Timestamp: base.Add(time.Duration(i) * time.Millisecond)})
		if coalesced := coalesceEvent(&lastKey, &lastAt, event); i == 0 && coalesced {
			t.Fatal("first child removal was coalesced")
		} else if i == 1 && !coalesced {
			t.Fatal("duplicate child removal was not coalesced for delivery")
		}
	}
	if backend.observationRevision != 2 {
		t.Fatalf("physical removal transitions=%d, want both signals processed", backend.observationRevision)
	}
	if err := backend.validateHandle(parent); err != nil {
		t.Fatalf("coalesced child removal invalidated parent: %v", err)
	}
	if err := backend.validateHandle(child); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("coalesced child removal left target usable: %v", err)
	}
}

func TestMalformedChildrenRemovalFailsClosed(t *testing.T) {
	backend := &dbusBackend{generation: 5}
	parent := NodeID{BusName: "org.test", ObjectPath: "/parent", Generation: 5}
	child := NodeID{BusName: "org.test", ObjectPath: "/child", Generation: 5}
	sig := &dbus.Signal{
		Name:   "org.a11y.atspi.Event.Object:ChildrenChanged",
		Sender: parent.BusName,
		Path:   dbus.ObjectPath(parent.ObjectPath),
		// The child reference is missing from the standard five-field event
		// body, so the backend cannot safely scope this removal.
		Body: []any{"remove", int32(0), int32(0)},
	}
	backend.prepareEvent(sig, signalEvent(sig))
	if backend.Generation() != 6 {
		t.Fatalf("malformed removal global epoch=%d, want fail-closed epoch 6", backend.Generation())
	}
	if err := backend.validateHandle(child); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("malformed removal left prior child usable: %v", err)
	}
}

func TestIncompleteAncestryTrackingFallsBackToGlobalInvalidation(t *testing.T) {
	backend := &dbusBackend{generation: 5, parentTrackingIncomplete: true}
	parent := NodeID{BusName: "org.test", ObjectPath: "/parent", Generation: 5}
	target := NodeID{BusName: "org.test", ObjectPath: "/child", Generation: 5}
	sig := &dbus.Signal{
		Name:   "org.a11y.atspi.Event.Object:ChildrenChanged",
		Sender: parent.BusName,
		Path:   dbus.ObjectPath(parent.ObjectPath),
		Body: []any{
			"remove", int32(0), int32(0),
			dbus.MakeVariant([]any{target.BusName, dbus.ObjectPath(target.ObjectPath)}),
			map[string]dbus.Variant{},
		},
	}
	backend.prepareEvent(sig, signalEvent(sig))
	if backend.Generation() != 6 {
		t.Fatalf("incomplete ancestry did not advance backend epoch: %d, want 6", backend.Generation())
	}
	if err := backend.validateHandle(parent); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("incomplete ancestry left old parent handle valid: %v", err)
	}
}

func TestIncarnationTrackingCapFailsClosedWithoutGrowing(t *testing.T) {
	backend := &dbusBackend{generation: 5, incarnations: make(map[objectIdentity]uint64, maxTrackedObjects)}
	for i := 0; i < maxTrackedObjects; i++ {
		backend.incarnations[objectIdentity{busName: "org.test", objectPath: fmt.Sprintf("/tracked/%d", i)}] = 1
	}
	backend.mu.Lock()
	parent := backend.nodeIDLocked(objectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/parent")})
	target := backend.nodeIDLocked(objectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/target")})
	trackedCount := len(backend.incarnations)
	trackingIncomplete := backend.parentTrackingIncomplete
	backend.mu.Unlock()
	if trackedCount != maxTrackedObjects || !trackingIncomplete {
		t.Fatalf("capped identity tracking = %d entries, incomplete=%t; want %d and true", trackedCount, trackingIncomplete, maxTrackedObjects)
	}
	sig := &dbus.Signal{
		Name:   "org.a11y.atspi.Event.Object:ChildrenChanged",
		Sender: parent.BusName,
		Path:   dbus.ObjectPath(parent.ObjectPath),
		Body: []any{
			"remove", int32(0), int32(0),
			dbus.MakeVariant([]any{target.BusName, dbus.ObjectPath(target.ObjectPath)}),
			map[string]dbus.Variant{},
		},
	}
	backend.prepareEvent(sig, Event{Kind: sig.Name, Node: parent})
	if backend.Generation() != 6 {
		t.Fatalf("incomplete capped ancestry did not advance handle epoch: %d, want 6", backend.Generation())
	}
	if err := backend.validateHandle(parent); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("incomplete capped ancestry left parent handle valid: %v", err)
	}
	if len(backend.incarnations) > maxTrackedObjects {
		t.Fatalf("incarnation tracking grew to %d entries beyond cap %d", len(backend.incarnations), maxTrackedObjects)
	}
}

func TestCacheSignalWirePayloadRetainsParentIdentity(t *testing.T) {
	payload := []any{
		[]any{"org.test", dbus.ObjectPath("/child")},
		[]any{"org.test", dbus.ObjectPath("/application")},
		[]any{"org.test", dbus.ObjectPath("/parent")},
		int32(3), int32(0), []string{"org.a11y.atspi.Accessible"}, "child", uint32(43), "", []uint32{8, 24},
	}
	item, ok := cacheItemFromSignal([]any{payload})
	if !ok {
		t.Fatal("AT-SPI Cache.AddAccessible wire payload was not decoded")
	}
	if item.Object.ObjectPath != "/child" || item.Parent.ObjectPath != "/parent" || item.Index != 3 {
		t.Fatalf("decoded cache addition lost object ancestry: %+v", item)
	}
	ref, ok := cacheObjectRefFromSignal([]any{payload[0]})
	if !ok || ref.ObjectPath != "/child" {
		t.Fatalf("AT-SPI cache object reference decode = %+v, %v", ref, ok)
	}
}

func TestMalformedCacheSignalForcesCacheReload(t *testing.T) {
	backend := &dbusBackend{
		generation: 5,
		cacheItems: map[NodeID]cacheItem{{BusName: "org.test", ObjectPath: "/node", Generation: 5}: {}},
		cacheApps:  map[string]bool{"org.test": true},
	}
	backend.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":AddAccessible", Body: []any{"unexpected-wire-shape"}})
	if backend.cacheItems != nil || backend.cacheApps != nil {
		t.Fatalf("malformed cache signal retained state: items=%v apps=%v", backend.cacheItems, backend.cacheApps)
	}
}

func TestCacheAcceptsProtocolParentReferences(t *testing.T) {
	tests := []struct {
		name   string
		parent []any
		role   uint32
		want   cacheObjectRef
	}{
		{
			name:   "application root with null parent",
			parent: []any{"", nullObjectPath},
			role:   75,
			want:   cacheObjectRef{ObjectPath: nullObjectPath},
		},
		{
			name:   "application root with desktop parent",
			parent: []any{registryName, desktopPath},
			role:   75,
			want:   cacheObjectRef{BusName: registryName, ObjectPath: desktopPath},
		},
		{
			name:   "embedded child with foreign parent",
			parent: []any{"org.parent.App", dbus.ObjectPath("/socket")},
			role:   78,
			want:   cacheObjectRef{BusName: "org.parent.App", ObjectPath: dbus.ObjectPath("/socket")},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := []any{
				[]any{"org.child.App", dbus.ObjectPath("/accessible/root")},
				[]any{"org.child.App", dbus.ObjectPath("/accessible/root")},
				test.parent,
				int32(0), int32(1), []string{"org.a11y.atspi.Accessible"}, "child", test.role, "", []uint32{},
			}
			item, ok := cacheItemFromSignal([]any{payload})
			if !ok {
				t.Fatal("valid protocol parent reference was rejected from a cache signal")
			}
			if item.Parent != test.want {
				t.Fatalf("decoded parent = %+v, want %+v", item.Parent, test.want)
			}
			if err := validateLoadedCacheItems("org.child.App", []cacheItem{item}); err != nil {
				t.Fatalf("valid protocol parent reference rejected from cache snapshot: %v", err)
			}
		})
	}
}

func TestNullParentCacheAdditionIsKnownObservation(t *testing.T) {
	item := cacheItem{
		Object:      cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/application")},
		Application: cacheObjectRef{BusName: "org.test", ObjectPath: dbus.ObjectPath("/application")},
		Parent:      cacheObjectRef{ObjectPath: nullObjectPath},
		Role:        75,
	}
	backend := &dbusBackend{
		generation: 5,
		cacheItems: make(map[NodeID]cacheItem),
		cacheApps:  map[string]bool{"org.test": true},
		parents:    make(map[objectIdentity]objectIdentity),
	}
	sig := &dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: "org.test", Body: []any{item}}
	backend.prepareEvent(sig, Event{Kind: sig.Name, Node: NodeID{BusName: "org.test", ObjectPath: "/application", Generation: 5}})
	if len(backend.observationHistory) != 1 || backend.observationHistory[0].unknown {
		t.Fatalf("null-parent cache addition observation = %+v, want a known event", backend.observationHistory)
	}
	if got, ok := backend.cacheItems[item.nodeIDAt(5)]; !ok || got.Parent != item.Parent {
		t.Fatalf("null-parent cache item = %+v, present %t; want original parent reference", got, ok)
	}
}

func TestSubtreeRevocationDoesNotCrossApplicationParentEdge(t *testing.T) {
	for _, useObservedParent := range []bool{false, true} {
		name := "cache ancestry"
		if useObservedParent {
			name = "observed ancestry"
		}
		t.Run(name, func(t *testing.T) {
			const generation = 5
			root := objectIdentity{busName: "org.parent.App", objectPath: "/embed-parent"}
			localChild := objectIdentity{busName: root.busName, objectPath: "/local-child"}
			foreignChild := objectIdentity{busName: "org.child.App", objectPath: "/plug"}
			childItem := cacheItem{
				Object:      cacheObjectRef{BusName: foreignChild.busName, ObjectPath: dbus.ObjectPath(foreignChild.objectPath)},
				Application: cacheObjectRef{BusName: foreignChild.busName, ObjectPath: dbus.ObjectPath("/application")},
				Parent:      cacheObjectRef{BusName: root.busName, ObjectPath: dbus.ObjectPath(root.objectPath)},
				Role:        78,
			}
			localID := NodeID{BusName: localChild.busName, ObjectPath: localChild.objectPath, Generation: generation, Incarnation: 1}
			foreignID := NodeID{BusName: foreignChild.busName, ObjectPath: foreignChild.objectPath, Generation: generation, Incarnation: 1}
			parents := map[objectIdentity]objectIdentity{localChild: root}
			if useObservedParent {
				parents[foreignChild] = root
			}
			backend := &dbusBackend{
				generation: generation,
				cacheItems: map[NodeID]cacheItem{childItem.nodeIDAt(generation): childItem},
				cacheApps:  map[string]bool{root.busName: true, foreignChild.busName: true},
				incarnations: map[objectIdentity]uint64{
					root: 1, localChild: 1, foreignChild: 1,
				},
				parents: parents,
			}

			if !backend.revokeObjectLocked(root) {
				t.Fatal("known same-application subtree revocation failed")
			}
			if backend.Generation() != generation {
				t.Fatalf("cross-application edge advanced global generation to %d", backend.Generation())
			}
			if err := backend.validateHandle(localID); !errors.Is(err, ErrStaleNode) {
				t.Fatalf("same-application child validation = %v, want ErrStaleNode", err)
			}
			if err := backend.validateHandle(foreignID); err != nil {
				t.Fatalf("foreign child was revoked with embedded parent: %v", err)
			}
			if _, ok := backend.parents[foreignChild]; ok {
				t.Fatal("foreign child retained an edge to the removed parent identity")
			}
			if backend.cacheApps[foreignChild.busName] {
				t.Fatal("foreign child's application cache remained complete after its parent was removed")
			}
			if _, ok := backend.cacheItems[foreignID]; ok {
				t.Fatal("foreign child's cache row retained the removed parent reference")
			}
			reusedRootID := backend.nodeIDLocked(objectRef{BusName: root.busName, ObjectPath: dbus.ObjectPath(root.objectPath)})
			if children := backend.cachedChildren(reusedRootID); len(children) != 0 {
				t.Fatalf("removed parent cache exposed foreign child to a reused path: %v", children)
			}
		})
	}
}

func TestNonCacheEventDiscardsPreviousGenerationCacheMetadata(t *testing.T) {
	backend := &dbusBackend{
		generation: 5,
		cacheItems: map[NodeID]cacheItem{},
		cacheApps:  map[string]bool{"org.test": true},
	}
	oldID := NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 5}
	backend.cacheItems[oldID] = cacheItem{
		Object: cacheObjectRef{BusName: oldID.BusName, ObjectPath: dbus.ObjectPath(oldID.ObjectPath)},
		Name:   "old name",
		States: []uint32{1},
	}
	if _, ok := backend.cachedItem(oldID); !ok {
		t.Fatal("fixture cache item was not available")
	}
	event := backend.prepareEvent(&dbus.Signal{Name: "org.a11y.atspi.Event.Object:PropertyChange"}, Event{Node: oldID})
	if event.Node.Generation != 5 || backend.observationRevision != 1 {
		t.Fatalf("property event handle epoch/observation = %d/%d, want 5/1", event.Node.Generation, backend.observationRevision)
	}
	if _, ok := backend.cachedItem(NodeID{BusName: oldID.BusName, ObjectPath: oldID.ObjectPath, Generation: 5}); ok {
		t.Fatal("stale cached name/state was reused after a non-cache event")
	}
	if backend.cacheApps[oldID.BusName] {
		t.Fatal("cache application remained marked fresh after non-cache invalidation")
	}
}

func TestCacheSignalAndExplicitInvalidationKeepOneCacheEpoch(t *testing.T) {
	oldID := NodeID{BusName: "org.test", ObjectPath: "/old", Generation: 10}
	backend := &dbusBackend{
		generation: 10,
		cacheItems: map[NodeID]cacheItem{
			oldID: {Object: cacheObjectRef{BusName: oldID.BusName, ObjectPath: dbus.ObjectPath(oldID.ObjectPath)}, Name: "old"},
		},
		cacheApps: map[string]bool{"org.test": true},
	}
	added := cacheItem{Object: cacheObjectRef{BusName: "org.test", ObjectPath: "/new"}}
	signal := &dbus.Signal{Name: cacheIface + ":AddAccessible", Body: []any{added}}
	start := make(chan struct{})
	finished := make(chan bool, 1)
	go func() {
		<-start
		_, active := backend.prepareEventTransition(signal, Event{Kind: signal.Name})
		finished <- active
	}()
	go func() {
		<-start
		backend.Invalidate(NodeID{})
		finished <- true
	}()
	close(start)
	for i := 0; i < 2; i++ {
		if !<-finished {
			t.Fatal("cache signal was discarded while backend remained active")
		}
	}

	backend.mu.RLock()
	defer backend.mu.RUnlock()
	if backend.generation != 11 {
		t.Fatalf("generation = %d, want explicit invalidation to advance the handle epoch once", backend.generation)
	}
	for id, item := range backend.cacheItems {
		if id.Generation != backend.generation {
			t.Fatalf("cache key handle epoch = %d, owner epoch = %d", id.Generation, backend.generation)
		}
		if item.Name == "old" || id.ObjectPath == oldID.ObjectPath {
			t.Fatalf("stale cache item resurfaced after invalidation: %+v", item)
		}
	}
}

func TestWatchSubscriberReturnsForNonCancelableContext(t *testing.T) {
	backend := &dbusBackend{subscribers: map[uint64]*eventSubscriber{}}
	done := make(chan struct{})
	go func() {
		backend.watchSubscriber(context.Background(), 1)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watchSubscriber blocked on context.Background")
	}
}

func TestEventAdmissionWaitsForDispatcherTeardown(t *testing.T) {
	oldDone := make(chan struct{})
	oldSubscriber := &eventSubscriber{out: make(chan Event, 1)}
	backend := &dbusBackend{
		subscribers: map[uint64]*eventSubscriber{1: oldSubscriber},
		eventCancel: func() {},
		eventDone:   oldDone,
	}

	backend.beginEventDispatcherCleanup(oldDone)
	select {
	case _, open := <-oldSubscriber.out:
		if open {
			t.Fatal("old subscriber stream remained open during dispatcher cleanup")
		}
	default:
		t.Fatal("old subscriber stream was not closed before remote cleanup")
	}

	out := make(chan Event, 1)
	id, start, wait, err := backend.reserveEventSubscriber(out)
	if err != nil {
		t.Fatalf("reserve while old dispatcher stops: %v", err)
	}
	if id != 0 || start || wait != oldDone || len(backend.subscribers) != 0 {
		t.Fatalf("stopping dispatcher admission = id %d, start %v, wait %v, subscribers %d; want wait for old run without insertion", id, start, wait, len(backend.subscribers))
	}

	backend.finishEventDispatcher(oldDone)
	if waitErr := waitForEventRun(context.Background(), wait); waitErr != nil {
		t.Fatalf("wait for old dispatcher completion: %v", waitErr)
	}
	id, start, wait, err = backend.reserveEventSubscriber(out)
	if err != nil {
		t.Fatalf("reserve after old dispatcher completion: %v", err)
	}
	if id == 0 || !start || wait != nil || len(backend.subscribers) != 1 {
		t.Fatalf("post-teardown admission = id %d, start %v, wait %v, subscribers %d; want one new run", id, start, wait, len(backend.subscribers))
	}
}

func TestEventAdmissionWaitCancellationDoesNotInsertSubscriber(t *testing.T) {
	done := make(chan struct{})
	backend := &dbusBackend{
		subscribers: map[uint64]*eventSubscriber{},
		eventCancel: func() {},
		eventDone:   done,
	}
	out := make(chan Event, 1)
	id, start, wait, err := backend.reserveEventSubscriber(out)
	if err != nil {
		t.Fatalf("reserve while dispatcher stops: %v", err)
	}
	if id != 0 || start || wait != done || len(backend.subscribers) != 0 {
		t.Fatalf("stopping dispatcher admission = id %d, start %v, wait %v, subscribers %d; want no insertion", id, start, wait, len(backend.subscribers))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForEventRun(ctx, wait); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission wait error = %v, want context.Canceled", err)
	}
	if len(backend.subscribers) != 0 {
		t.Fatalf("canceled admission inserted %d subscribers", len(backend.subscribers))
	}
}

func TestLastSubscriberCancellationGatesRestartUntilRunDone(t *testing.T) {
	runDone := make(chan struct{})
	dispatcherCanceled := make(chan struct{})
	subscriberCtx, cancelSubscriber := context.WithCancel(context.Background())
	backend := &dbusBackend{
		subscribers: map[uint64]*eventSubscriber{1: {out: make(chan Event, 1)}},
		eventCancel: func() { close(dispatcherCanceled) },
		eventDone:   runDone,
	}
	go backend.watchSubscriber(subscriberCtx, 1)
	cancelSubscriber()
	<-dispatcherCanceled

	out := make(chan Event, 1)
	id, start, wait, err := backend.reserveEventSubscriber(out)
	if err != nil {
		t.Fatalf("reserve after last subscriber cancellation: %v", err)
	}
	if id != 0 || start || wait != runDone || len(backend.subscribers) != 0 {
		t.Fatalf("last-subscriber restart admission = id %d, start %v, wait %v, subscribers %d; want wait for canceled dispatcher", id, start, wait, len(backend.subscribers))
	}

	backend.finishEventDispatcher(runDone)
	id, start, wait, err = backend.reserveEventSubscriber(out)
	if err != nil || id == 0 || !start || wait != nil {
		t.Fatalf("restart after dispatcher completion = id %d, start %v, wait %v, err %v; want fresh run", id, start, wait, err)
	}
}

func TestStaleSubscriberCancellationDoesNotStopNewSetup(t *testing.T) {
	subscriberCtx, cancelSubscriber := context.WithCancel(context.Background())
	setupCanceled := make(chan struct{})
	watcherDone := make(chan struct{})
	backend := &dbusBackend{
		subscribers:   map[uint64]*eventSubscriber{},
		eventStarting: &eventStart{done: make(chan struct{})},
		eventStartStop: func() {
			close(setupCanceled)
		},
	}
	go func() {
		backend.watchSubscriber(subscriberCtx, 1)
		close(watcherDone)
	}()
	cancelSubscriber()
	<-watcherDone
	select {
	case <-setupCanceled:
		t.Fatal("stale subscriber cancellation stopped a different event setup")
	default:
	}
}

func TestEventAdmissionWaitsForUnobservedSetupAndHonorsRetirement(t *testing.T) {
	state := &eventStart{done: make(chan struct{})}
	backend := &dbusBackend{subscribers: map[uint64]*eventSubscriber{}, eventStarting: state}
	out := make(chan Event, 1)
	id, start, wait, err := backend.reserveEventSubscriber(out)
	if err != nil {
		t.Fatalf("reserve while event setup is in progress: %v", err)
	}
	if id != 0 || start || wait != state.done || len(backend.subscribers) != 0 {
		t.Fatalf("setup-in-progress admission = id %d, start %v, wait %v, subscribers %d; want wait without insertion", id, start, wait, len(backend.subscribers))
	}
	backend.finishEventStart(state, context.Canceled, true)
	if waitErr := waitForEventRun(context.Background(), wait); waitErr != nil {
		t.Fatalf("wait for setup completion: %v", waitErr)
	}
	id, start, wait, err = backend.reserveEventSubscriber(out)
	if err != nil || id == 0 || !start || wait != nil {
		t.Fatalf("admission after canceled setup = id %d, start %v, wait %v, err %v; want fresh setup", id, start, wait, err)
	}

	retired := &dbusBackend{subscribers: map[uint64]*eventSubscriber{}, eventsRetired: true}
	if _, _, _, err := retired.reserveEventSubscriber(make(chan Event, 1)); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("retired backend admission error = %v, want ErrDisconnected", err)
	}
}

// The child index is keyed only by bus name and object path, so it has to record
// the generation it was built from. Without that, a reload that produced a new
// generation left the previous generation's index in place and every parent lookup
// returned the old children.
func TestCachedChildrenRebuildsWhenTheGenerationAdvances(t *testing.T) {
	const busName = "org.test.App"
	application := cacheObjectRef{BusName: busName, ObjectPath: "/application"}
	rootItem := cacheItem{Object: cacheObjectRef{BusName: busName, ObjectPath: "/root"}, Application: application, Parent: application, ChildCount: 1}
	oldChild := cacheItem{Object: cacheObjectRef{BusName: busName, ObjectPath: "/old"}, Application: application, Parent: cacheObjectRef{BusName: busName, ObjectPath: "/root"}}

	backend := &dbusBackend{
		generation: 1,
		cacheItems: map[NodeID]cacheItem{rootItem.nodeIDAt(1): rootItem, oldChild.nodeIDAt(1): oldChild},
		cacheApps:  map[string]bool{busName: true},
	}

	first := NodeID{BusName: busName, ObjectPath: "/root", Generation: 1}
	if got := backend.cachedChildren(first); len(got) != 1 || got[0].ObjectPath != "/old" {
		t.Fatalf("children at generation 1 = %+v, want /old", got)
	}

	// A new generation arrives with different children. The index is left in place
	// deliberately: nothing about a reload is required to clear it.
	newChild := cacheItem{Object: cacheObjectRef{BusName: busName, ObjectPath: "/new"}, Application: application, Parent: cacheObjectRef{BusName: busName, ObjectPath: "/root"}}
	backend.mu.Lock()
	backend.generation = 2
	backend.cacheItems = map[NodeID]cacheItem{
		rootItem.nodeIDAt(2): rootItem,
		newChild.nodeIDAt(2): newChild,
	}
	stillCached := backend.cacheChildrenIndex != nil
	backend.mu.Unlock()
	if !stillCached {
		t.Fatal("test needs the index to survive the reload; this fixture cleared it")
	}

	second := NodeID{BusName: busName, ObjectPath: "/root", Generation: 2}
	if got := backend.cachedChildren(second); len(got) != 1 || got[0].ObjectPath != "/new" {
		t.Fatalf("children at generation 2 = %+v, want /new from the new generation", got)
	}
}

func TestAdversarialSnapshotBudgetIsEnforced(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 1}
	nodes := make([]Node, 0, 8)
	for i := 0; i < 8; i++ {
		nodes = append(nodes, Node{ID: NodeID{BusName: "org.test", ObjectPath: "/n", Generation: 1}, Name: strings.Repeat("x", 4096)})
	}
	snapshot := Snapshot{Root: Node{ID: root}, Nodes: nodes, Generation: 1}
	bounded, err := boundSnapshotResponse(snapshot, 1024)
	if err != nil {
		if !errors.Is(err, ErrResponseBudget) {
			t.Fatalf("budget error = %v", err)
		}
		return
	}
	if snapshotJSONSize(bounded) > 1024 {
		t.Fatalf("bounded snapshot exceeds budget: %d > 1024", snapshotJSONSize(bounded))
	}
}

func TestAdversarialCloneSnapshotIsolatesCache(t *testing.T) {
	root := NodeID{BusName: "b", ObjectPath: "/root", Generation: 3}
	original := Snapshot{
		Root:       Node{ID: root, Name: "root", Attributes: map[string]string{"class": "field"}},
		Nodes:      []Node{{ID: root, Name: "root", Attributes: map[string]string{"class": "field"}, Children: []NodeID{root}}},
		Generation: 3,
	}
	cloned := cloneSnapshot(original)
	cloned.Nodes[0].Name = "mutated"
	cloned.Nodes[0].Attributes["class"] = "mutated"
	cloned.Nodes[0].Children[0] = NodeID{}
	if original.Nodes[0].Name == "mutated" || original.Nodes[0].Attributes["class"] == "mutated" || original.Nodes[0].Children[0] == (NodeID{}) {
		t.Fatal("cloneSnapshot shares mutable state with cached snapshot")
	}
}

func TestAdversarialShortAccessibleNameMustNotCorrelate(t *testing.T) {
	target := WindowTarget{ID: "w", Title: "Editor - Preferences", PID: 7}
	node := Node{Name: "Edit", Role: "frame", Bounds: Rect{X: 0, Y: 0, Width: 100, Height: 100}, HasBounds: true, Showing: true}
	if score := windowCandidateScore(node, target); score != 0 {
		t.Fatalf("generic 4-char accessible name correlated with unrelated title: score=%d", score)
	}
}

func TestAdversarialPrefixSiblingsAreAmbiguous(t *testing.T) {
	target := WindowTarget{ID: "w", Title: "Terminal", PID: 9}
	first := Node{ID: NodeID{BusName: "b", ObjectPath: "/a", Generation: 1}, Role: "frame", Name: "Terminal - a"}
	second := Node{ID: NodeID{BusName: "b", ObjectPath: "/b", Generation: 1}, Role: "frame", Name: "Terminal - b"}
	firstScore, secondScore := windowCandidateScore(first, target), windowCandidateScore(second, target)
	if firstScore == 0 || secondScore == 0 {
		t.Fatalf("prefix siblings discarded: %d vs %d", firstScore, secondScore)
	}
	if firstScore != secondScore {
		t.Fatalf("prefix siblings scored differently: %d vs %d, ambiguity would be hidden", firstScore, secondScore)
	}
	selected, _, ambiguous := chooseWindowCandidate([]windowCandidate{{node: first, score: firstScore}, {node: second, score: secondScore}})
	if selected != nil || !ambiguous {
		t.Fatalf("prefix siblings did not report ambiguity: selected=%+v ambiguous=%v", selected, ambiguous)
	}
}

func TestAdversarialStalePublicationNeverCaches(t *testing.T) {
	backend := &dbusBackend{generation: 8}
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 8}
	snapshot := Snapshot{Root: Node{ID: root}, Nodes: []Node{{ID: root}}, Generation: 8}
	backend.Invalidate(root)
	if err := backend.publishSnapshot("k", root.Generation, 0, root, true, snapshot); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale publish = %v, want ErrStaleGeneration", err)
	}
	if len(backend.cache) != 0 {
		t.Fatal("stale snapshot entered cache")
	}
}

func TestAdversarialOperationsAreSingleSource(t *testing.T) {
	ops := (&dbusBackend{}).SupportedOperations()
	caps := capability.Operations("accessibility")
	if len(ops) != len(caps) {
		t.Fatalf("backend ops %d != capability ops %d", len(ops), len(caps))
	}
	for i := range ops {
		if ops[i] != caps[i] {
			t.Fatalf("op %d: backend %q != capability %q", i, ops[i], caps[i])
		}
	}
	seen := map[string]bool{}
	for _, op := range ops {
		if op == "" {
			t.Fatal("empty operation name")
		}
		if seen[op] {
			t.Fatalf("duplicate operation %q", op)
		}
		seen[op] = true
	}
	for _, required := range []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events", "outline", "window-root", "reopen"} {
		if !seen[required] {
			t.Fatalf("operations missing %q: %v", required, ops)
		}
	}
}

// Best-effort application discovery must clamp its desktop work budget before
// contacting any provider. Repeated references beyond the limit must not
// cause unbounded application enumeration or produce more than the limit.
func TestApplicationsCapsOversizedPrivateATSPIDesktop(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
		original := root.children[0]
		root.children = make([]objectRef, maxApplications+1)
		for i := range root.children {
			root.children[i] = original
		}
	})
	applications, err := backend.Applications(context.Background())
	if err != nil {
		t.Fatalf("bounded enumeration failed: %v", err)
	}
	if len(applications) > maxApplications {
		t.Fatalf("unbounded application enumeration: %d > %d", len(applications), maxApplications)
	}
}

// A missing application identity or a disconnected accessibility bus cannot
// establish process ownership. In both cases, no PID may be invented from
// stale state or an unrelated process.
func TestApplicationConnectionPIDMissingIdentityAndTransportDoNotInventOwner(t *testing.T) {
	var missing *dbusBackend
	if pid, err := missing.connectionPID(context.Background(), "org.example.Application"); err != nil || pid != 0 {
		t.Fatalf("nil accessibility backend invented process ownership: pid=%d err=%v", pid, err)
	}
	disconnected := &dbusBackend{}
	for _, bus := range []string{"", "   ", "org.example.Application"} {
		if pid, err := disconnected.connectionPID(context.Background(), bus); err != nil || pid != 0 {
			t.Fatalf("unavailable bus %q invented process ownership: pid=%d err=%v", bus, pid, err)
		}
	}
	live, _, _ := newSyntheticATSPIBackend(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if pid, err := live.connectionPID(ctx, registryName); pid != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled PID lookup returned stale application owner: pid=%d err=%v", pid, err)
	}
}

// Fresh application discovery is a strict identity read, not a best-effort
// display list. A truncated desktop can hide a second matching application.
func TestPrivateATSPIApplicationFreshRefusesOversizedDesktopEnumeration(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
		original := root.children[0]
		root.children = make([]objectRef, maxApplications+1)
		for i := range root.children {
			root.children[i] = original
		}
	})
	app, err := backend.FindApplicationFresh(context.Background(), ApplicationFilter{Name: "Editor"})
	if !errors.Is(err, ErrIncompleteSnapshot) || app.ID.valid() || app.Name != "" {
		t.Fatalf("partial desktop disclosed a matching app: %+v %v", app, err)
	}
}

// An unowned or vanished desktop child cannot satisfy a requested PID; the
// system bus's identity lookup must succeed rather than being assumed.
func TestPrivateATSPIApplicationFreshDoesNotIgnoreUnverifiedPID(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
		root.children = []objectRef{{BusName: ":1.999999", ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/ghost")}}
	})
	app, err := backend.FindApplicationFresh(context.Background(), ApplicationFilter{PID: 424242})
	if err == nil || !strings.Contains(err.Error(), "resolve application PID") || app.ID.valid() {
		t.Fatalf("unverified PID became an application identity: %+v %v", app, err)
	}
}

func TestPrivateATSPIApplicationFreshRejectsNilContextWithoutPublishingIdentity(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackend(t, false)
	var ctx context.Context
	app, err := backend.FindApplicationFresh(ctx, ApplicationFilter{Name: "Editor"})
	if err == nil || !strings.Contains(err.Error(), "nil context") || app.ID.valid() {
		t.Fatalf("nil context returned a live application: %+v %v", app, err)
	}
}

// A child whose property service has vanished cannot be silently skipped as
// a non-match. Doing so would turn an incomplete enumeration into certainty.
func TestPrivateATSPIApplicationFreshRequiresAuthoritativeNameRead(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
		root.children = []objectRef{{BusName: ":1.999999", ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/ghost")}}
	})
	app, err := backend.FindApplicationFresh(context.Background(), ApplicationFilter{Name: "Editor"})
	if err == nil || !strings.Contains(err.Error(), "read application name") || app.ID.valid() {
		t.Fatalf("unreadable name treated as a known non-match: %+v %v", app, err)
	}
}

// Fresh application identity must be consistent with one AT-SPI generation.
// When the desktop changes epochs during child enumeration, a single change
// may be retried, but repeated changes cannot release any application.
func TestFreshApplicationLookupBoundsDesktopEpochRetries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		invalidations int
		wantErr       bool
	}{
		{name: "one invalidation retries", invalidations: 1},
		{name: "repeated invalidation fails closed", invalidations: 2, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var live atomic.Pointer[dbusBackend]
			var reads atomic.Int32
			backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
				root.childrenHook = func() {
					n := reads.Add(1)
					if b := live.Load(); b != nil && n <= int32(tc.invalidations) {
						b.Invalidate(NodeID{})
					}
				}
			})
			live.Store(backend)
			app, err := backend.FindApplicationFresh(context.Background(), ApplicationFilter{Name: "Editor"})
			if tc.wantErr {
				if err == nil || app.ID.valid() || !errors.Is(err, ErrStaleGeneration) {
					t.Fatalf("repeated epoch changes exposed app=%+v err=%v", app, err)
				}
			} else if err != nil || app.Name != "Editor" || app.ID.Generation != backend.Generation() {
				t.Fatalf("retry did not return current application: app=%+v err=%v generation=%d", app, err, backend.Generation())
			}
			if got := reads.Load(); got != 2 {
				t.Fatalf("provider reads=%d, want 2 bounded attempts", got)
			}
		})
	}
}

// An event-history rollover while the provider is enumerating applications
// makes a successful identity read unverifiable, even if the handle generation
// did not change. Retry once; never publish from two incomplete observations.
func TestFreshApplicationLookupRejectsEvictedObservationHistory(t *testing.T) {
	for _, tc := range []struct {
		name       string
		invalidate int
		wantError  bool
	}{
		{name: "single observation rollover retries", invalidate: 1},
		{name: "repeated observation rollover fails closed", invalidate: 2, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var live atomic.Pointer[dbusBackend]
			var reads atomic.Int32
			b, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
				root.childrenHook = func() {
					count := reads.Add(1)
					if backend := live.Load(); backend != nil && count <= int32(tc.invalidate) {
						backend.mu.Lock()
						backend.observationRevision++
						backend.observationFloor = backend.observationRevision
						backend.mu.Unlock()
					}
				}
			})
			live.Store(b)
			app, err := b.FindApplicationFresh(context.Background(), ApplicationFilter{Name: "Editor"})
			if tc.wantError {
				if !errors.Is(err, ErrObservationChanged) || app.ID.valid() {
					t.Fatalf("incomplete history published app=%+v err=%v", app, err)
				}
			} else if err != nil || app.Name != "Editor" || app.ID.Generation != b.Generation() {
				t.Fatalf("valid retry failed: app=%+v err=%v", app, err)
			}
			if got := reads.Load(); got != 2 {
				t.Fatalf("provider reads=%d, want two bounded reads", got)
			}
		})
	}
}

// A matching application cannot become an authorized fresh scope if the
// provider withdraws its name or window-title identity while it is read.
// Use an isolated AT-SPI provider with a directly exposed application node.
func TestFreshApplicationLookupRejectsUnavailableProviderIdentity(t *testing.T) {
	for _, key := range []string{"Name", "Description"} {
		t.Run(key, func(t *testing.T) {
			b, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, frame *syntheticATSPINode) {
				root.children = []objectRef{{BusName: registryName, ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/editor")}}
				frame.propertyFailure = key
			})
			filter := ApplicationFilter{Name: "Editor", WindowTitle: "document"}
			app, err := b.FindApplicationFresh(context.Background(), filter)
			if err == nil || app.ID.valid() || !strings.Contains(err.Error(), "read application ") {
				t.Fatalf("missing %s authorized an application: app=%+v err=%v", key, app, err)
			}
		})
	}
}

type invalidatingApplicationPropertyNode struct {
	*syntheticATSPINode
	beforeProperty func(string)
}

func (n *invalidatingApplicationPropertyNode) Get(iface, key string) (dbus.Variant, *dbus.Error) {
	if n.beforeProperty != nil {
		n.beforeProperty(key)
	}
	return n.syntheticATSPINode.Get(iface, key)
}

func newGenerationChangingApplication(t *testing.T, property string) (*dbusBackend, NodeID, *atomic.Int32) {
	t.Helper()
	ctx := context.Background()
	address := startTestDBus(t)
	registry, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	client, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	reply, err := registry.RequestName(registryName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request AT-SPI registry name: reply=%v err=%v", reply, err)
	}

	const appPath = dbus.ObjectPath("/org/a11y/atspi/accessible/app")
	appRef := objectRef{BusName: registryName, ObjectPath: appPath}
	root := &syntheticATSPINode{children: []objectRef{appRef}}
	var liveBackend atomic.Pointer[dbusBackend]
	propertyReads := &atomic.Int32{}
	appNode := &invalidatingApplicationPropertyNode{
		syntheticATSPINode: &syntheticATSPINode{name: "Editor", role: 68, application: appRef},
		beforeProperty: func(key string) {
			if key == property && propertyReads.Add(1) == 1 {
				if backend := liveBackend.Load(); backend != nil {
					backend.Invalidate(NodeID{})
				}
			}
		},
	}
	if err := registry.Export(root, desktopPath, accessibleIface); err != nil {
		t.Fatal(err)
	}
	if err := registry.Export(appNode, appPath, accessibleIface); err != nil {
		t.Fatal(err)
	}
	if err := registry.Export(appNode, appPath, propertiesIface); err != nil {
		t.Fatal(err)
	}
	backend := &dbusBackend{
		generation:   1,
		access:       client,
		cache:        make(map[string]cachedSnapshot),
		cacheItems:   make(map[NodeID]cacheItem),
		cacheApps:    make(map[string]bool),
		incarnations: make(map[objectIdentity]uint64),
		parents:      make(map[objectIdentity]objectIdentity),
		toolkits:     make(map[string]string),
	}
	liveBackend.Store(backend)
	return backend, backend.refID(appRef), propertyReads
}

func TestApplicationsRetriesWhenNodeReadChangesGeneration(t *testing.T) {
	backend, _, invalidations := newGenerationChangingApplication(t, "Name")
	applications, err := backend.Applications(context.Background())
	if err != nil {
		t.Fatalf("enumerate applications after stale node read: %v", err)
	}
	if len(applications) != 1 || applications[0].Name != "Editor" || applications[0].ID.Generation != 2 {
		t.Fatalf("application from stale generation escaped enumeration: applications=%+v generation=%d", applications, backend.Generation())
	}
	if got := invalidations.Load(); got != 2 {
		t.Fatalf("application name reads=%d, want one stale observation and one current-generation retry", got)
	}
}

func TestToolkitIdentityRejectsPropertyReplyFromStaleGeneration(t *testing.T) {
	backend, id, invalidations := newGenerationChangingApplication(t, "ToolkitName")
	toolkit, err := backend.toolkitForNode(context.Background(), id)
	if !errors.Is(err, ErrStaleGeneration) || toolkit != "" {
		t.Fatalf("toolkit from stale generation was accepted: toolkit=%q err=%v", toolkit, err)
	}
	if got := invalidations.Load(); got != 1 || backend.Generation() != 2 {
		t.Fatalf("toolkit property reads=%d generation=%d, want one read and generation 2", got, backend.Generation())
	}
	if _, ok := backend.toolkits[id.BusName]; ok {
		t.Fatal("stale toolkit identity entered the generation-scoped cache")
	}
}

// An AT-SPI application selector is not allowed to publish an identity or
// treat a disappeared provider as a no-match result. Cached enumeration,
// fresh enumeration, and toolkit lookup all fail closed without a live bus.
func TestApplicationSelectionRejectsDisconnectedProviderWithoutIdentity(t *testing.T) {
	const generation = 31
	b := &dbusBackend{generation: generation}
	id := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/application", Generation: generation}
	filter := ApplicationFilter{Name: "Editor", Bus: id.BusName}
	for _, tc := range []struct {
		name string
		run  func() (Application, error)
	}{
		{"cached", func() (Application, error) { return b.FindApplication(context.Background(), filter) }},
		{"fresh", func() (Application, error) { return b.FindApplicationFresh(context.Background(), filter) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := tc.run()
			if !errors.Is(err, ErrDisconnected) || app.Node.ID != (NodeID{}) || app.Name != "" {
				t.Fatalf("disconnected identity disclosed or misreported: %+v %v", app, err)
			}
		})
	}
	if toolkit, err := b.toolkitForNode(context.Background(), id); !errors.Is(err, ErrDisconnected) || toolkit != "" {
		t.Fatalf("disconnected provider toolkit trusted: %q %v", toolkit, err)
	}
}

type cancelAtAccessibilityContextRead struct {
	context.Context
	cancel    context.CancelFunc
	threshold int32
	calls     atomic.Int32
}

func (c *cancelAtAccessibilityContextRead) Err() error {
	if c.calls.Add(1) == c.threshold {
		c.cancel()
	}
	return c.Context.Err()
}

// Run cancellation at each observed admission and provider boundary rather
// than only before the first call. No private app data may escape a canceled
// AT-SPI enumeration, even if optional provider properties fail after cancel.
func TestATSPISyntheticApplicationsCancellationBoundaryMatrix(t *testing.T) {
	b, _, _ := newSyntheticATSPIBackend(t, false)
	normalBase, normalCancel := context.WithCancel(context.Background())
	baseline := &cancelAtAccessibilityContextRead{Context: normalBase, cancel: normalCancel, threshold: 10000}
	apps, err := b.Applications(baseline)
	normalCancel()
	if err != nil || len(apps) != 1 || apps[0].Name != "Editor" {
		t.Fatalf("real AT-SPI application baseline=%+v err=%v", apps, err)
	}
	checks := baseline.calls.Load()
	t.Logf("AT-SPI application enumeration cancellation checks=%d", checks)
	if checks < 2 {
		t.Fatalf("application enumeration lacks cancellation checks: %d", checks)
	}
	for index := int32(1); index <= checks; index++ {
		t.Run("cancellation-read-"+stringInt32(index), func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelAtAccessibilityContextRead{Context: base, cancel: cancel, threshold: index}
			result, err := b.Applications(ctx)
			if base.Err() == nil {
				t.Skipf("cached path used fewer checks: %d", ctx.calls.Load())
			}
			if len(result) != 0 || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled AT-SPI enumeration returned private applications=%+v err=%v (cancel after Err read %d, total %d)", result, err, index, ctx.calls.Load())
			}
		})
	}
}
func stringInt32(n int32) string { return strconv.FormatInt(int64(n), 10) }

func TestATSPISyntheticFreshApplicationCancellationBoundaryMatrix(t *testing.T) {
	b, _, _ := newSyntheticATSPIBackend(t, false)
	filter := ApplicationFilter{Name: "Editor"}
	normalBase, normalCancel := context.WithCancel(context.Background())
	baseline := &cancelAtAccessibilityContextRead{Context: normalBase, cancel: normalCancel, threshold: 10000}
	app, err := b.FindApplicationFresh(baseline, filter)
	normalCancel()
	if err != nil || app.Name != "Editor" {
		t.Fatalf("fresh private bus app baseline=%+v err=%v", app, err)
	}
	checks := baseline.calls.Load()
	t.Logf("AT-SPI fresh application checks=%d", checks)
	for index := int32(1); index <= checks; index++ {
		t.Run("fresh-cancel-"+stringInt32(index), func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelAtAccessibilityContextRead{Context: base, cancel: cancel, threshold: index}
			result, err := b.FindApplicationFresh(ctx, filter)
			if base.Err() == nil {
				t.Skipf("lookup read fewer contexts (%d)", ctx.calls.Load())
			}
			if !errors.Is(err, context.Canceled) || result.Name != "" {
				t.Fatalf("fresh application lookup returned private identity=%q on cancellation: %v (cancel at Err %d, total %d)", result.Name, err, index, ctx.calls.Load())
			}
		})
	}
}

func TestApplicationsDiscardsCacheResponseFromStaleGeneration(t *testing.T) {
	ctx := context.Background()
	address := startTestDBus(t)
	registry, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	cacheService, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cacheService.Close() })
	client, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	for _, request := range []struct {
		connection *dbus.Conn
		name       string
	}{
		{connection: registry, name: registryName},
		{connection: cacheService, name: syntheticCacheService},
	} {
		reply, requestErr := request.connection.RequestName(request.name, dbus.NameFlagDoNotQueue)
		if requestErr != nil || reply != dbus.RequestNameReplyPrimaryOwner {
			t.Fatalf("request D-Bus name %s: reply=%v err=%v", request.name, reply, requestErr)
		}
	}

	const appPath = dbus.ObjectPath("/org/a11y/atspi/accessible/app")
	appRef := objectRef{BusName: syntheticCacheService, ObjectPath: appPath}
	root := &syntheticATSPINode{children: []objectRef{appRef}}
	appNode := &syntheticATSPINode{name: "Editor", role: 68}
	if err := registry.Export(root, desktopPath, accessibleIface); err != nil {
		t.Fatal(err)
	}
	if err := cacheService.Export(appNode, appPath, accessibleIface); err != nil {
		t.Fatal(err)
	}
	if err := cacheService.Export(appNode, appPath, propertiesIface); err != nil {
		t.Fatal(err)
	}
	item := cacheItem{
		Object:      cacheObjectRef{BusName: syntheticCacheService, ObjectPath: appPath},
		Application: cacheObjectRef{BusName: syntheticCacheService, ObjectPath: appPath},
		Parent:      cacheObjectRef{ObjectPath: nullObjectPath},
		Name:        "Editor",
		Role:        68,
	}
	provider := &syntheticCacheProvider{items: []cacheItem{item}}
	var cacheReads atomic.Int32
	var liveBackend atomic.Pointer[dbusBackend]
	provider.beforeReturn = func() {
		if cacheReads.Add(1) == 1 {
			liveBackend.Load().Invalidate(NodeID{})
		}
	}
	if err := cacheService.Export(provider, cachePath, cacheIface); err != nil {
		t.Fatal(err)
	}
	backend := &dbusBackend{
		generation:   1,
		access:       client,
		cache:        make(map[string]cachedSnapshot),
		cacheItems:   make(map[NodeID]cacheItem),
		cacheApps:    make(map[string]bool),
		incarnations: make(map[objectIdentity]uint64),
		parents:      make(map[objectIdentity]objectIdentity),
		toolkits:     make(map[string]string),
	}
	liveBackend.Store(backend)

	applications, err := backend.Applications(ctx)
	if err != nil {
		t.Fatalf("enumerate applications after stale cache response: %v", err)
	}
	if len(applications) != 1 || applications[0].Name != "Editor" || applications[0].ID.Generation != 2 {
		t.Fatalf("stale cache response escaped enumeration: applications=%+v generation=%d", applications, backend.Generation())
	}
	if reads := cacheReads.Load(); reads != 2 {
		t.Fatalf("Cache.GetItems calls=%d, want one discarded response and one current-generation retry", reads)
	}
	if !backend.cacheApps[syntheticCacheService] {
		t.Fatal("current-generation cache response was not published")
	}
}

// Application enumeration must discard a child list observed across a
// generation change, retry once against the current desktop, and bound repeats.
func TestApplicationsRetriesOneStaleDesktopGenerationAndBoundsRepeat(t *testing.T) {
	for _, tc := range []struct {
		name             string
		invalidations    int32
		wantGeneration   uint64
		wantApplications int
		wantStaleError   bool
	}{
		{name: "one generation change retries", invalidations: 1, wantGeneration: 2, wantApplications: 1},
		{name: "repeated changes exhaust retry", invalidations: 2, wantGeneration: 3, wantStaleError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var live atomic.Pointer[dbusBackend]
			var active atomic.Bool
			var reads atomic.Int32
			var invalidations atomic.Int32
			invalidations.Store(tc.invalidations)
			backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) {
				root.childrenHook = func() {
					if !active.Load() {
						return
					}
					if reads.Add(1) <= invalidations.Load() {
						if backend := live.Load(); backend != nil {
							backend.Invalidate(NodeID{})
						}
					}
				}
			})
			live.Store(backend)
			active.Store(true)

			applications, err := backend.Applications(context.Background())
			if tc.wantStaleError {
				if !errors.Is(err, ErrStaleGeneration) || len(applications) != 0 {
					t.Fatalf("repeated generation changes returned apps=%+v err=%v, want bounded stale failure", applications, err)
				}
			} else if err != nil || len(applications) != tc.wantApplications || applications[0].ID.Generation != tc.wantGeneration {
				t.Fatalf("retried enumeration apps=%+v err=%v, want generation %d", applications, err, tc.wantGeneration)
			}
			if reads.Load() != 2 || backend.Generation() != tc.wantGeneration {
				t.Fatalf("enumeration reads=%d generation=%d, want two reads and generation %d", reads.Load(), backend.Generation(), tc.wantGeneration)
			}
		})
	}
}

type syntheticATSPINode struct {
	name                string
	description         string
	role                uint32
	states              []uint32
	text                string
	attrs               map[string]string
	interfaces          []string
	children            []objectRef
	childrenHook        func()
	childrenFailure     string
	interfacesFailure   string
	propertyFailure     string
	attributesFailure   string
	attributesHook      func()
	roleFailure         string
	stateFailure        string
	application         objectRef
	pointResult         objectRef
	pointFailure        string
	x, y, width, height int32
}

func (n *syntheticATSPINode) GetRole() (uint32, *dbus.Error) {
	if n.roleFailure != "" {
		return 0, dbus.MakeFailedError(errors.New(n.roleFailure))
	}
	return n.role, nil
}
func (n *syntheticATSPINode) GetInterfaces() ([]string, *dbus.Error) {
	if n.interfacesFailure != "" {
		return nil, dbus.MakeFailedError(errors.New(n.interfacesFailure))
	}
	return n.interfaces, nil
}
func (n *syntheticATSPINode) GetState() ([]uint32, *dbus.Error) {
	if n.stateFailure != "" {
		return nil, dbus.MakeFailedError(errors.New(n.stateFailure))
	}
	return n.states, nil
}
func (n *syntheticATSPINode) GetAttributes() (map[string]string, *dbus.Error) {
	if n.attributesHook != nil {
		n.attributesHook()
	}
	if n.attributesFailure != "" {
		return nil, dbus.MakeFailedError(errors.New(n.attributesFailure))
	}
	return n.attrs, nil
}
func (n *syntheticATSPINode) GetChildren() ([]objectRef, *dbus.Error) {
	if n.childrenFailure != "" {
		return nil, dbus.MakeFailedError(errors.New(n.childrenFailure))
	}
	children := append([]objectRef(nil), n.children...)
	if n.childrenHook != nil {
		n.childrenHook()
	}
	return children, nil
}
func (n *syntheticATSPINode) GetApplication() (objectRef, *dbus.Error) { return n.application, nil }
func (n *syntheticATSPINode) GetAccessibleAtPoint(_, _ int32, _ uint32) (objectRef, *dbus.Error) {
	if n.pointFailure != "" {
		return objectRef{}, dbus.MakeFailedError(errors.New(n.pointFailure))
	}
	if n.pointResult.null() {
		return objectRef{BusName: registryName, ObjectPath: nullObjectPath}, nil
	}
	return n.pointResult, nil
}
func (n *syntheticATSPINode) GetText(start, end int32) (string, *dbus.Error) {
	if start < 0 || end < start || end > int32(len(n.text)) {
		return "", dbus.MakeFailedError(errors.New("invalid text range"))
	}
	return n.text[start:end], nil
}
func (n *syntheticATSPINode) GetExtents(_ uint32) (struct{ X, Y, Width, Height int32 }, *dbus.Error) {
	return struct{ X, Y, Width, Height int32 }{n.x, n.y, n.width, n.height}, nil
}
func (n *syntheticATSPINode) GetRelationSet() ([]struct {
	RelationType uint32
	Targets      []objectRef
}, *dbus.Error) {
	return []struct {
		RelationType uint32
		Targets      []objectRef
	}{}, nil
}
func (n *syntheticATSPINode) Get(iface, key string) (dbus.Variant, *dbus.Error) {
	if n.propertyFailure == key {
		return dbus.Variant{}, dbus.MakeFailedError(errors.New("provider refused " + key))
	}
	switch key {
	case "Name":
		return dbus.MakeVariant(n.name), nil
	case "Description":
		return dbus.MakeVariant(n.description), nil
	case "ChildCount":
		return dbus.MakeVariant(int32(len(n.children))), nil
	case "CharacterCount":
		return dbus.MakeVariant(int32(len(n.text))), nil
	case "ToolkitName":
		return dbus.MakeVariant("GTK"), nil
	default:
		return dbus.Variant{}, dbus.MakeFailedError(errors.New("no such property " + iface + "." + key))
	}
}

func newSyntheticATSPIBackend(t *testing.T, protected bool) (*dbusBackend, NodeID, WindowTarget) {
	return newSyntheticATSPIBackendWithFrameSetup(t, protected, nil)
}

func newSyntheticATSPIBackendWithFrameSetup(t *testing.T, protected bool, setup func(*syntheticATSPINode, *syntheticATSPINode)) (*dbusBackend, NodeID, WindowTarget) {
	t.Helper()
	address := startTestDBus(t)
	ctx := context.Background()
	service, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(registryName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register private AT-SPI provider: %v %v", reply, err)
	}
	const appPath = dbus.ObjectPath("/org/a11y/atspi/accessible/app")
	const framePath = dbus.ObjectPath("/org/a11y/atspi/accessible/editor")
	appRef := objectRef{BusName: registryName, ObjectPath: appPath}
	frameRef := objectRef{BusName: registryName, ObjectPath: framePath}
	root := &syntheticATSPINode{name: "Desktop", children: []objectRef{appRef}, role: 23}
	app := &syntheticATSPINode{name: "Editor", children: []objectRef{frameRef}, role: 68}
	frame := &syntheticATSPINode{
		name: "Editor — document", description: "Editor",
		role: 23, text: "sensitive text",
		attrs:       map[string]string{"password": "secret", "role": "editor", "text-value": "sensitive text"},
		interfaces:  []string{componentIface, textIface},
		application: appRef,
		states:      []uint32{(1 << 8) | (1 << 12) | (1 << 25) | (1 << 30)},
		x:           10, y: 15, width: 900, height: 700,
	}
	if protected {
		frame.states = append(frame.states, 1<<2)
	}
	if setup != nil {
		setup(root, frame)
	}
	for _, item := range []struct {
		path dbus.ObjectPath
		node *syntheticATSPINode
	}{
		{desktopPath, root}, {appPath, app}, {framePath, frame},
	} {
		if err := service.Export(item.node, item.path, accessibleIface); err != nil {
			t.Fatal(err)
		}
		if err := service.Export(item.node, item.path, propertiesIface); err != nil {
			t.Fatal(err)
		}
		if err := service.Export(item.node, item.path, componentIface); err != nil {
			t.Fatal(err)
		}
		if err := service.Export(item.node, item.path, textIface); err != nil {
			t.Fatal(err)
		}
	}
	client, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	backend := &dbusBackend{
		generation: 1, access: client,
		cache:        make(map[string]cachedSnapshot),
		cacheItems:   make(map[NodeID]cacheItem),
		incarnations: make(map[objectIdentity]uint64),
		parents:      make(map[objectIdentity]objectIdentity),
		toolkits:     make(map[string]string),
	}
	target := WindowTarget{ID: "window-7", PID: int32(os.Getpid()), Title: "Editor", Bounds: Rect{X: 10, Y: 15, Width: 900, Height: 700}, Active: true}
	return backend, backend.refID(frameRef), target
}

func TestSyntheticATSPINodeReadsTextGeometryAndMetadata(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackend(t, false)
	node, err := backend.readNode(context.Background(), id, NodeID{}, 20, false)
	if err != nil {
		t.Fatalf("readNode: %v", err)
	}
	if node.Name != "Editor — document" || node.Role != "frame" || node.Text != "sensitive text" {
		t.Fatalf("AT-SPI read omitted identity or text: %+v", node)
	}
	if !node.HasBounds || node.Bounds != (Rect{X: 10, Y: 15, Width: 900, Height: 700}) {
		t.Fatalf("component extent incorrect: %+v", node.Bounds)
	}
	if !node.Enabled || !node.Focused || !node.Visible || !node.Showing {
		t.Fatalf("AT-SPI state flags not applied: %+v", node)
	}
	if node.Attributes["password"] != "secret" {
		t.Fatalf("non-protected attribute missing: %+v", node.Attributes)
	}
	if len(node.Warnings) != 0 {
		t.Logf("optional protocol warnings: %v", node.Warnings)
	}
}

func TestSyntheticATSPIProtectedNodeNeverLeaksTextOrAttributes(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackend(t, true)
	node, err := backend.readNodeFresh(context.Background(), id, NodeID{}, 20, false)
	if err != nil {
		t.Fatal(err)
	}
	if !node.Redacted || node.Text != "" || node.Value != nil {
		t.Fatalf("protected node leaked text/value: %+v", node)
	}
	for key := range node.Attributes {
		if strings.Contains(strings.ToLower(key), "password") || strings.Contains(strings.ToLower(key), "text") {
			t.Fatalf("protected node leaked %q attribute", key)
		}
	}
	raw, err := backend.readNodeFresh(context.Background(), id, NodeID{}, 20, true)
	if err != nil || raw.Redacted || raw.Text != "sensitive text" {
		t.Fatalf("explicitly authorized sensitive read = %+v, %v", raw, err)
	}
}

func TestSyntheticATSPIStaleNodeRejectedAfterInvalidation(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackend(t, false)
	backend.Invalidate(id)
	if _, err := backend.readNode(context.Background(), id, NodeID{}, 20, false); !errors.Is(err, ErrStaleNode) && !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("old AT-SPI handle not invalidated: %v", err)
	}
}

func TestSyntheticATSPIWindowCorrelationUsesLiveIdentity(t *testing.T) {
	backend, _, target := newSyntheticATSPIBackend(t, false)
	scope, err := backend.ResolveWindowFresh(context.Background(), target)
	if err != nil {
		t.Fatalf("real D-Bus window correlation: %v", err)
	}
	if scope.WindowID != "window-7" || scope.Root.ObjectPath != "/org/a11y/atspi/accessible/editor" {
		t.Fatalf("window scope correlated to wrong node: %+v", scope)
	}
	noEvidence := WindowTarget{ID: "window-7"}
	if _, err := backend.ResolveWindowFresh(context.Background(), noEvidence); !errors.Is(err, ErrUnsupportedCorrelation) {
		t.Fatalf("no-evidence correlation did not fail closed: %v", err)
	}
}

func TestSyntheticATSPIApplicationsAndFreshLookup(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackend(t, false)
	ctx := context.Background()
	apps, err := backend.Applications(ctx)
	if err != nil || len(apps) != 1 || apps[0].Name != "Editor" {
		t.Fatalf("private accessibility bus applications=%+v err=%v", apps, err)
	}
	app, err := backend.FindApplication(ctx, ApplicationFilter{Name: "editor"})
	if err != nil || app.Name != "Editor" {
		t.Fatalf("FindApplication returned %+v: %v", app, err)
	}
	fresh, err := backend.FindApplicationFresh(ctx, ApplicationFilter{Name: "EDITOR", Bus: registryName})
	if err != nil || fresh.Name != "Editor" {
		t.Fatalf("FindApplicationFresh returned %+v: %v", fresh, err)
	}
	for _, filter := range []ApplicationFilter{
		{Name: "not-editor"}, {PID: 999999999}, {Bus: "org.elsewhere"}, {WindowID: "/wrong/path"},
	} {
		if _, err := backend.FindApplication(ctx, filter); !errors.Is(err, ErrNotFound) {
			t.Fatalf("nonmatching app filter %+v returned %v", filter, err)
		}
		if _, err := backend.FindApplicationFresh(ctx, filter); !errors.Is(err, ErrNotFound) {
			t.Fatalf("fresh app filter %+v returned %v", filter, err)
		}
	}
}

func TestSyntheticATSPIWindowScopeCachedAndFreshAgree(t *testing.T) {
	backend, id, target := newSyntheticATSPIBackend(t, false)
	ctx := context.Background()
	cached, err := backend.ResolveWindow(ctx, target)
	if err != nil {
		t.Fatalf("ResolveWindow: %v", err)
	}
	fresh, err := backend.ResolveWindowFresh(ctx, target)
	if err != nil {
		t.Fatalf("ResolveWindowFresh: %v", err)
	}
	if cached.Root != id || fresh.Root != id {
		t.Fatalf("cached and fresh correlated to different roots: %+v %+v", cached, fresh)
	}
	if _, err := backend.ResolveWindow(ctx, WindowTarget{ID: target.ID, Title: "Not Editor", PID: target.PID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("incorrect title unexpectedly correlated: %v", err)
	}
	empty := WindowTarget{ID: "   ", Title: target.Title}
	if _, err := backend.ResolveWindow(ctx, empty); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing window identity accepted: %v", err)
	}
}

func TestSyntheticATSPISnapshotBoundsAndSensitiveRedaction(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackend(t, true)
	ctx := context.Background()
	snapshot, err := backend.Snapshot(ctx, id, SnapshotOptions{Fresh: true, MaxDepth: 1, MaxNodes: 2})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Root.ID != id || len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Text != "" || !snapshot.Nodes[0].Redacted {
		t.Fatalf("protected snapshot leaked data: root=%+v nodes=%+v", snapshot.Root, snapshot.Nodes)
	}
	if _, err := backend.Snapshot(ctx, NodeID{}, SnapshotOptions{}); !errors.Is(err, ErrScope) {
		t.Fatalf("unscoped snapshot should be refused: %v", err)
	}
	allowed, err := backend.Snapshot(ctx, id, SnapshotOptions{Fresh: true, AllowSensitive: true, MaxNodes: 2})
	if err != nil || allowed.Root.Text != "sensitive text" {
		t.Fatalf("authorized snapshot text=%q err=%v", allowed.Root.Text, err)
	}
	backend.Invalidate(id)
	if _, err := backend.Snapshot(ctx, id, SnapshotOptions{Fresh: true}); !errors.Is(err, ErrStaleNode) && !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale snapshot was accepted: %v", err)
	}
}

func TestSyntheticATSPIApplicationAndWindowLookupsRespectCancellation(t *testing.T) {
	backend, _, target := newSyntheticATSPIBackend(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Applications(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Applications cancellation=%v", err)
	}
	if _, err := backend.FindApplicationFresh(ctx, ApplicationFilter{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("fresh application cancellation=%v", err)
	}
	if _, err := backend.ResolveWindowFresh(ctx, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("window cancellation=%v", err)
	}
}

func TestAccessibilityBackendNilAndIdempotentLifecycleBoundary(t *testing.T) {
	var nilBackend *dbusBackend
	if got := nilBackend.Generation(); got != 0 {
		t.Fatalf("nil backend generation=%d", got)
	}
	nilBackend.Invalidate(NodeID{})
	if err := nilBackend.Close(); err != nil {
		t.Fatalf("nil backend close: %v", err)
	}
	if err := nilBackend.validateHandle(NodeID{}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil backend handle accepted: %v", err)
	}
	b := &dbusBackend{generation: 5, cacheApps: map[string]bool{"org.test.Application": true}}
	if err := b.Close(); err != nil {
		t.Fatalf("empty desktop close: %v", err)
	}
	closedGeneration := b.Generation()
	if err := b.Close(); err != nil || b.Generation() != closedGeneration {
		t.Fatalf("duplicate close affected epoch: %v generation=%d", err, b.Generation())
	}
	b.Invalidate(NodeID{})
	if err := b.validateHandle(NodeID{BusName: "org.test.Application", ObjectPath: "/node", Generation: b.Generation()}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("closed handle became live: %v", err)
	}
}

func TestAccessibilityInvalidOpenNeverUsesUnscopedSessionBus(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=:1", "XDG_CURRENT_DESKTOP=GNOME"})
	if backend, err := openRuntime(context.Background(), rt, 0); backend != nil || err == nil {
		t.Fatalf("zero-generation unscoped bus accepted: %v %v", backend, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if backend, err := openRuntime(ctx, rt, 0); backend != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled accessibility opener succeeded: %v %v", backend, err)
	}
}

func TestAccessibilityParentTrackingFailClosedAtCapacity(t *testing.T) {
	child := cacheObjectRef{BusName: "org.test.Application", ObjectPath: "/child"}
	parent := cacheObjectRef{BusName: "org.test.Application", ObjectPath: "/parent"}
	b := &dbusBackend{generation: 1}
	b.recordParentLocked(child, parent)
	id := objectIdentity{busName: child.BusName, objectPath: string(child.ObjectPath)}
	if b.parents[id] != (objectIdentity{busName: parent.BusName, objectPath: string(parent.ObjectPath)}) {
		t.Fatalf("first parent never tracked: %+v", b.parents)
	}
	if b.incarnations == nil {
		t.Fatal("parent-tracking did not initialize incarnation map")
	}
	for i := 0; i < maxTrackedObjects; i++ {
		b.parents[objectIdentity{busName: "org.test.Application", objectPath: fmt.Sprintf("/overflow/%d", i)}] = objectIdentity{busName: "org.test.Application", objectPath: "/root"}
	}
	b.recordParentLocked(cacheObjectRef{BusName: "org.test.Application", ObjectPath: "/late"}, parent)
	if !b.parentTrackingIncomplete {
		t.Fatal("oversized parent graph remained authoritative")
	}
	if _, ok := b.parents[objectIdentity{busName: "org.test.Application", objectPath: "/late"}]; ok {
		t.Fatal("unbounded graph inserted untracked parent")
	}
	before := b.generation
	b.revokeOrInvalidateLocked(objectIdentity{busName: "org.test.Application", objectPath: "/root"})
	if b.generation != before+1 || b.parents != nil || b.incarnations != nil {
		t.Fatalf("incomplete ancestry only invalidated one object: generation=%d parents=%v", b.generation, b.parents != nil)
	}
}

func TestAccessibilityToolkitCacheDoesNotResurrectStaleHandles(t *testing.T) {
	b := &dbusBackend{generation: 5, toolkits: map[string]string{"org.test.Application": "Qt"}}
	id := NodeID{BusName: "org.test.Application", ObjectPath: "/node", Generation: 5, Incarnation: 1}
	toolkit, ok, err := b.cachedToolkit(id)
	if err != nil || !ok || toolkit != "Qt" {
		t.Fatalf("valid toolkit cache lookup failed: %q %v %v", toolkit, ok, err)
	}
	if err := b.cacheToolkit(id, "GTK"); err != nil {
		t.Fatalf("live toolkit update: %v", err)
	}
	if name, err := b.toolkitForNode(context.Background(), id); err != nil || name != "GTK" {
		t.Fatalf("cached toolkit resolution unexpectedly accessed D-Bus: %q %v", name, err)
	}
	b.Invalidate(id)
	if _, ok, err := b.cachedToolkit(id); ok || !errors.Is(err, ErrStaleNode) {
		t.Fatalf("stale toolkit accepted: %v %v", ok, err)
	}
	if err := b.cacheToolkit(id, "stale"); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("stale toolkit reinserted: %v", err)
	}
	if _, err := b.toolkitForNode(context.Background(), id); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("stale toolkit resolver accessed D-Bus: %v", err)
	}
}

// A complete incarnation map leaves no capacity to record a new removed
// object. Selective revocation must refuse to claim a complete subtree so
// the caller can retire the entire backend epoch instead.
func TestAccessibilitySelectiveRevocationRejectsUntrackedObjectAtCapacity(t *testing.T) {
	const bus = "org.example.Editor"
	b := &dbusBackend{generation: 7, incarnations: make(map[objectIdentity]uint64, maxTrackedObjects)}
	for i := 0; i < maxTrackedObjects; i++ {
		b.incarnations[objectIdentity{busName: bus, objectPath: "/observed/" + strconv.Itoa(i)}] = 1
	}
	removed := objectIdentity{busName: bus, objectPath: "/newly-removed"}
	if b.revokeObjectLocked(removed) {
		t.Fatal("selective revoke accepted object with no remaining tracking capacity")
	}
	if b.generation != 7 || len(b.incarnations) != maxTrackedObjects || b.incarnations[removed] != 0 {
		t.Fatalf("refused selective revoke mutated epoch or tracking: gen=%d count=%d new=%d", b.generation, len(b.incarnations), b.incarnations[removed])
	}
	// Once called through the normal policy, an untracked removal must
	// invalidate the entire epoch rather than preserving old handles.
	b.revokeOrInvalidateLocked(removed)
	if b.generation != 8 || len(b.incarnations) != 0 || b.parents != nil {
		t.Fatalf("untracked removal failed closed: generation=%d identities=%d parents=%v", b.generation, len(b.incarnations), b.parents)
	}
}

func TestAccessibilityRejectsMalformedHandleAndNilEnumerationContext(t *testing.T) {
	b := &dbusBackend{generation: 7}
	if err := b.validateHandle(NodeID{BusName: "org.example.Editor", ObjectPath: "", Generation: 7}); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("malformed handle accepted: %v", err)
	}
	apps, err := b.Applications(nil)
	if err == nil || len(apps) != 0 {
		t.Fatalf("nil context disclosed applications: %v err=%v", apps, err)
	}
}
