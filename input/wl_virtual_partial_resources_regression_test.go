package input

import (
	"context"
	"errors"
	"testing"
)

// A previously admitted virtual-input backend must not panic or inject
// partial events if its synthetic compositor fails before a keyboard or
// pointer proxy becomes available. These are actual connected Wayland sockets.
func TestWlVirtualMissingProtocolObjectFailsClosedWithoutSendingFrames(t *testing.T) {
	for _, tc := range []struct {
		name    string
		disable func(*WlVirtualBackend)
		call    func(*WlVirtualBackend) error
	}{
		{"literal keyboard", func(b *WlVirtualBackend) { b.kbd = nil }, func(b *WlVirtualBackend) error { return b.TypeLiteral(context.Background(), "Café") }},
		{"key syntax keyboard", func(b *WlVirtualBackend) { b.kbd = nil }, func(b *WlVirtualBackend) error { return b.Type(context.Background(), "hello{ctrl+a}") }},
		{"keyboard press", func(b *WlVirtualBackend) { b.kbd = nil }, func(b *WlVirtualBackend) error { return b.KeyDown(context.Background(), "ctrl") }},
		{"keyboard release", func(b *WlVirtualBackend) { b.kbd = nil }, func(b *WlVirtualBackend) error { return b.KeyUp(context.Background(), "ctrl") }},
		{"pointer motion", func(b *WlVirtualBackend) { b.ptr = nil }, func(b *WlVirtualBackend) error { return b.MouseMove(context.Background(), 40, 50) }},
		{"pointer button press", func(b *WlVirtualBackend) { b.ptr = nil }, func(b *WlVirtualBackend) error { return b.MouseDown(context.Background(), 1) }},
		{"pointer click", func(b *WlVirtualBackend) { b.ptr = nil }, func(b *WlVirtualBackend) error { return b.MouseClick(context.Background(), 40, 50, 1) }},
		{"pointer scroll", func(b *WlVirtualBackend) { b.ptr = nil }, func(b *WlVirtualBackend) error { return b.ScrollDown(context.Background(), 3) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newCapturedWlVirtualBackend(t)
			tc.disable(b)
			defer func() {
				if issue := recover(); issue != nil {
					t.Errorf("missing proxy panicked: %v", issue)
				}
			}()
			err := tc.call(b)
			if !errors.Is(err, ErrNotSupported) {
				t.Errorf("missing device error=%v, want unsupported", err)
			}
			if err := b.Close(); err != nil {
				t.Error(err)
			}
			if wire := readCapturedWlWrites(t, conn); len(wire) != 0 {
				t.Errorf("missing device sent %d Wayland messages", len(wire))
			}
		})
	}
}
