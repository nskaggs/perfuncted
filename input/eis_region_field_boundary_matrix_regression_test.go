package input

import (
	"strings"
	"testing"
)

// Every field boundary in the compositor's region advertisement is untrusted.
// A partial frame must be rejected before it can change a device's permitted
// absolute coordinate region or the device's other negotiated capabilities.
func TestEISRegionFieldTruncationNeverWidensDeviceAuthority(t *testing.T) {
	const deviceID = eiServerIDFloor + 0x151
	baseline := eiRegion{x: 4, y: 6, width: 40, height: 30, scale: 1}
	full := append(append(append(append(eiU32(11), eiU32(13)...), eiU32(100)...), eiU32(80)...), eiF32(1.25)...)
	for _, tc := range []struct {
		name   string
		length int
	}{
		{"missing x", 0}, {"missing y", 4}, {"missing width", 8}, {"missing height", 12}, {"missing scale", 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			device := &eiDevice{id: deviceID, version: 1, interfaces: map[string]uint64{"ei_keyboard": deviceID + 1}, regions: []eiRegion{baseline}}
			s := &eiSender{devices: map[uint64]*eiDevice{deviceID: device}}
			err := s.consumeSetupEvent(eiMessage{object: deviceID, opcode: 4, body: full[:tc.length]})
			if err == nil {
				t.Fatal("accepted incomplete absolute region")
			}
			if len(device.regions) != 1 || device.regions[0] != baseline || device.interfaces["ei_keyboard"] != deviceID+1 {
				t.Fatalf("malformed region changed negotiated permission: regions=%+v interfaces=%+v", device.regions, device.interfaces)
			}
		})
	}
}

// An interface announcement can be truncated before either its object ID or
// name is complete. Incomplete metadata must never grant a new input object.
func TestEISInterfaceFieldTruncationPreservesPriorAuthorization(t *testing.T) {
	const deviceID = eiServerIDFloor + 0x161
	full := append(eiU64(deviceID+12), eiString("ei_pointer_absolute")...)
	for _, tc := range []struct {
		name   string
		length int
	}{
		{"missing object", 0}, {"partial object", 4}, {"missing name length", 8}, {"partial name payload", 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			existing := map[string]uint64{"ei_keyboard": deviceID + 10}
			device := &eiDevice{id: deviceID, version: 1, interfaces: existing}
			s := &eiSender{devices: map[uint64]*eiDevice{deviceID: device}}
			err := s.consumeSetupEvent(eiMessage{object: deviceID, opcode: 5, body: full[:tc.length]})
			if err == nil || strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("incomplete interface was not rejected as invalid: %v", err)
			}
			if len(device.interfaces) != 1 || device.interfaces["ei_keyboard"] != deviceID+10 {
				t.Fatalf("incomplete interface changed authority: %+v", device.interfaces)
			}
		})
	}
}
