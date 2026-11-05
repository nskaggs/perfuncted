package accessibility

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// Cache.GetItems describes each accessible object once. Conflicting entries
// must not silently overwrite the parent of an authorized object and make a
// partial or malformed provider snapshot authoritative.
func TestCacheGetItemsRejectsDuplicateObjectIdentityWithoutPublishing(t *testing.T) {
	const bus = "org.example.PrivateApp"
	parentA := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath("/a")}
	parentB := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath("/b")}
	base := cacheItem{
		Object:      cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath("/sensitive")},
		Application: cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath("/app")},
		Parent:      parentA,
		Index:       0,
	}
	collision := base
	collision.Parent = parentB
	backend := &dbusBackend{generation: 7}
	if err := backend.publishLoadedCache(bus, []cacheItem{base, collision}, 7, backend.cacheRevision); err == nil {
		t.Fatal("conflicting duplicate Cache.GetItems object identities were promoted")
	}
	if backend.cacheApps[bus] || len(backend.cacheItems) != 0 || len(backend.parents) != 0 {
		t.Fatalf("invalid complete cache became authoritative: apps=%v items=%v parents=%v", backend.cacheApps, backend.cacheItems, backend.parents)
	}
}
