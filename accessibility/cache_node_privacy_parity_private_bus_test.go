package accessibility

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

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
