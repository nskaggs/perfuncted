package accessibility

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

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
