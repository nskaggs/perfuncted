package wl

import (
	"math"
	"testing"
)

// A server-provided object ID must not overwrite an existing proxy, even
// when client-side allocation wraps around the uint32 ID namespace.
func TestWaylandRegistryWraparoundPreservesExistingProxyIdentities(t *testing.T) {
	ctx := &Context{nextID: math.MaxUint32 - 1}
	high := &RawProxy{}
	ctx.Register(high)
	if high.ID() != math.MaxUint32 {
		t.Fatalf("highest client object ID=%d", high.ID())
	}
	low := &RawProxy{}
	ctx.Register(low)
	if low.ID() != 1 {
		t.Fatalf("wraparound object ID=%d, want 1", low.ID())
	}
	occupied := &RawProxy{}
	ctx.SetProxy(2, occupied)
	next := &RawProxy{}
	ctx.Register(next)
	if next.ID() != 3 {
		t.Fatalf("registration overwrote occupied object ID: got %d", next.ID())
	}
	intruder := &RawProxy{}
	ctx.SetProxy(high.ID(), intruder)
	if ctx.objects[high.ID()] != high || intruder.Ctx() != nil {
		t.Fatal("server-assigned ID replaced existing live proxy")
	}
	ctx.Unregister(intruder)
	if ctx.objects[high.ID()] != high {
		t.Fatal("unregistered unowned proxy evicted existing object")
	}
	ctx.Unregister(high)
	if _, found := ctx.objects[math.MaxUint32]; found {
		t.Fatal("destroyed proxy survived unregister")
	}
	if ctx.objects[low.ID()] != low || ctx.objects[occupied.ID()] != occupied || ctx.objects[next.ID()] != next {
		t.Fatal("registration or unregistration corrupted unrelated objects")
	}
}

// Stale teardown must not remove a later object that legitimately reused
// the same Wayland ID; nil and zero-ID requests have no registry authority.
func TestWaylandRegistryIgnoresStaleTeardownAndZeroIDs(t *testing.T) {
	ctx := &Context{}
	ctx.SetProxy(0, &RawProxy{})
	if len(ctx.objects) != 0 {
		t.Fatal("server registered reserved object ID zero")
	}
	var nilCtx *Context
	nilCtx.Unregister(&RawProxy{})
	ctx.Unregister(nil)
	Unregister(nil, &RawProxy{})
	Unregister(ctx, nil)
	old := &RawProxy{}
	ctx.SetProxy(9, old)
	newer := &RawProxy{}
	ctx.Unregister(old)
	ctx.SetProxy(9, newer)
	ctx.Unregister(old)
	if ctx.objects[9] != newer {
		t.Fatal("stale teardown evicted replacement object")
	}
	ctx.Unregister(newer)
	if _, exists := ctx.objects[9]; exists {
		t.Fatal("replacement teardown did not retire object")
	}
}
