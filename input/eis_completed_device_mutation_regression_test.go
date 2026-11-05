package input

import (
	"strings"
	"testing"
)

// ei_device.done seals initial enumeration. A compositor cannot add new
// authoritative capability objects or regions to an already completed grant.
func TestEISCompletedDeviceRejectsLateCapabilityAnnouncements(t *testing.T) {
	const deviceID = eiServerIDFloor + 42
	for _, tc := range []struct {
		name   string
		opcode uint32
		body   []byte
	}{{"interface", 5, append(eiU64(eiServerIDFloor+55), eiString("ei_keyboard")...)}, {"region", 4, append(append(append(append(eiU32(0), eiU32(0)...), eiU32(100)...), eiU32(100)...), eiF32(1)...)}} {
		t.Run(tc.name, func(t *testing.T) {
			device := &eiDevice{id: deviceID, version: 1, done: true, resumed: true, interfaces: map[string]uint64{}}
			sender := &eiSender{devices: map[uint64]*eiDevice{deviceID: device}}
			err := sender.consumeSetupEvent(eiMessage{object: deviceID, opcode: tc.opcode, body: tc.body})
			if err == nil || !strings.Contains(err.Error(), "completed") {
				t.Fatalf("accepted late %s after device.done: %v", tc.name, err)
			}
			if len(device.interfaces) != 0 || len(device.regions) != 0 {
				t.Fatalf("late %s mutated completed grant: %+v", tc.name, device)
			}
		})
	}
}
