package accessibility

import (
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// A cache lookup is an observation, and cannot disclose private children to
// a caller without context or with an obsolete generation handle.
func TestCachedAccessibilityAdmissionRejectsNilContextAndStaleHandles(t *testing.T) {
	const bus = "org.example.Private"
	live := NodeID{BusName: bus, ObjectPath: "/parent", Generation: 9, Incarnation: 1}
	stale := live
	stale.Generation = 8
	b := &dbusBackend{generation: 9, cacheApps: map[string]bool{bus: true}, cacheItems: map[NodeID]cacheItem{
		live: {Object: cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(live.ObjectPath)}, Name: "private parent"},
	}}
	if refs, err := b.childrenWithCache(nil, live, true); err == nil || len(refs) != 0 || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("nil context exposed cache: refs=%v err=%v", refs, err)
	}
	if item, ok := b.cachedItem(stale); ok || item.Name != "" {
		t.Fatalf("stale handle disclosed cache item %+v ok=%t", item, ok)
	}
	if item, ok := b.cachedItem(live); !ok || item.Name != "private parent" {
		t.Fatalf("current handle unable to read cache: %+v ok=%t", item, ok)
	}
	var nilBackend *dbusBackend
	if item, ok := nilBackend.cachedItem(live); ok || item.Name != "" {
		t.Fatalf("nil backend disclosed cache item %+v ok=%t", item, ok)
	}
	if refs := nilBackend.cachedChildren(live); len(refs) != 0 {
		t.Fatalf("nil backend leaked cached children %+v", refs)
	}
}

// Equal provider indexes are not a stable ordering authority. Break ties by
// object path to keep cache enumeration deterministic across map iteration.
func TestCachedAccessibilityChildIndexTieBreaksByObjectPath(t *testing.T) {
	const bus = "org.example.Private"
	p := NodeID{BusName: bus, ObjectPath: "/parent", Generation: 9, Incarnation: 1}
	parent := cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(p.ObjectPath)}
	b := &dbusBackend{generation: 9, cacheApps: map[string]bool{bus: true}, cacheItems: map[NodeID]cacheItem{
		p: {Object: parent, ChildCount: 2},
		{BusName: bus, ObjectPath: "/z", Generation: 9, Incarnation: 1}: {Object: cacheObjectRef{BusName: bus, ObjectPath: "/z"}, Parent: parent, Index: 0},
		{BusName: bus, ObjectPath: "/a", Generation: 9, Incarnation: 1}: {Object: cacheObjectRef{BusName: bus, ObjectPath: "/a"}, Parent: parent, Index: 0},
	}}
	for i := 0; i < 5; i++ {
		b.cacheChildrenIndex = nil
		refs := b.cachedChildren(p)
		if len(refs) != 2 || refs[0].ObjectPath != "/a" || refs[1].ObjectPath != "/z" {
			t.Fatalf("cache rebuild %d order=%+v", i, refs)
		}
	}
}

// A malformed direct signal must invalidate incomplete cache state rather
// than publish a guessed node or an untrusted ancestry relationship.
func TestCacheDirectMalformedMutationFailsClosed(t *testing.T) {
	const bus = "org.example.Private"
	for _, tc := range []struct {
		name   string
		mutate func(*dbusBackend)
	}{
		{"malformed add", func(b *dbusBackend) { b.applyCacheAddLocked([]any{"not a cache item"}) }},
		{"malformed remove", func(b *dbusBackend) { b.applyCacheRemoveLocked([]any{"not a cache reference"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: 9, cacheApps: map[string]bool{bus: true}, cacheItems: map[NodeID]cacheItem{{BusName: bus, ObjectPath: "/existing", Generation: 9, Incarnation: 1}: {Name: "private"}}}
			tc.mutate(b)
			if b.cacheItems != nil || b.cacheApps != nil {
				t.Fatalf("untrusted mutation retained cache data: items=%v apps=%v", b.cacheItems, b.cacheApps)
			}
		})
	}
}
