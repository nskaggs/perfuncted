package input

import (
	"encoding/binary"
	"math"
	"testing"
)

// Scroll values are signed protocol distances. A sign or byte-order reversal
// would move the desktop in the opposite direction even when EIS accepts it.
func TestEISScrollFramePreservesSignedDistancesAndDeviceSerial(t *testing.T) {
	sender := &eiSender{client: &eiClient{}, scroll: 0x102, scrollDev: 0x203}
	sender.client.serial.Store(0x11223344)
	for _, tc := range []struct {
		name string
		x, y int32
	}{
		{"vertical up", 0, -120},
		{"vertical down", 0, 120},
		{"horizontal left", -360, 0},
		{"horizontal right", 360, 0},
		{"signed extremes", math.MinInt32, math.MaxInt32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := eiScrollFrame(sender, tc.x, tc.y)
			if err != nil {
				t.Fatal(err)
			}
			request, rest := splitEIRequest(t, encoded)
			if request.object != sender.scroll || request.opcode != 2 || len(request.body) != 8 {
				t.Fatalf("scroll request = %+v", request)
			}
			x := int32(binary.NativeEndian.Uint32(request.body[:4]))
			y := int32(binary.NativeEndian.Uint32(request.body[4:]))
			if x != tc.x || y != tc.y {
				t.Fatalf("encoded distances (%d,%d), want (%d,%d)", x, y, tc.x, tc.y)
			}
			frame, trailing := splitEIRequest(t, rest)
			if frame.object != sender.scrollDev || frame.opcode != 3 || len(frame.body) != 12 || len(trailing) != 0 {
				t.Fatalf("device frame = %+v, trailing %d bytes", frame, len(trailing))
			}
			if serial := binary.NativeEndian.Uint32(frame.body[:4]); serial != 0x11223344 {
				t.Fatalf("frame serial = %#x", serial)
			}
			if stamp := binary.NativeEndian.Uint64(frame.body[4:]); stamp == 0 {
				t.Fatal("frame missing monotonic timestamp")
			}
		})
	}
}
