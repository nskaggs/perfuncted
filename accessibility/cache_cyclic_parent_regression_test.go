package accessibility

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// GetItems describes an accessibility tree. Parent cycles cannot represent a
// valid tree and must not be published as authoritative ancestry.
func TestCacheGetItemsRejectsCyclicParentTopology(t *testing.T) {
	const bus = "org.example.CyclicApp"
	mk := func(path, parent string) cacheItem {
		return cacheItem{
			Object:      cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(path)},
			Application: cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath("/application")},
			Parent:      cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(parent)},
		}
	}
	for _, tc := range []struct {
		name  string
		items []cacheItem
	}{
		{"self parent", []cacheItem{mk("/child", "/child")}},
		{"two node cycle", []cacheItem{mk("/a", "/b"), mk("/b", "/a")}},
		{"three node cycle", []cacheItem{mk("/a", "/b"), mk("/b", "/c"), mk("/c", "/a")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: 11}
			err := b.publishLoadedCache(bus, tc.items, 11, 0)
			if err == nil {
				t.Fatal("cyclic accessibility parent graph accepted as authoritative")
			}
			if b.cacheApps[bus] || len(b.cacheItems) > 0 || len(b.parents) > 0 {
				t.Fatalf("invalid topology was published: apps=%v items=%v parents=%v", b.cacheApps, b.cacheItems, b.parents)
			}
		})
	}
}
