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
	if !(NodeID{BusName: "org.example", ObjectPath: "/org/example/node", Generation: 1}).valid() {
		t.Fatal("ordinary AT-SPI object is invalid")
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
}

func TestCacheItemsProvideDeterministicChildrenAndSignals(t *testing.T) {
	root := NodeID{BusName: "org.test.App", ObjectPath: "/root", Generation: 1}
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
	root := NodeID{BusName: "b", ObjectPath: "/root", Generation: 1}
	child := NodeID{BusName: "b", ObjectPath: "/child", Generation: 1}
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
	root := NodeID{BusName: "b", ObjectPath: "/root", Generation: 1}
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
	root := NodeID{BusName: "b", ObjectPath: "/root", Generation: 1}
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

func TestTypedAutomationProtocolFixtureCoversMutations(t *testing.T) {
	var methods []string
	backend := &dbusBackend{generation: 1, callOverride: func(_ context.Context, _ NodeID, method string, _ []any) (any, error) {
		methods = append(methods, method)
		if method == actionIface+".GetActions" {
			return []Action{{Index: 0, Name: "activate"}, {Index: 1, Name: "alternate"}}, nil
		}
		return true, nil
	}}
	id := NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 1}
	ctx := context.Background()
	if err := backend.InvokeAction(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := backend.InvokeActionByName(ctx, id, "alternate"); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.InvokeDefaultAction(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := backend.GrabFocus(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := backend.ScrollTo(ctx, id, ScrollAnyWhere); err != nil {
		t.Fatal(err)
	}
	if err := backend.ScrollToPoint(ctx, id, ScrollTopLeft, 10, 20); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetCurrentValue(ctx, id, 0.5); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetTextContents(ctx, id, "safe"); err != nil {
		t.Fatal(err)
	}
	if err := backend.ReplaceText(ctx, id, 0, 1, "x"); err != nil {
		t.Fatal(err)
	}
	if err := backend.InsertText(ctx, id, 0, "x"); err != nil {
		t.Fatal(err)
	}
	if err := backend.CopyText(ctx, id, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := backend.CutText(ctx, id, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := backend.PasteText(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetCaretOffset(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetTextSelection(ctx, id, 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := backend.AddTextSelection(ctx, id, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := backend.RemoveTextSelection(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := backend.SelectChild(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := backend.DeselectChild(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := backend.SelectAll(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := backend.ClearSelection(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := backend.DeselectAll(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := backend.SelectRow(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := backend.DeselectRow(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := backend.SelectColumn(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := backend.DeselectColumn(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if len(methods) < 25 {
		t.Fatalf("recorded only %d typed protocol calls: %v", len(methods), methods)
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
