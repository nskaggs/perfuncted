package accessibility

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

type invalidatingApplicationPropertyNode struct {
	*syntheticATSPINode
	beforeProperty func(string)
}

func (n *invalidatingApplicationPropertyNode) Get(iface, key string) (dbus.Variant, *dbus.Error) {
	if n.beforeProperty != nil {
		n.beforeProperty(key)
	}
	return n.syntheticATSPINode.Get(iface, key)
}

func newGenerationChangingApplication(t *testing.T, property string) (*dbusBackend, NodeID, *atomic.Int32) {
	t.Helper()
	ctx := context.Background()
	address := startTestDBus(t)
	registry, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close() })
	client, err := dbusutil.ConnectContext(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	reply, err := registry.RequestName(registryName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request AT-SPI registry name: reply=%v err=%v", reply, err)
	}

	const appPath = dbus.ObjectPath("/org/a11y/atspi/accessible/app")
	appRef := objectRef{BusName: registryName, ObjectPath: appPath}
	root := &syntheticATSPINode{children: []objectRef{appRef}}
	var liveBackend atomic.Pointer[dbusBackend]
	propertyReads := &atomic.Int32{}
	appNode := &invalidatingApplicationPropertyNode{
		syntheticATSPINode: &syntheticATSPINode{name: "Editor", role: 68, application: appRef},
		beforeProperty: func(key string) {
			if key == property && propertyReads.Add(1) == 1 {
				if backend := liveBackend.Load(); backend != nil {
					backend.Invalidate(NodeID{})
				}
			}
		},
	}
	if err := registry.Export(root, desktopPath, accessibleIface); err != nil {
		t.Fatal(err)
	}
	if err := registry.Export(appNode, appPath, accessibleIface); err != nil {
		t.Fatal(err)
	}
	if err := registry.Export(appNode, appPath, propertiesIface); err != nil {
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
	return backend, backend.refID(appRef), propertyReads
}

func TestApplicationsRetriesWhenNodeReadChangesGeneration(t *testing.T) {
	backend, _, invalidations := newGenerationChangingApplication(t, "Name")
	applications, err := backend.Applications(context.Background())
	if err != nil {
		t.Fatalf("enumerate applications after stale node read: %v", err)
	}
	if len(applications) != 1 || applications[0].Name != "Editor" || applications[0].ID.Generation != 2 {
		t.Fatalf("application from stale generation escaped enumeration: applications=%+v generation=%d", applications, backend.Generation())
	}
	if got := invalidations.Load(); got != 2 {
		t.Fatalf("application name reads=%d, want one stale observation and one current-generation retry", got)
	}
}

func TestToolkitIdentityRejectsPropertyReplyFromStaleGeneration(t *testing.T) {
	backend, id, invalidations := newGenerationChangingApplication(t, "ToolkitName")
	toolkit, err := backend.toolkitForNode(context.Background(), id)
	if !errors.Is(err, ErrStaleGeneration) || toolkit != "" {
		t.Fatalf("toolkit from stale generation was accepted: toolkit=%q err=%v", toolkit, err)
	}
	if got := invalidations.Load(); got != 1 || backend.Generation() != 2 {
		t.Fatalf("toolkit property reads=%d generation=%d, want one read and generation 2", got, backend.Generation())
	}
	if _, ok := backend.toolkits[id.BusName]; ok {
		t.Fatal("stale toolkit identity entered the generation-scoped cache")
	}
}
