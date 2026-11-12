//go:build linux

package input

import (
	"encoding/binary"
	"testing"
)

// Multiple physical pointer devices can expose disjoint absolute regions.
// Their advertisement order must not change which authoritative interface
// receives a coordinate, and a gap must not be sent to either device.
func TestEISDeviceReadinessSelectsMultipleDisjointPointersDeterministically(t *testing.T) {
	const low = eiServerIDFloor + 0x80
	const high = eiServerIDFloor + 0x90
	for _, order := range [][]uint64{{high, low}, {low, high}} {
		devices := map[uint64]*eiDevice{}
		for _, id := range order {
			x := uint32(0)
			if id == high {
				x = 100
			}
			devices[id] = &eiDevice{id: id, done: true, resumed: true, interfaces: map[string]uint64{"ei_pointer_absolute": id + 1}, regions: []eiRegion{{x: x, y: 0, width: 80, height: 60, scale: 1}}}
		}
		// Full capability readiness requires keyboard and buttons as well.
		devices[low].interfaces["ei_keyboard"] = low + 2
		devices[high].interfaces["ei_button"] = high + 2
		sender := &eiSender{client: &eiClient{}, devices: devices}
		if !sender.devicesReady() {
			t.Fatalf("valid split devices were not ready: %+v", sender)
		}
		if len(sender.pointers) != 2 || sender.pointers[0].device != low || sender.pointers[1].device != high || sender.pointerDev != low || sender.keyboardDev != low || sender.buttonDev != high {
			t.Fatalf("device selection depended on announcement order %v: %+v", order, sender)
		}
		for _, tc := range []struct {
			x, y   int
			device uint64
			ok     bool
		}{{1, 1, low, true}, {79, 59, low, true}, {100, 0, high, true}, {179, 59, high, true}, {80, 1, 0, false}, {180, 1, 0, false}} {
			chosen, ok := sender.pointerForPoint(tc.x, tc.y)
			if ok != tc.ok || (ok && chosen.device != tc.device) {
				t.Fatalf("%v: point %d,%d routed to %x ok=%t", order, tc.x, tc.y, chosen.device, ok)
			}
			frame, err := eiPointerFrame(sender, tc.x, tc.y)
			if !tc.ok {
				if err == nil || frame != nil {
					t.Fatalf("%v: unowned point %d,%d got frame %x, %v", order, tc.x, tc.y, frame, err)
				}
				continue
			}
			if err != nil || len(frame) < 14 {
				t.Fatalf("%v: owned point %d,%d rejected: %v", order, tc.x, tc.y, err)
			}
			if object := binary.NativeEndian.Uint64(frame[:8]); object != tc.device+1 {
				t.Fatalf("%v: point %d,%d sent to %x, expected pointer interface %x", order, tc.x, tc.y, object, tc.device+1)
			}
		}
	}
}
