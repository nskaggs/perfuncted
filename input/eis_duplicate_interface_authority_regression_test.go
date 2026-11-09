package input

import (
	"strings"
	"testing"
)

// A device's announced interface name is an immutable protocol binding.
// An unsolicited repeated or conflicting advertisement must not replace an
// already selected keyboard, button, pointer, scroll or text object.
func TestEISDeviceInterfaceCannotBeReboundByRepeatedAdvertisement(t *testing.T) {
	for _, tc := range []struct {
		name        string
		replacement uint64
	}{
		{"different interface object", eiServerIDFloor + 102},
		{"replayed interface object", eiServerIDFloor + 101},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{devices: map[uint64]*eiDevice{eiServerIDFloor + 12: {id: eiServerIDFloor + 12, version: 1, interfaces: map[string]uint64{}}}}
			device := sender.devices[eiServerIDFloor+12]
			first := eiMessage{object: device.id, opcode: 5, body: append(eiU64(eiServerIDFloor+101), eiString("ei_keyboard")...)}
			if err := sender.consumeSetupEvent(first); err != nil {
				t.Fatalf("initial keyboard authorization rejected: %v", err)
			}
			replay := eiMessage{object: device.id, opcode: 5, body: append(eiU64(tc.replacement), eiString("ei_keyboard")...)}
			err := sender.consumeSetupEvent(replay)
			if err == nil || !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("repeated interface announcement accepted: %v", err)
			}
			if device.interfaces["ei_keyboard"] != eiServerIDFloor+101 || len(device.interfaces) != 1 {
				t.Fatalf("replayed interface changed keyboard authority: %+v", device.interfaces)
			}
		})
	}
}

func TestEISRejectsZeroIdentityDeviceAndInterfaceAnnouncements(t *testing.T) {
	device := eiServerIDFloor + 12
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"zero device", append(eiU64(0), eiU32(1)...)},
		{"zero device version", append(eiU64(device), eiU32(0)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{seat: eiServerIDFloor + 11, devices: make(map[uint64]*eiDevice)}
			err := sender.consumeSetupEvent(eiMessage{object: sender.seat, opcode: 4, body: tc.body})
			if err == nil || len(sender.devices) != 0 {
				t.Fatalf("invalid device identity admitted: %v devices=%v", err, sender.devices)
			}
		})
	}
	sender := &eiSender{devices: map[uint64]*eiDevice{device: {id: device, version: 1, interfaces: make(map[string]uint64)}}}
	err := sender.consumeSetupEvent(eiMessage{object: device, opcode: 5, body: append(eiU64(0), eiString("ei_keyboard")...)})
	if err == nil || len(sender.devices[device].interfaces) != 0 {
		t.Fatalf("zero interface object admitted: %v interfaces=%v", err, sender.devices[device].interfaces)
	}
}
