package accessibility

import (
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/internal/dbusutil"
	"strings"
	"sync"
	"testing"
	"time"
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

// Even the in-memory provider cache is an accessibility observation: an
// already canceled caller has no permission to receive private node handles.
func TestATSPIChildrenCacheDoesNotReturnPrivateHandlesAfterCancellation(t *testing.T) {
	parent := NodeID{BusName: "org.example.Private", ObjectPath: "/parent", Generation: 7, Incarnation: 1}
	child := NodeID{BusName: parent.BusName, ObjectPath: "/private/document", Generation: 7, Incarnation: 1}
	backend := &dbusBackend{generation: 7, cacheApps: map[string]bool{parent.BusName: true}, cacheItems: map[NodeID]cacheItem{
		parent: {Object: cacheObjectRef{BusName: parent.BusName, ObjectPath: dbus.ObjectPath(parent.ObjectPath)}, Name: "Secret parent"},
		child:  {Object: cacheObjectRef{BusName: child.BusName, ObjectPath: dbus.ObjectPath(child.ObjectPath)}, Parent: cacheObjectRef{BusName: parent.BusName, ObjectPath: dbus.ObjectPath(parent.ObjectPath)}, Index: 0, Name: "Secret child"},
	}}
	children, err := backend.children(context.Background(), parent)
	if err != nil || len(children) != 1 || string(children[0].ObjectPath) != child.ObjectPath {
		t.Fatalf("valid cache children not available: %+v err=%v", children, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, mode := range []struct {
		name string
		run  func() ([]objectRef, error)
	}{
		{"cached lookup", func() ([]objectRef, error) { return backend.children(ctx, parent) }},
	} {
		t.Run(mode.name, func(t *testing.T) {
			actual, err := mode.run()
			if !errors.Is(err, context.Canceled) || len(actual) != 0 {
				t.Fatalf("canceled lookup leaked cached node handles: children=%+v err=%v", actual, err)
			}
		})
	}
}

type cancelAfterFirstCacheContextCheck struct {
	context.Context //nolint:containedctx // synthetic context triggers the precise cached-read cancellation boundary.
	cancel          context.CancelFunc
	once            sync.Once
}

func (c *cancelAfterFirstCacheContextCheck) Err() error {
	err := c.Context.Err()
	c.once.Do(c.cancel)
	return err
}

// A cancellation after the initial admission check must not publish cached
// accessibility identities, even when no provider D-Bus read is necessary.
func TestATSPIWarmChildrenCacheHonorsCancellationAtLookupBoundary(t *testing.T) {
	const bus = "org.example.Private"
	parent := NodeID{BusName: bus, ObjectPath: "/parent", Generation: 4, Incarnation: 1}
	child := NodeID{BusName: bus, ObjectPath: "/parent/private", Generation: 4, Incarnation: 1}
	b := &dbusBackend{generation: 4, cacheApps: map[string]bool{bus: true}, cacheItems: map[NodeID]cacheItem{
		parent: {Object: cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(parent.ObjectPath)}, ChildCount: 1},
		child:  {Object: cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(child.ObjectPath)}, Parent: cacheObjectRef{BusName: bus, ObjectPath: dbus.ObjectPath(parent.ObjectPath)}},
	}}
	found, err := b.children(context.Background(), parent)
	if err != nil || len(found) != 1 {
		t.Fatalf("cache baseline: %+v %v", found, err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterFirstCacheContextCheck{Context: base, cancel: cancel}
	refs, err := b.children(ctx, parent)
	if !errors.Is(err, context.Canceled) || len(refs) != 0 {
		t.Fatalf("canceled cached read leaked private object handles: %+v %v", refs, err)
	}
}

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

func TestATSPIAlreadyLoadedCacheRejectsCanceledCaller(t *testing.T) {
	b := &dbusBackend{generation: 7, cacheItems: map[NodeID]cacheItem{}, cacheApps: map[string]bool{"org.example.Private": true}}
	if err := b.loadCache(context.Background(), "org.example.Private"); err != nil {
		t.Fatalf("valid loaded cache refused: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.loadCache(ctx, "org.example.Private"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cache lookup reported success: %v", err)
	}
}

// Cache.GetItems is authoritative for accessible identity and state only
// while its generation is live. A protected cached state must trigger the
// same redaction rules as the fresh D-Bus provider path; a caller opting for
// fresh evidence must not accidentally use cached display names or states.
func TestATSPIPrivateBusCachedProtectedMetadataRedactionAndFreshBypass(t *testing.T) {
	b, id, _ := newOptionalATSPINode(t)
	b.cacheItems[id] = cacheItem{
		Object: cacheObjectRef{BusName: id.BusName, ObjectPath: dbus.ObjectPath(id.ObjectPath)},
		Name:   "cached private title", Description: "cached description", Role: 43,
		States: []uint32{0, 1 << 2}, ChildCount: -12,
		Interfaces: []string{valueIface, actionIface, selectionIface, tableIface, documentIface},
	}
	cached, err := b.readNode(context.Background(), id, NodeID{}, 40, false)
	if err != nil {
		t.Fatal(err)
	}
	if cached.Name != "cached private title" || cached.Description != "cached description" || cached.ChildCount != 0 || !cached.Redacted || cached.Value != nil || cached.Attributes["password"] != "" || cached.Attributes["text-value"] != "" {
		t.Fatalf("cached protected node lost metadata or leaked values: %+v", cached)
	}
	fresh, err := b.readNodeFresh(context.Background(), id, NodeID{}, 40, false)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Redacted || fresh.Name == cached.Name || fresh.Value == nil {
		t.Fatalf("fresh metadata did not bypass stale cached identity/state: %+v", fresh)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	item, err := b.readNode(ctx, id, NodeID{}, 40, false)
	if !errors.Is(err, context.Canceled) || item.Name != "" {
		t.Fatalf("canceled cached read disclosed metadata: %+v err=%v", item, err)
	}
}

const syntheticCacheService = "org.example.PerfunctedCache"

type syntheticCacheProvider struct {
	mu           sync.Mutex
	items        []cacheItem
	calls        int
	rejected     bool
	beforeReturn func()
}

func (p *syntheticCacheProvider) GetItems() ([]cacheItem, *dbus.Error) {
	p.mu.Lock()
	p.calls++
	if p.rejected {
		p.mu.Unlock()
		return nil, dbus.MakeFailedError(errors.New("synthetic GetItems temporarily unavailable"))
	}
	out := make([]cacheItem, len(p.items))
	copy(out, p.items)
	beforeReturn := p.beforeReturn
	p.mu.Unlock()
	if beforeReturn != nil {
		beforeReturn()
	}
	return out, nil
}
func newPrivateCacheBackend(t *testing.T, items []cacheItem) (*dbusBackend, *syntheticCacheProvider) {
	t.Helper()
	addr := startTestDBus(t)
	service, err := dbusutil.ConnectContext(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(syntheticCacheService, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("private cache name %v %v", reply, err)
	}
	provider := &syntheticCacheProvider{items: items}
	if lintShadow47 := service.Export(provider, cachePath, cacheIface); lintShadow47 != nil {
		t.Fatal(lintShadow47)
	}
	client, err := dbusutil.ConnectContext(context.Background(), addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &dbusBackend{generation: 12, access: client, cacheItems: make(map[NodeID]cacheItem), cacheApps: make(map[string]bool)}, provider
}
func syntheticCacheItems() []cacheItem {
	app := cacheObjectRef{BusName: syntheticCacheService, ObjectPath: "/app"}
	child := cacheObjectRef{BusName: syntheticCacheService, ObjectPath: "/app/editor"}
	return []cacheItem{
		{Object: app, Application: app, Parent: cacheObjectRef{ObjectPath: nullObjectPath}, Index: 0, ChildCount: 1, Name: "app", Role: 75},
		{Object: child, Application: app, Parent: app, Index: 0, ChildCount: 0, Name: "editor", Role: 43},
	}
}
func TestATSPIPrivateBusCacheGetItemsPublishesCompleteAncestryAndReusesRead(t *testing.T) {
	b, provider := newPrivateCacheBackend(t, syntheticCacheItems())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.loadCache(ctx, syntheticCacheService); err != nil {
		t.Fatalf("load real private cache response: %v", err)
	}
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 1 || !b.cacheApps[syntheticCacheService] || len(b.cacheItems) != 2 {
		t.Fatalf("cache get-items not authoritative calls=%d apps=%v items=%v", calls, b.cacheApps, b.cacheItems)
	}
	parentID := b.nodeIDLocked(objectRef{BusName: syntheticCacheService, ObjectPath: "/app"})
	children := b.cachedChildren(parentID)
	if len(children) != 1 || children[0].ObjectPath != "/app/editor" {
		t.Fatalf("loaded ancestry lost real child: %+v", children)
	}
	if err := b.loadCache(ctx, syntheticCacheService); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	calls = provider.calls
	provider.mu.Unlock()
	if calls != 1 {
		t.Fatalf("already loaded complete cache performed %d additional requests", calls-1)
	}
	if err := b.publishLoadedCache(syntheticCacheService, nil, 12, 0); err != nil {
		t.Fatalf("replace old complete cache: %v", err)
	}
	if len(b.cacheItems) != 0 {
		t.Fatal("complete empty cache still exposed stale children")
	}
	if err := b.loadCache(ctx, syntheticCacheService); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	calls = provider.calls
	provider.mu.Unlock()
	if calls != 1 {
		t.Fatal("loaded empty cache must not cause repeated provider requests")
	}
}
func TestATSPIPrivateBusCacheGetItemsFailureNeverPublishesPartialCache(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*syntheticCacheProvider)
	}{
		{"method rejects", func(p *syntheticCacheProvider) { p.rejected = true }},
		{"foreign object", func(p *syntheticCacheProvider) { p.items[1].Object.BusName = "org.spoofed.App" }},
		{"foreign application", func(p *syntheticCacheProvider) { p.items[1].Application.BusName = "org.spoofed.App" }},
		{"malformed path", func(p *syntheticCacheProvider) { p.items[1].Object.ObjectPath = "relative" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, p := newPrivateCacheBackend(t, syntheticCacheItems())
			p.mu.Lock()
			tc.change(p)
			p.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := b.loadCache(ctx, syntheticCacheService)
			if err == nil {
				t.Fatalf("malformed provider %s published successful cache", tc.name)
			}
			if b.cacheApps[syntheticCacheService] || len(b.cacheItems) > 0 {
				t.Fatalf("malformed provider published partial cache %+v", b.cacheItems)
			}
		})
	}
}
func TestATSPIPrivateBusCacheLoadRejectsInvalidTargetsAndConnectionLifetime(t *testing.T) {
	b, _ := newPrivateCacheBackend(t, syntheticCacheItems())
	if err := b.loadCache(absentCacheContext, syntheticCacheService); err == nil {
		t.Fatal("nil context accepted")
	}
	for _, name := range []string{"", registryName, "   "} {
		if err := b.loadCache(context.Background(), name); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("invalid cache target %q: %v", name, err)
		}
	}
	if err := (*dbusBackend)(nil).loadCache(context.Background(), syntheticCacheService); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("nil backend cache: %v", err)
	}
	b.access = nil
	if err := b.loadCache(context.Background(), syntheticCacheService); err == nil {
		t.Fatal("closed D-Bus cache accepted")
	}
}

// An intentionally absent context exercises the public nil-context boundary.
var absentCacheContext context.Context

// Cache removal must eliminate every cached identity for the exact provider
// and object path, not just the current incarnation or one stale generation.
// A different application exposing the same object path must remain intact.
func TestCacheRemoveInvalidatesAllMatchingHandleIdentitiesOnly(t *testing.T) {
	const own = "org.example.Owner"
	const other = "org.example.Other"
	owned := cacheItem{Object: cacheObjectRef{BusName: own, ObjectPath: "/shared"}, Application: cacheObjectRef{BusName: own, ObjectPath: "/app"}}
	foreign := cacheItem{Object: cacheObjectRef{BusName: other, ObjectPath: "/shared"}, Application: cacheObjectRef{BusName: other, ObjectPath: "/app"}}
	sibling := cacheItem{Object: cacheObjectRef{BusName: own, ObjectPath: "/sibling"}, Application: cacheObjectRef{BusName: own, ObjectPath: "/app"}}
	b := &dbusBackend{
		generation: 7,
		cacheItems: map[NodeID]cacheItem{
			{BusName: own, ObjectPath: "/shared", Generation: 6, Incarnation: 1}:   owned,
			{BusName: own, ObjectPath: "/shared", Generation: 7, Incarnation: 1}:   owned,
			{BusName: own, ObjectPath: "/shared", Generation: 7, Incarnation: 2}:   owned,
			{BusName: other, ObjectPath: "/shared", Generation: 7, Incarnation: 1}: foreign,
			{BusName: own, ObjectPath: "/sibling", Generation: 7, Incarnation: 1}:  sibling,
		},
		cacheApps: map[string]bool{own: true, other: true},
	}
	// The AT-SPI payload carries only the object identity, not its generation.
	b.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":RemoveAccessible", Sender: own, Body: []any{[]any{own, dbus.ObjectPath("/shared")}}})
	for id := range b.cacheItems {
		if id.BusName == own && id.ObjectPath == "/shared" {
			t.Fatalf("removed object identity leaked: %+v", id)
		}
	}
	if len(b.cacheItems) != 2 {
		t.Fatalf("removed wrong cache entries: %+v", b.cacheItems)
	}
	if _, ok := b.cacheItems[NodeID{BusName: other, ObjectPath: "/shared", Generation: 7, Incarnation: 1}]; !ok {
		t.Fatal("other application cache removed")
	}
	if _, ok := b.cacheItems[NodeID{BusName: own, ObjectPath: "/sibling", Generation: 7, Incarnation: 1}]; !ok {
		t.Fatal("sibling cache removed")
	}
	if !b.cacheApps[own] || !b.cacheApps[other] {
		t.Fatalf("unrelated cache completeness lost: %+v", b.cacheApps)
	}
}

