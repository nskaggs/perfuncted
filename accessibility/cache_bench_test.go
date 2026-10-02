package accessibility

import (
	"fmt"
	"testing"

	"github.com/godbus/dbus/v5"
)

var cachedChildrenBenchmarkCount int

func BenchmarkCachedChildrenLargeCache(b *testing.B) {
	const (
		application  = "org.test.App"
		parentCount  = 256
		totalObjects = 8192
	)
	backend := &dbusBackend{
		generation: 1,
		cacheItems: make(map[NodeID]cacheItem, totalObjects),
		cacheApps:  map[string]bool{application: true},
	}
	root := cacheObjectRef{BusName: application, ObjectPath: "/app"}
	parents := make([]NodeID, parentCount)
	for i := range parentCount {
		id := NodeID{BusName: application, ObjectPath: fmt.Sprintf("/app/parent-%03d", i), Generation: 1}
		parents[i] = id
		backend.cacheItems[id] = cacheItem{
			Object:      cacheObjectRef{BusName: application, ObjectPath: dbus.ObjectPath(id.ObjectPath)},
			Application: root,
			Parent:      root,
			Index:       int32(i),
		}
	}
	childrenPerParent := (totalObjects - parentCount) / parentCount
	for _, parent := range parents {
		parentRef := cacheObjectRef{BusName: application, ObjectPath: dbus.ObjectPath(parent.ObjectPath)}
		for child := range childrenPerParent {
			path := fmt.Sprintf("%s/child-%02d", parent.ObjectPath, child)
			id := NodeID{BusName: application, ObjectPath: path, Generation: 1}
			backend.cacheItems[id] = cacheItem{
				Object:      cacheObjectRef{BusName: application, ObjectPath: dbus.ObjectPath(path)},
				Application: root,
				Parent:      parentRef,
				Index:       int32(child),
			}
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		count := 0
		for _, parent := range parents {
			count += len(backend.cachedChildren(parent))
		}
		cachedChildrenBenchmarkCount = count
	}
}
