package accessibility

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// Callers that process one child enumeration must not be able to rewrite the
// indexed object identities subsequently returned to another observation.
func TestCachedChildrenResultsCannotOverwriteSharedIndex(t *testing.T) {
	const bus = "org.example.PrivateCache"
	parent := NodeID{BusName: bus, ObjectPath: "/parent", Generation: 9, Incarnation: 1}
	child := NodeID{BusName: bus, ObjectPath: "/parent/secret", Generation: 9, Incarnation: 1}
	b := &dbusBackend{
		generation: 9,
		cacheApps:  map[string]bool{bus: true},
		cacheItems: map[NodeID]cacheItem{
			parent: {Object: cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(parent.ObjectPath)}, ChildCount: 1},
			child:  {Object: cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(child.ObjectPath)}, Parent: cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(parent.ObjectPath)}},
		},
	}
	for i := range 2 {
		refs := b.cachedChildren(parent)
		if len(refs) != 1 || refs[0].ObjectPath != dbus.ObjectPath(child.ObjectPath) {
			t.Fatalf("lookup %d lost original child identity: %+v", i, refs)
		}
		refs[0].ObjectPath = dbus.ObjectPath("/unrelated")
	}
	restored := b.cachedChildren(parent)
	if len(restored) != 1 || restored[0].ObjectPath != dbus.ObjectPath(child.ObjectPath) {
		t.Fatalf("caller mutation poisoned cached ancestry: %+v", restored)
	}
}
