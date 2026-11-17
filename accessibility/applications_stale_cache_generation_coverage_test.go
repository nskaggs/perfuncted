package accessibility

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

func TestApplicationsDiscardsCacheResponseFromStaleGeneration(t *testing.T) {
	ctx := context.Background()
	address := startTestDBus(t)
	registry, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	cacheService, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cacheService.Close() })
	client, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	for _, request := range []struct {
		connection *dbus.Conn
		name       string
	}{
		{connection: registry, name: registryName},
		{connection: cacheService, name: syntheticCacheService},
	} {
		reply, requestErr := request.connection.RequestName(request.name, dbus.NameFlagDoNotQueue)
		if requestErr != nil || reply != dbus.RequestNameReplyPrimaryOwner {
			t.Fatalf("request D-Bus name %s: reply=%v err=%v", request.name, reply, requestErr)
		}
	}

	const appPath = dbus.ObjectPath("/org/a11y/atspi/accessible/app")
	appRef := objectRef{BusName: syntheticCacheService, ObjectPath: appPath}
	root := &syntheticATSPINode{children: []objectRef{appRef}}
	appNode := &syntheticATSPINode{name: "Editor", role: 68}
	if err := registry.Export(root, desktopPath, accessibleIface); err != nil {
		t.Fatal(err)
	}
	if err := cacheService.Export(appNode, appPath, accessibleIface); err != nil {
		t.Fatal(err)
	}
	if err := cacheService.Export(appNode, appPath, propertiesIface); err != nil {
		t.Fatal(err)
	}
	item := cacheItem{
		Object:      cacheObjectRef{BusName: syntheticCacheService, ObjectPath: appPath},
		Application: cacheObjectRef{BusName: syntheticCacheService, ObjectPath: appPath},
		Parent:      cacheObjectRef{ObjectPath: nullObjectPath},
		Name:        "Editor",
		Role:        68,
	}
	provider := &syntheticCacheProvider{items: []cacheItem{item}}
	var cacheReads atomic.Int32
	var liveBackend atomic.Pointer[dbusBackend]
	provider.beforeReturn = func() {
		if cacheReads.Add(1) == 1 {
			liveBackend.Load().Invalidate(NodeID{})
		}
	}
	if err := cacheService.Export(provider, cachePath, cacheIface); err != nil {
		t.Fatal(err)
	}
	backend := &dbusBackend{
		generation:   1,
		access:       client,
		cache:        make(map[string]cachedSnapshot),
		cacheItems:   make(map[NodeID]cacheItem),
		cacheApps:    make(map[string]bool),
		incarnations: make(map[objectIdentity]uint64),
		parents:      make(map[objectIdentity]objectIdentity),
		toolkits:     make(map[string]string),
	}
	liveBackend.Store(backend)

	applications, err := backend.Applications(ctx)
	if err != nil {
		t.Fatalf("enumerate applications after stale cache response: %v", err)
	}
	if len(applications) != 1 || applications[0].Name != "Editor" || applications[0].ID.Generation != 2 {
		t.Fatalf("stale cache response escaped enumeration: applications=%+v generation=%d", applications, backend.Generation())
	}
	if reads := cacheReads.Load(); reads != 2 {
		t.Fatalf("Cache.GetItems calls=%d, want one discarded response and one current-generation retry", reads)
	}
	if !backend.cacheApps[syntheticCacheService] {
		t.Fatal("current-generation cache response was not published")
	}
}
