package accessibility

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

const syntheticCacheService = "org.example.PerfunctedCache"

type syntheticCacheProvider struct {
	mu       sync.Mutex
	items    []cacheItem
	calls    int
	rejected bool
}

func (p *syntheticCacheProvider) GetItems() ([]cacheItem, *dbus.Error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.rejected {
		return nil, dbus.MakeFailedError(errors.New("synthetic GetItems temporarily unavailable"))
	}
	out := make([]cacheItem, len(p.items))
	copy(out, p.items)
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
	if err := service.Export(provider, cachePath, cacheIface); err != nil {
		t.Fatal(err)
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
	if err := b.loadCache(nil, syntheticCacheService); err == nil {
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
