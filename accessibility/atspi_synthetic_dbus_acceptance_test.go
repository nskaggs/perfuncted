package accessibility

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

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
	application         objectRef
	pointResult         objectRef
	pointFailure        string
	x, y, width, height int32
}

func (n *syntheticATSPINode) GetRole() (uint32, *dbus.Error)                  { return n.role, nil }
func (n *syntheticATSPINode) GetInterfaces() ([]string, *dbus.Error)          { return n.interfaces, nil }
func (n *syntheticATSPINode) GetState() ([]uint32, *dbus.Error)               { return n.states, nil }
func (n *syntheticATSPINode) GetAttributes() (map[string]string, *dbus.Error) { return n.attrs, nil }
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
