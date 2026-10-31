package accessibility

import (
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

// Fresh snapshots must establish not merely that an added object exists, but
// that its role, state, interfaces, parent and child count correspond to the
// provider's concurrent Cache:AddAccessible signal. Incomplete observations
// must never be presented as verified negative search results.
func TestFreshSnapshotCacheAdditionRejectsMismatchedProviderObservations(t *testing.T) {
	root := NodeID{BusName: "org.example.App", ObjectPath: "/window", Generation: 3}
	child := NodeID{BusName: root.BusName, ObjectPath: "/window/button", Generation: 3}
	item := cacheItem{Object: cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(child.ObjectPath)}, Application: cacheObjectRef{BusName: root.BusName, ObjectPath: "/app"}, Parent: cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)}, Role: 43, Name: "Save", Description: "Write document", ChildCount: 0, Interfaces: []string{accessibleIface, actionIface}, States: []uint32{1 << 8}}
	baseParent := Node{ID: root, Role: "window", Children: []NodeID{child}}
	baseChild := Node{ID: child, Parent: root, RoleID: 43, Name: "Save", Description: "Write document", ChildCount: 0, Interfaces: []string{actionIface, accessibleIface}, States: []string{stateName(8)}}
	for _, tc := range []struct {
		name   string
		change func(*Node, *Node, *cacheItem)
		accept bool
	}{
		{"matching complete snapshot", func(*Node, *Node, *cacheItem) {}, true},
		{"missing added child", func(p *Node, _ *Node, _ *cacheItem) { p.Children = nil }, false},
		{"unexpected parent", func(_ *Node, c *Node, _ *cacheItem) {
			c.Parent = NodeID{BusName: root.BusName, ObjectPath: "/other", Generation: 3}
		}, false},
		{"forged name", func(_ *Node, c *Node, _ *cacheItem) { c.Name = "Save As" }, false},
		{"forged description", func(_ *Node, c *Node, _ *cacheItem) { c.Description = "Different" }, false},
		{"mismatched role", func(_ *Node, c *Node, _ *cacheItem) { c.RoleID = 12 }, false},
		{"mismatched interface", func(_ *Node, c *Node, _ *cacheItem) { c.Interfaces = []string{accessibleIface} }, false},
		{"missing state", func(_ *Node, c *Node, _ *cacheItem) { c.States = nil }, false},
		{"extra state", func(_ *Node, c *Node, _ *cacheItem) { c.States = append(c.States, "focused") }, false},
		{"child count mismatch", func(_ *Node, c *Node, _ *cacheItem) { c.ChildCount = 1 }, false},
		{"duplicate interface", func(_ *Node, c *Node, _ *cacheItem) { c.Interfaces = []string{accessibleIface, accessibleIface} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, observed, copyItem := baseParent, baseChild, item
			parent.Children = append([]NodeID(nil), baseParent.Children...)
			observed.Interfaces = append([]string(nil), baseChild.Interfaces...)
			observed.States = append([]string(nil), baseChild.States...)
			tc.change(&parent, &observed, &copyItem)
			snap := Snapshot{Root: parent, Nodes: []Node{parent, observed}, Generation: root.Generation}
			change := observationChange{revision: 1, kind: cacheIface + ":AddAccessible", node: objectIdentity{busName: root.BusName, objectPath: child.ObjectPath}, parent: objectIdentity{busName: root.BusName, objectPath: root.ObjectPath}, cacheAdd: true, cacheItem: copyItem}
			b := &dbusBackend{generation: 3, observationRevision: 1, observationHistory: []observationChange{change}}
			err := b.observationChangesError(0, root, snap, true)
			if tc.accept && err != nil {
				t.Fatalf("identical fresh cache addition rejected: %v", err)
			}
			if !tc.accept && !errors.Is(err, ErrObservationChanged) {
				t.Fatalf("inconsistent fresh cache addition accepted: %v", err)
			}
			err = b.observationChangesError(0, root, snap, false)
			if !errors.Is(err, ErrObservationChanged) {
				t.Fatalf("cached observation wrongly accepted concurrent addition: %v", err)
			}
		})
	}
}
func TestSnapshotConcurrentRemovalAndWindowCreationScopeBoundaries(t *testing.T) {
	root := NodeID{BusName: "org.example.App", ObjectPath: "/window", Generation: 5}
	child := NodeID{BusName: root.BusName, ObjectPath: "/window/button", Generation: 5}
	nodes := map[objectIdentity]Node{
		{busName: root.BusName, objectPath: root.ObjectPath}:   {ID: root, Role: "window"},
		{busName: child.BusName, objectPath: child.ObjectPath}: {ID: child, Parent: root, Role: "button"},
	}
	rootIdentity := objectIdentity{busName: root.BusName, objectPath: root.ObjectPath}
	childIdentity := objectIdentity{busName: child.BusName, objectPath: child.ObjectPath}
	foreign := objectIdentity{busName: "org.unrelated.App", objectPath: "/unrelated"}
	for _, tc := range []struct {
		name     string
		change   observationChange
		scope    objectIdentity
		rootRole string
		reject   bool
	}{
		{"observed child removed", observationChange{node: childIdentity, cacheRemove: true}, rootIdentity, "window", true},
		{"observed parent removed", observationChange{node: foreign, parent: rootIdentity, childrenRemove: true}, rootIdentity, "window", true},
		{"observed descendant removed", observationChange{node: foreign, removedChild: childIdentity, childrenRemove: true}, rootIdentity, "window", true},
		{"unknown same-app removal", observationChange{node: objectIdentity{busName: root.BusName, objectPath: "/unknown"}, cacheRemove: true}, rootIdentity, "window", true},
		{"different-app removal", observationChange{node: foreign, cacheRemove: true}, rootIdentity, "window", false},
		{"foreign window creation", observationChange{kind: "Window:Create", node: foreign}, rootIdentity, "application", false},
		{"in-app window creation", observationChange{kind: "Window:Create", node: objectIdentity{busName: root.BusName, objectPath: "/window/new"}}, rootIdentity, "application", true},
		{"in-window scope create unrelated", observationChange{kind: "Window:Create", node: objectIdentity{busName: root.BusName, objectPath: "/window/new"}}, rootIdentity, "window", false},
		{"desktop scope creation", observationChange{kind: "Window:Create", node: foreign}, objectIdentity{busName: registryName, objectPath: string(desktopPath)}, "desktop", true},
		{"unknown changed identity", observationChange{unknown: true}, rootIdentity, "window", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := observationChangeError(tc.change, tc.scope, tc.scope, tc.rootRole, nodes, true)
			if tc.reject && !errors.Is(err, ErrObservationChanged) {
				t.Fatalf("concurrent change was not rejected: %v", err)
			}
			if !tc.reject && err != nil {
				t.Fatalf("unrelated change invalidated independent observation: %v", err)
			}
		})
	}
}
