package accessibility

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// Rebuilding a provider's child index must exclude obsolete generation
// entries, even if they share the same parent identity as live children.
// A cached empty parent must not acquire an obsolete child by accident.
func TestCachedChildrenIndexExcludesStaleGenerationAndKeepsEmptyParent(t *testing.T) {
	const bus = "org.example.CacheIsolation"
	parent := NodeID{BusName: bus, ObjectPath: "/parent", Generation: 9, Incarnation: 1}
	empty := NodeID{BusName: bus, ObjectPath: "/empty", Generation: 9, Incarnation: 1}
	current := NodeID{BusName: bus, ObjectPath: "/current", Generation: 9, Incarnation: 1}
	obsolete := NodeID{BusName: bus, ObjectPath: "/obsolete", Generation: 8, Incarnation: 1}
	parentRef := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(parent.ObjectPath)}
	emptyRef := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(empty.ObjectPath)}
	b := &dbusBackend{generation: 9, cacheApps: map[string]bool{bus: true}, cacheItems: map[NodeID]cacheItem{
		parent:   {Object: parentRef, ChildCount: 1},
		empty:    {Object: emptyRef, ChildCount: 0},
		current:  {Object: cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(current.ObjectPath)}, Parent: parentRef},
		obsolete: {Object: cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(obsolete.ObjectPath)}, Parent: emptyRef},
	}}
	if got := b.cachedChildren(empty); len(got) != 0 {
		t.Fatalf("stale child leaked into empty parent: %+v", got)
	}
	if got := b.cachedChildren(empty); len(got) != 0 {
		t.Fatalf("cached empty parent leaked stale child: %+v", got)
	}
	if got := b.cachedChildren(parent); len(got) != 1 || got[0].ObjectPath != dbus.ObjectPath(current.ObjectPath) {
		t.Fatalf("current parent index contains obsolete entry: %+v", got)
	}
	if b.cacheChildrenIndexGen != 9 {
		t.Fatalf("unexpected index generation %d", b.cacheChildrenIndexGen)
	}
}
