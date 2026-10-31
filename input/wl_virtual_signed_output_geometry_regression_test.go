package input

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// wl_output.scale/width/height are signed 32-bit protocol integers. A
// malformed negative wire number cannot become an enormous valid extent,
// override a previously confirmed geometry, or authorize out-of-bounds input.
func TestWlVirtualRejectsNegativeSignedGeometryWithoutChangingLastValidExtent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*WlVirtualBackend)
	}{
		{"negative scale -1", func(b *WlVirtualBackend) { b.applyOutputScale(^uint32(0)) }},
		{"negative scale minimum", func(b *WlVirtualBackend) { b.applyOutputScale(1 << 31) }},
		{"negative width", func(b *WlVirtualBackend) { b.applyOutputMode(^uint32(0), 1080) }},
		{"negative height", func(b *WlVirtualBackend) { b.applyOutputMode(1920, ^uint32(0)) }},
		{"zero width", func(b *WlVirtualBackend) { b.applyOutputMode(0, 1080) }},
		{"zero height", func(b *WlVirtualBackend) { b.applyOutputMode(1920, 0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &WlVirtualBackend{}
			b.applyOutputMode(1920, 1080)
			b.applyOutputScale(2)
			tc.apply(b)
			w, h := b.outputExtent()
			if w != 960 || h != 540 {
				t.Fatalf("invalid signed Wayland geometry changed confirmed 960x540 extent to %dx%d", w, h)
			}
		})
	}
}

func TestWlVirtualUntrustedScaleCannotAuthorizeOutOfBoundsPointerMove(t *testing.T) {
	b, conn := newCapturedWlVirtualBackend(t)
	b.applyOutputMode(1920, 1080)
	b.applyOutputScale(^uint32(0))
	if err := b.MouseMove(context.Background(), 2048, 500); err == nil {
		t.Fatal("malformed Wayland scale authorized out-of-bounds pointer motion")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if writes := readCapturedWlWrites(t, conn); len(writes) > 0 {
		t.Fatalf("out-of-bounds motion sent protocol messages: %d", len(writes))
	}
}

func TestWlVirtualOutputModeWireUsesSignedDimensions(t *testing.T) {
	mode := make([]byte, 16)
	wl.PutUint32(mode[4:8], ^uint32(0))
	wl.PutUint32(mode[8:12], 1080)
	decoded, ok := wl.DecodeOutputMode(mode)
	if !ok {
		t.Fatal("could not parse synthetic Wayland mode")
	}
	b := &WlVirtualBackend{}
	b.applyOutputMode(1920, 1080)
	b.applyOutputMode(decoded.Width, decoded.Height)
	w, h := b.outputExtent()
	if w != 1920 || h != 1080 {
		t.Fatalf("signed protocol width corrupted extent: %dx%d", w, h)
	}
}
