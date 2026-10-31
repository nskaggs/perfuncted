package input

import (
	"strings"
	"testing"
)

// Re-announcing an existing device ID is a protocol replay, not an update.
// Replacing its descriptor drops validated interfaces, regions and done/
// resume state, potentially corrupting negotiated input authorization.
func TestEISRepeatedDeviceAnnouncementMustNotReplaceAuthorizedDevice(t *testing.T) {
	const seat, device = eiServerIDFloor + 11, eiServerIDFloor + 21
	initial := &eiDevice{id: device, version: 1, resumed: true, done: true, interfaces: map[string]uint64{"ei_keyboard": eiServerIDFloor + 30, "ei_pointer_absolute": eiServerIDFloor + 31}, regions: []eiRegion{{x: 0, y: 0, width: 120, height: 80, scale: 1}}}
	for _, version := range []uint32{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			sender := &eiSender{seat: seat, devices: map[uint64]*eiDevice{device: initial}}
			replay := eiMessage{object: seat, opcode: 4, body: append(eiU64(device), eiU32(version)...)}
			if err := sender.consumeSetupEvent(replay); err == nil || !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("duplicate device ID %d version %d accepted: %v", device, version, err)
			}
			if sender.devices[device] != initial || !sender.devices[device].done || !sender.devices[device].resumed {
				t.Fatalf("existing authorized device replaced: %+v", sender.devices[device])
			}
		})
	}
}
