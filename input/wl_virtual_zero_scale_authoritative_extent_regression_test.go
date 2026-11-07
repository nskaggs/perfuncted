//go:build linux

package input

import (
	"context"
	"testing"
)

// The compositor cannot advertise scale zero. A malformed event after a
// verified non-unit scale must not reset logical geometry or widen the region
// where the virtual pointer is authorized to move.
func TestWlVirtualZeroScaleCannotOverrideLastVerifiedLogicalExtent(t *testing.T) {
	b, conn := newCapturedWlVirtualBackend(t)
	b.applyOutputMode(3840, 2160)
	b.applyOutputScale(2)
	b.applyOutputScale(0)
	w, h := b.outputExtent()
	if w != 1920 || h != 1080 {
		t.Fatalf("invalid zero scale widened logical extent to %dx%d", w, h)
	}
	if err := b.MouseMove(context.Background(), 2100, 500); err == nil {
		t.Fatal("zero scale authorized motion outside verified logical output")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if writes := readCapturedWlWrites(t, conn); len(writes) != 0 {
		t.Fatalf("unverified movement emitted %d Wayland requests", len(writes))
	}
}