func TestCacheRemoveMalformedOrSpoofedSignalInvalidatesUntrustedState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sender string
		body   []any
	}{
		{"missing body", "org.example.Owner", nil},
		{"invalid path", "org.example.Owner", []any{[]any{"org.example.Owner", dbus.ObjectPath("relative")}}},
		{"foreign sender", "org.example.Owner", []any{[]any{"org.example.Other", dbus.ObjectPath("/shared")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: 7, cacheItems: map[NodeID]cacheItem{{BusName: "org.example.Owner", ObjectPath: "/shared", Generation: 7}: {}}, cacheApps: map[string]bool{"org.example.Owner": true}}
			b.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":RemoveAccessible", Sender: tc.sender, Body: tc.body})
			if b.cacheItems != nil || b.cacheApps != nil {
				t.Fatalf("untrusted cache survived malformed removal: items=%v apps=%v", b.cacheItems, b.cacheApps)
			}
		})
	}
}

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
func TestCacheSignalRejectsMalformedProviderIdentity(t *testing.T) {
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
			b.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: bus, Body: []any{tc.item}})
			if b.cacheItems != nil || b.cacheApps != nil {
				t.Fatalf("invalid provider item survived: %+v, %+v", b.cacheItems, b.cacheApps)
			}
		})
	}
}

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

// Malformed cache signals invalidate local state rather than publish a
// guessed node or untrusted ancestry relationship.
func TestCacheMalformedSignalsFailClosed(t *testing.T) {
	const bus = "org.example.Private"
	for _, tc := range []struct {
		name   string
		mutate func(*dbusBackend)
	}{
		{"malformed add", func(b *dbusBackend) {
			b.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: bus, Body: []any{"not a cache item"}})
		}},
		{"malformed remove", func(b *dbusBackend) {
			b.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":RemoveAccessible", Sender: bus, Body: []any{"not a cache reference"}})
		}},
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
			backend.applyCacheSignal(&dbus.Signal{Name: cacheIface + ":AddAccessible", Sender: bus, Body: []any{tc.item}})
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
