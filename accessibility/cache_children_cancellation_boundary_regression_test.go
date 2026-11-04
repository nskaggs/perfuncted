package accessibility

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
)

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
