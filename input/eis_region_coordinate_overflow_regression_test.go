package input

import (
	"math"
	"testing"
)

// The compositor must not grant an absolute region whose far edge wraps
// outside the uint32 coordinate domain used to describe its origin and size.
func TestEISSetupRejectsUnrepresentableAbsoluteRegionBounds(t *testing.T) {
	cases := []struct {
		name                string
		x, y, width, height uint32
	}{
		{"x endpoint overflow", math.MaxUint32, 0, 2, 1},
		{"y endpoint overflow", 0, math.MaxUint32, 1, 2},
		{"both endpoints overflow", math.MaxUint32, math.MaxUint32, 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{devices: map[uint64]*eiDevice{42: {id: 42, interfaces: map[string]uint64{}}}}
			body := append(eiU32(tc.x), eiU32(tc.y)...)
			body = append(body, eiU32(tc.width)...)
			body = append(body, eiU32(tc.height)...)
			body = append(body, eiF32(1)...)
			if err := sender.consumeSetupEvent(eiMessage{object: 42, opcode: 4, body: body}); err == nil {
				t.Fatalf("accepted absolute region with unrepresentable boundary: %+v", tc)
			}
			if got := len(sender.devices[42].regions); got != 0 {
				t.Fatalf("invalid region acquired authorization: %d", got)
			}
		})
	}
}

// An exclusive far edge exactly at 2^32 is still representable as the
// boundary after the final uint32 coordinate and must not be mistaken for
// an overflow into a different coordinate space.
func TestEISSetupAllowsLastRepresentableCoordinate(t *testing.T) {
	sender := &eiSender{devices: map[uint64]*eiDevice{42: {id: 42, interfaces: map[string]uint64{}}}}
	body := append(eiU32(math.MaxUint32), eiU32(math.MaxUint32)...)
	body = append(body, eiU32(1)...)
	body = append(body, eiU32(1)...)
	body = append(body, eiF32(1)...)
	if err := sender.consumeSetupEvent(eiMessage{object: 42, opcode: 4, body: body}); err != nil {
		t.Fatalf("rejected final representable position: %v", err)
	}
	if got := len(sender.devices[42].regions); got != 1 {
		t.Fatalf("expected one boundary region, got %d", got)
	}
}
