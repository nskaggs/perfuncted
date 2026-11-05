package wl

import "testing"

// Compositor-assigned new_id messages are untrusted. A malformed duplicate
// must not replace another live protocol proxy on the connection.
func TestContextSetProxyRejectsDuplicateOrNullObjectIdentities(t *testing.T) {
	ctx := &Context{}
	previous := &RawProxy{}
	ctx.Register(previous)
	replacement := &RawProxy{}
	ctx.SetProxy(previous.ID(), replacement)
	ctx.objectsMu.RLock()
	retained := ctx.objects[previous.ID()]
	ctx.objectsMu.RUnlock()
	if retained != previous {
		t.Fatalf("existing live proxy %d replaced by compositor-assigned new_id", previous.ID())
	}
	if replacement.ID() != 0 {
		t.Fatalf("rejected proxy inherited an existing protocol ID %d", replacement.ID())
	}
	zero := &RawProxy{}
	ctx.SetProxy(0, zero)
	ctx.objectsMu.RLock()
	_, registered := ctx.objects[0]
	ctx.objectsMu.RUnlock()
	if registered || zero.ID() != 0 {
		t.Fatal("reserved zero object identity registered")
	}
}

// A client-side allocation must not reuse an ID that was already registered
// as a compositor-created object, regardless of the order of events.
func TestContextRegisterSkipsExistingCompositorAssignedIdentity(t *testing.T) {
	ctx := &Context{}
	ctx.Register(&RawProxy{})
	prior := &RawProxy{}
	ctx.SetProxy(2, prior)
	next := &RawProxy{}
	ctx.Register(next)
	ctx.objectsMu.RLock()
	retained := ctx.objects[2]
	ctx.objectsMu.RUnlock()
	if retained != prior || next.ID() == 2 {
		t.Fatalf("client allocation clobbered server object 2: retained=%v next=%d", retained == prior, next.ID())
	}
}
