package accessibility

import (
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
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
}

func TestNodeIDValidation(t *testing.T) {
	if (NodeID{}).valid() {
		t.Fatal("zero NodeID is valid")
	}
	if (NodeID{BusName: "org.example", ObjectPath: string(nullObjectPath)}).valid() {
		t.Fatal("null AT-SPI object is valid")
	}
	if !(NodeID{BusName: "org.example", ObjectPath: "/org/example/node"}).valid() {
		t.Fatal("ordinary AT-SPI object is invalid")
	}
}

func TestCacheItemWireSignatureMatchesATSPICache(t *testing.T) {
	if got := dbus.SignatureOf([]cacheItem{}).String(); got != "a((so)(so)(so)iiassusau)" {
		t.Fatalf("cache GetItems signature = %q", got)
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
}

func TestEventCoalescingTracksDuplicateInvalidations(t *testing.T) {
	base := time.Now()
	first := Event{Kind: "focus", Node: NodeID{BusName: "app", ObjectPath: "/node"}, Timestamp: base}
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

func TestCloneSnapshotDeepCopiesAndPreservesMetadata(t *testing.T) {
	at := time.Now()
	in := Snapshot{Root: Node{ID: NodeID{BusName: "b", ObjectPath: "/r"}}, Nodes: []Node{{ID: NodeID{BusName: "b", ObjectPath: "/r"}, Attributes: map[string]string{"value": "x"}, Children: []NodeID{{BusName: "b", ObjectPath: "/c"}}, Warnings: []string{"optional Text unavailable"}}}, Generation: 4, CapturedAt: at, Source: "at-spi"}
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
	root := NodeID{BusName: "b", ObjectPath: "/r"}
	if snapshotKey(root, SnapshotOptions{MaxDepth: 1}) == snapshotKey(root, SnapshotOptions{MaxDepth: 2}) {
		t.Fatal("depth not part of snapshot key")
	}
	if snapshotKey(root, SnapshotOptions{AllowSensitive: true}) == snapshotKey(root, SnapshotOptions{}) {
		t.Fatal("redaction policy not part of snapshot key")
	}
	if snapshotKey(root, SnapshotOptions{VisibleOnly: true}) == snapshotKey(root, SnapshotOptions{}) {
		t.Fatal("visibility policy not part of snapshot key")
	}
}

func TestCacheItemsProvideDeterministicChildrenAndSignals(t *testing.T) {
	root := NodeID{BusName: "org.test.App", ObjectPath: "/root"}
	first := cacheItem{Object: cacheObjectRef{BusName: root.BusName, ObjectPath: "/first"}, Parent: cacheObjectRef{BusName: root.BusName, ObjectPath: "/root"}, Index: 1}
	second := cacheItem{Object: cacheObjectRef{BusName: root.BusName, ObjectPath: "/second"}, Parent: cacheObjectRef{BusName: root.BusName, ObjectPath: "/root"}, Index: 0}
	backend := &dbusBackend{
		cacheItems: map[NodeID]cacheItem{first.nodeID(): first, second.nodeID(): second},
		cacheApps:  map[string]bool{root.BusName: true},
	}
	children := backend.cachedChildren(root)
	if len(children) != 2 || children[0].ObjectPath != "/second" || children[1].ObjectPath != "/first" {
		t.Fatalf("children = %+v, want index order", children)
	}
	backend.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":RemoveAccessible", Body: []any{cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath("/second")}}})
	if got := len(backend.cachedChildren(root)); got != 1 {
		t.Fatalf("children after remove = %d, want 1", got)
	}
	added := cacheItem{Object: cacheObjectRef{BusName: root.BusName, ObjectPath: "/third"}, Parent: cacheObjectRef{BusName: root.BusName, ObjectPath: "/root"}, Index: 0}
	backend.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":AddAccessible", Body: []any{added}})
	if got := len(backend.cachedChildren(root)); got != 2 {
		t.Fatalf("children after add = %d, want 2", got)
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
	node := Node{Name: "Password", Role: "entry", Text: "secret", States: []string{"sensitive"}, Attributes: map[string]string{"value": "secret", "aria-label": "Password", "class": "field"}}
	redactSensitiveNode(&node)
	if !node.Redacted || node.Text != "" {
		t.Fatalf("redacted node = %+v", node)
	}
	if _, ok := node.Attributes["value"]; ok {
		t.Fatal("value attribute leaked")
	}
	if node.Attributes["aria-label"] != "Password" || node.Attributes["class"] != "field" {
		t.Fatalf("semantic attributes removed: %+v", node.Attributes)
	}
}

type walkerFake struct {
	nodes map[NodeID]Node
	child map[NodeID][]objectRef
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

func TestSnapshotWalkerBoundsDepthNodesAndCycles(t *testing.T) {
	root := NodeID{BusName: "b", ObjectPath: "/root"}
	child := NodeID{BusName: "b", ObjectPath: "/child"}
	fake := walkerFake{
		nodes: map[NodeID]Node{root: {ID: root, ChildCount: 1}, child: {ID: child, ChildCount: 1}},
		child: map[NodeID][]objectRef{root: {{BusName: "b", ObjectPath: "/child"}}, child: {{BusName: "b", ObjectPath: "/root"}}},
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
	root := NodeID{BusName: "b", ObjectPath: "/root"}
	fake := walkerFake{nodes: map[NodeID]Node{root: {ID: root, ChildCount: 1}}, child: map[NodeID][]objectRef{root: {{BusName: "b", ObjectPath: "/missing"}}}}
	walker := snapshotWalker{backend: fake, opts: SnapshotOptions{MaxDepth: 4, MaxNodes: 4, MaxTextBytes: 10}, snapshot: Snapshot{}, seen: map[NodeID]struct{}{}}
	if _, err := walker.walk(context.Background(), root, NodeID{}, 0); err != nil {
		t.Fatalf("walk = %v", err)
	}
	if len(walker.snapshot.Warnings) != 1 {
		t.Fatalf("warnings = %+v", walker.snapshot.Warnings)
	}
}

func TestSnapshotWalkerHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := NodeID{BusName: "b", ObjectPath: "/root"}
	walker := snapshotWalker{backend: walkerFake{nodes: map[NodeID]Node{root: {ID: root}}}, opts: SnapshotOptions{MaxDepth: 1, MaxNodes: 1, MaxTextBytes: 1}, snapshot: Snapshot{}, seen: map[NodeID]struct{}{}}
	if _, err := walker.walk(ctx, root, NodeID{}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("walk error = %v", err)
	}
}

func TestOpenRuntimeReportsMissingSessionBus(t *testing.T) {
	_, err := OpenRuntime(env.FromEnviron([]string{}))
	if err == nil {
		t.Fatal("OpenRuntime unexpectedly succeeded without session bus")
	}
}
