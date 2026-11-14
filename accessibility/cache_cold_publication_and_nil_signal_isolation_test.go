package accessibility

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// A complete snapshot arriving after cache invalidation must rebuild both
// authoritative item and application indexes and preserve the ancestry used
// for a direct child's current-generation lookup.
func TestCacheColdPublicationRestoresCompletePrivateHierarchy(t *testing.T) {
	const bus = "org.example.Editor"
	app := cacheObjectRef{BusName: bus, ObjectPath: "/app"}
	child := cacheObjectRef{BusName: bus, ObjectPath: "/app/editor"}
	b := &dbusBackend{generation: 13}
	items := []cacheItem{
		{Object: app, Application: app, Parent: cacheObjectRef{ObjectPath: nullObjectPath}, ChildCount: 1, Name: "private application"},
		{Object: child, Application: app, Parent: app, ChildCount: 0, Name: "private editor"},
	}
	if err := b.publishLoadedCache(bus, items, 13, 0); err != nil {
		t.Fatal(err)
	}
	if !b.cacheApps[bus] || len(b.cacheItems) != 2 {
		t.Fatalf("cold publication did not initialize full cache: apps=%v items=%v", b.cacheApps, b.cacheItems)
	}
	parent := NodeID{BusName: bus, ObjectPath: "/app", Generation: 13, Incarnation: 1}
	refs := b.cachedChildren(parent)
	if len(refs) != 1 || refs[0].ObjectPath != child.ObjectPath {
		t.Fatalf("cold cache ancestry missing: %+v", refs)
	}
	b.Invalidate(parent)
	if b.cacheApps != nil || b.cacheItems != nil {
		t.Fatal("invalidation retained old cache state")
	}
	if err := b.publishLoadedCache(bus, items, 14, 1); err != nil {
		t.Fatal(err)
	}
	if refs = b.cachedChildren(NodeID{BusName: bus, ObjectPath: "/app", Generation: 14, Incarnation: 1}); len(refs) != 1 || refs[0].ObjectPath != child.ObjectPath {
		t.Fatalf("reloaded cache did not restore new-generation ancestry: %+v", refs)
	}
	if refs = b.cachedChildren(parent); len(refs) != 0 {
		t.Fatalf("old-generation parent disclosed new cache: %+v", refs)
	}
}

// An absent signal must never invalidate existing cache entries. A foreign
// signal claiming a valid source must, conversely, retire private cached data.
func TestCacheNilSignalsPreserveCacheAndForeignSignalsInvalidate(t *testing.T) {
	const bus = "org.example.Editor"
	id := NodeID{BusName: bus, ObjectPath: "/private", Generation: 3, Incarnation: 1}
	b := &dbusBackend{generation: 3, cacheItems: map[NodeID]cacheItem{id: {Name: "private"}}, cacheApps: map[string]bool{bus: true}}
	b.applyCacheSignal(nil)
	var absent *dbusBackend
	absent.applyCacheSignal(&dbus.Signal{Name: "org.a11y.atspi.Cache:AddAccessible"})
	if len(b.cacheItems) != 1 || !b.cacheApps[bus] {
		t.Fatal("nil signal discarded complete cache")
	}
	b.applyCacheSignal(&dbus.Signal{Name: "org.a11y.atspi.Cache:AddAccessible", Sender: "org.spoofed.Editor", Body: []any{"malformed"}})
	if b.cacheItems != nil || b.cacheApps != nil {
		t.Fatalf("malformed foreign signal preserved private cache: %v", b.cacheItems)
	}
}
