package accessibility

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestAccessibilityBackendNilAndIdempotentLifecycleBoundary(t *testing.T) {
	var nilBackend *dbusBackend
	if got := nilBackend.Generation(); got != 0 {
		t.Fatalf("nil backend generation=%d", got)
	}
	nilBackend.Invalidate(NodeID{})
	if err := nilBackend.Close(); err != nil {
		t.Fatalf("nil backend close: %v", err)
	}
	if err := nilBackend.validateHandle(NodeID{}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil backend handle accepted: %v", err)
	}
	b := &dbusBackend{generation: 5, cacheApps: map[string]bool{"org.test.Application": true}}
	if err := b.Close(); err != nil {
		t.Fatalf("empty desktop close: %v", err)
	}
	closedGeneration := b.Generation()
	if err := b.Close(); err != nil || b.Generation() != closedGeneration {
		t.Fatalf("duplicate close affected epoch: %v generation=%d", err, b.Generation())
	}
	b.Invalidate(NodeID{})
	if err := b.validateHandle(NodeID{BusName: "org.test.Application", ObjectPath: "/node", Generation: b.Generation()}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("closed handle became live: %v", err)
	}
}

func TestAccessibilityInvalidOpenNeverUsesUnscopedSessionBus(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=:1", "XDG_CURRENT_DESKTOP=GNOME"})
	if backend, err := openRuntime(context.Background(), rt, 0); backend != nil || err == nil {
		t.Fatalf("zero-generation unscoped bus accepted: %v %v", backend, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if backend, err := openRuntime(ctx, rt, 0); backend != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled accessibility opener succeeded: %v %v", backend, err)
	}
}

func TestAccessibilityParentTrackingFailClosedAtCapacity(t *testing.T) {
	child := cacheObjectRef{BusName: "org.test.Application", ObjectPath: "/child"}
	parent := cacheObjectRef{BusName: "org.test.Application", ObjectPath: "/parent"}
	b := &dbusBackend{generation: 1}
	b.recordParentLocked(child, parent)
	id := objectIdentity{busName: child.BusName, objectPath: string(child.ObjectPath)}
	if b.parents[id] != (objectIdentity{busName: parent.BusName, objectPath: string(parent.ObjectPath)}) {
		t.Fatalf("first parent never tracked: %+v", b.parents)
	}
	if b.incarnations == nil {
		t.Fatal("parent-tracking did not initialize incarnation map")
	}
	for i := 0; i < maxTrackedObjects; i++ {
		b.parents[objectIdentity{busName: "org.test.Application", objectPath: fmt.Sprintf("/overflow/%d", i)}] = objectIdentity{busName: "org.test.Application", objectPath: "/root"}
	}
	b.recordParentLocked(cacheObjectRef{BusName: "org.test.Application", ObjectPath: "/late"}, parent)
	if !b.parentTrackingIncomplete {
		t.Fatal("oversized parent graph remained authoritative")
	}
	if _, ok := b.parents[objectIdentity{busName: "org.test.Application", objectPath: "/late"}]; ok {
		t.Fatal("unbounded graph inserted untracked parent")
	}
	before := b.generation
	b.revokeOrInvalidateLocked(objectIdentity{busName: "org.test.Application", objectPath: "/root"})
	if b.generation != before+1 || b.parents != nil || b.incarnations != nil {
		t.Fatalf("incomplete ancestry only invalidated one object: generation=%d parents=%v", b.generation, b.parents != nil)
	}
}

func TestAccessibilityToolkitCacheDoesNotResurrectStaleHandles(t *testing.T) {
	b := &dbusBackend{generation: 5, toolkits: map[string]string{"org.test.Application": "Qt"}}
	id := NodeID{BusName: "org.test.Application", ObjectPath: "/node", Generation: 5, Incarnation: 1}
	toolkit, ok, err := b.cachedToolkit(id)
	if err != nil || !ok || toolkit != "Qt" {
		t.Fatalf("valid toolkit cache lookup failed: %q %v %v", toolkit, ok, err)
	}
	if err := b.cacheToolkit(id, "GTK"); err != nil {
		t.Fatalf("live toolkit update: %v", err)
	}
	if name, err := b.toolkitForNode(context.Background(), id); err != nil || name != "GTK" {
		t.Fatalf("cached toolkit resolution unexpectedly accessed D-Bus: %q %v", name, err)
	}
	b.Invalidate(id)
	if _, ok, err := b.cachedToolkit(id); ok || !errors.Is(err, ErrStaleNode) {
		t.Fatalf("stale toolkit accepted: %v %v", ok, err)
	}
	if err := b.cacheToolkit(id, "stale"); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("stale toolkit reinserted: %v", err)
	}
	if _, err := b.toolkitForNode(context.Background(), id); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("stale toolkit resolver accessed D-Bus: %v", err)
	}
}
