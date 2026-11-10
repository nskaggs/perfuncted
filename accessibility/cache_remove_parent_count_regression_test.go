package accessibility

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// Removing a child from the authenticated AT-SPI cache must reconcile its
// parent's cached ChildCount, not leave the previous count authoritative.
func TestCacheRemoveDecrementsParentChildCount(t *testing.T) {
	const bus = "org.example.Owner"
	parentRef := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath("/parent")}
	childRef := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath("/child")}
	parentID := NodeID{BusName: bus, ObjectPath: "/parent", Generation: 7, Incarnation: 1}
	childID := NodeID{BusName: bus, ObjectPath: "/child", Generation: 7, Incarnation: 1}
	backend := &dbusBackend{
		generation: 7,
		cacheApps:  map[string]bool{bus: true},
		cacheItems: map[NodeID]cacheItem{
			parentID: {Object: parentRef, Application: parentRef, ChildCount: 1},
			childID:  {Object: childRef, Application: parentRef, Parent: parentRef, Index: 0},
		},
	}
	backend.applyCacheSignal(&dbus.Signal{
		Name:   cacheIface + ":RemoveAccessible",
		Sender: bus,
		Body:   []any{[]any{bus, dbus.ObjectPath("/child")}},
	})
	if _, exists := backend.cacheItems[childID]; exists {
		t.Fatal("removed child remains cached")
	}
	parent, exists := backend.cacheItems[parentID]
	if !exists || parent.ChildCount != 0 {
		t.Fatalf("parent count stale after child removal: item=%+v exists=%v", parent, exists)
	}
}

func TestCacheRemoveCountsCurrentChildOnlyOnceAcrossHistoricalIdentities(t *testing.T) {
	const bus = "org.example.Owner"
	parentRef := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath("/parent")}
	childRef := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath("/child")}
	parentID := NodeID{BusName: bus, ObjectPath: "/parent", Generation: 7, Incarnation: 1}
	childID := NodeID{BusName: bus, ObjectPath: "/child", Generation: 7, Incarnation: 1}
	oldID := NodeID{BusName: bus, ObjectPath: "/child", Generation: 6, Incarnation: 1}
	child := cacheItem{Object: childRef, Application: parentRef, Parent: parentRef}
	b := &dbusBackend{generation: 7, cacheApps: map[string]bool{bus: true}, cacheItems: map[NodeID]cacheItem{
		parentID: {Object: parentRef, Application: parentRef, ChildCount: 2},
		childID:  child,
		oldID:    child,
	}}
	b.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":RemoveAccessible", Sender: bus, Body: []any{[]any{bus, dbus.ObjectPath("/child")}}})
	if b.cacheItems[parentID].ChildCount != 1 {
		t.Fatalf("duplicate historical child decremented cached parent more than once: %+v", b.cacheItems[parentID])
	}
	if _, ok := b.cacheItems[oldID]; ok {
		t.Fatal("historical child was not retired")
	}
	if _, ok := b.cacheItems[childID]; ok {
		t.Fatal("current child was not retired")
	}
}
