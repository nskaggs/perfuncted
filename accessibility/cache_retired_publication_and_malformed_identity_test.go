package accessibility

import (
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

// A provider reply that arrives after a generation is retired cannot
// republish old private object handles or erase the current cache.
func TestCachePublicationRefusesRetiredBackendGeneration(t *testing.T) {
	const bus = "org.example.Editor"
	existingID := NodeID{BusName: bus, ObjectPath: "/current", Generation: 9, Incarnation: 1}
	existing := cacheItem{Object: cacheObjectRef{BusName: bus, ObjectPath: "/current"}, Name: "current-private"}
	incoming := cacheItem{Object: cacheObjectRef{BusName: bus, ObjectPath: "/old"}, Application: cacheObjectRef{BusName: bus, ObjectPath: "/old"}, Parent: cacheObjectRef{ObjectPath: nullObjectPath}}
	for _, tc := range []struct {
		name                 string
		closed, disconnected bool
		generation           uint64
	}{
		{name: "generation changed", generation: 10},
		{name: "backend closed", generation: 9, closed: true},
		{name: "transport disconnected", generation: 9, disconnected: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: tc.generation, closed: tc.closed, disconnected: tc.disconnected, cacheItems: map[NodeID]cacheItem{existingID: existing}, cacheApps: map[string]bool{bus: true}}
			if err := b.publishLoadedCache(bus, []cacheItem{incoming}, 9, 0); !errors.Is(err, ErrStaleGeneration) {
				t.Fatalf("retired cache accepted: %v", err)
			}
			if got, ok := b.cacheItems[existingID]; !ok || got.Name != existing.Name || len(b.cacheItems) != 1 || !b.cacheApps[bus] {
				t.Fatalf("retired reply damaged existing cache: %+v apps=%v", b.cacheItems, b.cacheApps)
			}
		})
	}
}

// Even a correctly shaped signal sender is not permission to store an
// invalid object identity. Invalidating the entire cache avoids stale data.
func TestCacheDirectAddRejectsMalformedProviderIdentity(t *testing.T) {
	const bus = "org.example.Editor"
	app := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath("/app")}
	for _, tc := range []struct {
		name string
		item cacheItem
	}{
		{name: "foreign application", item: cacheItem{Object: app, Application: cacheObjectRef{BusName: "org.other.Editor", ObjectPath: "/app"}, Parent: cacheObjectRef{ObjectPath: nullObjectPath}}},
		{name: "invalid parent path", item: cacheItem{Object: app, Application: app, Parent: cacheObjectRef{BusName: bus, ObjectPath: "relative"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: 9, cacheItems: map[NodeID]cacheItem{{BusName: bus, ObjectPath: "/private", Generation: 9, Incarnation: 1}: {Name: "private"}}, cacheApps: map[string]bool{bus: true}}
			b.applyCacheAddLocked([]any{tc.item})
			if b.cacheItems != nil || b.cacheApps != nil {
				t.Fatalf("invalid provider item survived: %+v, %+v", b.cacheItems, b.cacheApps)
			}
		})
	}
}
