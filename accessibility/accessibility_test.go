package accessibility

import (
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"reflect"
	"sync"
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

func TestDocumentTextSelectionWireSignature(t *testing.T) {
	if got := dbus.SignatureOf([]documentTextSelectionWire{}).String(); got != "a((so)i(so)ib)" {
		t.Fatalf("document selection signature = %q, want a((so)i(so)ib)", got)
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
		generation: 1,
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
	type call struct {
		method string
		args   []any
	}
	var calls []call
	backend := &dbusBackend{generation: 1, callOverride: func(_ context.Context, _ NodeID, method string, args []any) (any, error) {
		calls = append(calls, call{method: method, args: args})
		if method == actionIface+".GetActions" {
			return []Action{{Index: 0, Name: "activate"}, {Index: 1, Name: "alternate"}}, nil
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
		{"action-name", func() error { return backend.InvokeActionByName(ctx, id, "alternate") }, []call{{actionIface + ".GetActions", nil}, {actionIface + ".DoAction", []any{int32(1)}}}},
		{"default-action", func() error { _, err := backend.InvokeDefaultAction(ctx, id); return err }, []call{{actionIface + ".GetActions", nil}, {actionIface + ".DoAction", []any{int32(0)}}}},
		{"focus", func() error { return backend.GrabFocus(ctx, id) }, []call{{componentIface + ".GrabFocus", nil}}},
		{"scroll", func() error { return backend.ScrollTo(ctx, id, ScrollAnyWhere) }, []call{{componentIface + ".ScrollTo", []any{uint32(ScrollAnyWhere)}}}},
		{"scroll-point", func() error { return backend.ScrollToPoint(ctx, id, CoordTypeWindow, 10, 20) }, []call{{componentIface + ".ScrollToPoint", []any{uint32(CoordTypeWindow), int32(10), int32(20)}}}},
		{"set-position", func() error { return backend.SetPosition(ctx, id, 11, 12, CoordTypeParent) }, []call{{componentIface + ".SetPosition", []any{int32(11), int32(12), uint32(CoordTypeParent)}}}},
		{"set-size", func() error { return backend.SetSize(ctx, id, 640, 480) }, []call{{componentIface + ".SetSize", []any{int32(640), int32(480)}}}},
		{"set-extents", func() error { return backend.SetExtents(ctx, id, 1, 2, 640, 480, CoordTypeScreen) }, []call{{componentIface + ".SetExtents", []any{int32(1), int32(2), int32(640), int32(480), uint32(CoordTypeScreen)}}}},
		{"value", func() error { return backend.SetCurrentValue(ctx, id, 0.5) }, []call{{propertiesIface + ".Set", []any{valueIface, "CurrentValue", dbus.MakeVariant(0.5)}}}},
		{"value-alias", func() error { return backend.SetValue(ctx, id, 0.5) }, []call{{propertiesIface + ".Set", []any{valueIface, "CurrentValue", dbus.MakeVariant(0.5)}}}},
		{"text", func() error { return backend.SetTextContents(ctx, id, "safe") }, []call{{editableTextIface + ".SetTextContents", []any{"safe"}}}},
		{"replace", func() error { return backend.ReplaceText(ctx, id, 0, 1, "x") }, []call{{editableTextIface + ".DeleteText", []any{int32(0), int32(1)}}, {editableTextIface + ".InsertText", []any{int32(0), "x", int32(1)}}}},
		{"insert", func() error { return backend.InsertText(ctx, id, 0, "x") }, []call{{editableTextIface + ".InsertText", []any{int32(0), "x", int32(1)}}}},
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
		{"deselect-all-alias", func() error { return backend.DeselectAll(ctx, id) }, []call{{selectionIface + ".ClearSelection", nil}}},
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
	item := cacheItem{Object: cacheObjectRef{BusName: "org.test", ObjectPath: "/node"}}
	backend.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":AddAccessible", Body: []any{item}})
	if got := backend.Generation(); got != 5 {
		t.Fatalf("cache signal changed generation before dispatcher invalidation: %d", got)
	}
	backend.Invalidate(item.nodeID())
	if got := backend.Generation(); got != 6 {
		t.Fatalf("single invalidation advanced generation to %d", got)
	}
}

func TestPreparedEventUsesPostInvalidationGeneration(t *testing.T) {
	backend := &dbusBackend{generation: 5, cacheItems: make(map[NodeID]cacheItem), cacheApps: map[string]bool{"org.test": true}}
	item := cacheItem{
		Object: cacheObjectRef{BusName: "org.test", ObjectPath: "/node"},
		Parent: cacheObjectRef{BusName: "org.test", ObjectPath: "/parent"},
	}
	sig := &dbus.Signal{Name: cacheIface + ":AddAccessible", Body: []any{item}}
	event := backend.prepareEvent(sig, Event{Kind: sig.Name, Node: NodeID{BusName: "org.test", ObjectPath: "/node"}})
	if event.Node.Generation != 6 {
		t.Fatalf("event generation = %d, want 6", event.Node.Generation)
	}
	if err := backend.validateHandle(event.Node); err != nil {
		t.Fatalf("prepared event handle is not current: %v", err)
	}
	if _, ok := backend.cachedItem(event.Node); !ok {
		t.Fatalf("cache item was not stored in event generation %d", event.Node.Generation)
	}
}

func TestCacheSignalTransitionPreservesAddAndRemoveState(t *testing.T) {
	backend := &dbusBackend{
		generation: 5,
		cacheItems: make(map[NodeID]cacheItem),
		cacheApps:  map[string]bool{"org.test": true},
	}
	item := cacheItem{
		Object: cacheObjectRef{BusName: "org.test", ObjectPath: "/node"},
		Parent: cacheObjectRef{BusName: "org.test", ObjectPath: "/parent"},
	}
	added := backend.prepareEvent(&dbus.Signal{Name: cacheIface + ":AddAccessible", Body: []any{item}}, Event{Node: NodeID{BusName: "org.test", ObjectPath: "/node"}})
	if added.Node.Generation != 6 || backend.Generation() != 6 {
		t.Fatalf("cache add generation = event %d/backend %d, want 6", added.Node.Generation, backend.Generation())
	}
	if _, ok := backend.cachedItem(added.Node); !ok {
		t.Fatal("cache add was lost after generation transition")
	}
	removed := backend.prepareEvent(&dbus.Signal{Name: cacheIface + ":RemoveAccessible", Body: []any{item.Object}}, Event{Node: added.Node})
	if removed.Node.Generation != 7 || backend.Generation() != 7 {
		t.Fatalf("cache remove generation = event %d/backend %d, want 7", removed.Node.Generation, backend.Generation())
	}
	if _, ok := backend.cachedItem(NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 7}); ok {
		t.Fatal("cache remove left the removed object present")
	}
	if !backend.cacheApps["org.test"] {
		t.Fatal("cache application registration was lost during signal invalidation")
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
	if event.Node.Generation != 6 {
		t.Fatalf("event generation = %d, want 6", event.Node.Generation)
	}
	if _, ok := backend.cachedItem(NodeID{BusName: oldID.BusName, ObjectPath: oldID.ObjectPath, Generation: 6}); ok {
		t.Fatal("stale cached name/state was reused after a non-cache event")
	}
	if backend.cacheApps[oldID.BusName] {
		t.Fatal("cache application remained marked fresh after non-cache invalidation")
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
