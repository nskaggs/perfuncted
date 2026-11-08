package wl

import (
	"context"
	"testing"
)

// Invalid shared-memory pool parameters must fail locally before a protocol
// object ID is consumed or a malformed request reaches the compositor.
func TestShmCreatePoolRejectsInvalidParametersBeforeRegistration(t *testing.T) {
	for _, tc := range []struct {
		name string
		fd   int
		size int32
	}{
		{"negative FD", -1, 4096}, {"negative pool size", 0, -1}, {"zero pool size", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &Context{nextID: 1}
			shm := &Shm{}
			shm.SetCtx(ctx)
			shm.SetID(12)
			pool, err := shm.CreatePoolContext(context.Background(), tc.fd, tc.size)
			if err == nil || pool != nil || len(ctx.objects) != 0 || ctx.nextID != 1 {
				t.Fatalf("invalid pool accepted or registered: pool=%p err=%v registry=%v nextID=%d", pool, err, ctx.objects, ctx.nextID)
			}
		})
	}
}

func TestShmCreateBufferRejectsInvalidGeometryBeforeRegistration(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		offset, width, height, stride int32
	}{
		{"negative offset", -1, 2, 2, 8}, {"zero width", 0, 0, 2, 8},
		{"negative width", 0, -1, 2, 8}, {"zero height", 0, 2, 0, 8},
		{"negative height", 0, 2, -1, 8}, {"zero stride", 0, 2, 2, 0},
		{"negative stride", 0, 2, 2, -8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &Context{nextID: 1}
			pool := &ShmPool{}
			pool.SetCtx(ctx)
			pool.SetID(14)
			buf, err := pool.CreateBufferContext(context.Background(), tc.offset, tc.width, tc.height, tc.stride, 0)
			if err == nil || buf != nil || len(ctx.objects) != 0 || ctx.nextID != 1 {
				t.Fatalf("invalid buffer accepted or registered: buf=%p err=%v registry=%v nextID=%d", buf, err, ctx.objects, ctx.nextID)
			}
		})
	}
}
