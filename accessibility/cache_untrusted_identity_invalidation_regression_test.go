package accessibility

import (
	"fmt"
	"testing"

	"github.com/godbus/dbus/v5"
)

// A syntactically decodable cache signal is not evidence that its object and
// application identities are safe to retain. Invalid identities must evict
// the entire private cache rather than allowing stale content to be served.
func TestCacheAddInvalidObjectOrApplicationIdentityInvalidatesPrivateState(t *testing.T) {
	const bus = "org.example.Private"
	valid := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath("/app")}
	for _, tc := range []struct {
		name string
		item cacheItem
	}{
		{name: "relative object path", item: cacheItem{Object: cacheObjectRef{BusName: bus, ObjectPath: "relative"}, Application: valid, Parent: cacheObjectRef{ObjectPath: nullObjectPath}}},
		{name: "relative application path", item: cacheItem{Object: valid, Application: cacheObjectRef{BusName: bus, ObjectPath: "relative"}, Parent: cacheObjectRef{ObjectPath: nullObjectPath}}},
		{name: "missing object owner", item: cacheItem{Object: cacheObjectRef{ObjectPath: "/node"}, Application: cacheObjectRef{ObjectPath: "/app"}, Parent: cacheObjectRef{ObjectPath: nullObjectPath}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			existing := NodeID{BusName: bus, ObjectPath: "/secret", Generation: 12, Incarnation: 1}
			backend := &dbusBackend{
				generation: 12,
				cacheItems: map[NodeID]cacheItem{existing: {Name: "sensitive cache content"}},
				cacheApps:  map[string]bool{bus: true},
			}
			backend.applyCacheAddLocked([]any{tc.item})
			if backend.cacheItems != nil || backend.cacheApps != nil {
				t.Fatalf("invalid identity retained cache: items=%+v apps=%v", backend.cacheItems, backend.cacheApps)
			}
		})
	}
}

// Independent children can share one parent without forming an ancestry cycle.
// The validator must not reject a valid provider cache merely because multiple
// paths converge on an ancestor previously visited by another child.
func TestLoadedCacheSharedAncestryDoesNotProduceFalseCycle(t *testing.T) {
	const bus = "org.example.Private"
	root := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath("/root")}
	items := []cacheItem{{Object: root, Application: root, Parent: cacheObjectRef{ObjectPath: nullObjectPath}}}
	for i := 0; i < 64; i++ {
		child := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(fmt.Sprintf("/root/child%d", i))}
		items = append(items, cacheItem{Object: child, Application: root, Parent: root})
	}
	for i := 0; i < 12; i++ {
		if err := validateLoadedCacheItems(bus, items); err != nil {
			t.Fatalf("valid shared ancestry rejected in pass %d: %v", i, err)
		}
	}
}
