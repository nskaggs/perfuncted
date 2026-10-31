package input

import "testing"

// A resumed device may still be enumerating its protocol interfaces or
// regions. A sender must not begin injecting input on an incomplete EIS
// device before the ei_device.done event seals its advertised capabilities.
func TestEISDeviceReadinessWaitsForDoneOnAllSelectedDevices(t *testing.T) {
	keyboard := &eiDevice{id: eiServerIDFloor + 0x20, version: 1, resumed: true, done: true, interfaces: map[string]uint64{"ei_keyboard": 101, "ei_button": 102}}
	pointer := &eiDevice{id: eiServerIDFloor + 0x21, version: 1, resumed: true, done: false, interfaces: map[string]uint64{"ei_pointer_absolute": 103}, regions: []eiRegion{{x: 0, y: 0, width: 200, height: 100, scale: 1}}}
	sender := &eiSender{devices: map[uint64]*eiDevice{keyboard.id: keyboard, pointer.id: pointer}}
	if sender.devicesReady() {
		t.Fatal("sender accepted resumed pointer before its device.done")
	}
	if err := sender.consumeSetupEvent(eiMessage{object: pointer.id, opcode: 6}); err != nil {
		t.Fatal(err)
	}
	if !sender.devicesReady() {
		t.Fatal("completed keyboard and pointer should be ready")
	}
	keyboard.done = false
	if sender.devicesReady() {
		t.Fatal("sender accepted incomplete keyboard before its device.done")
	}
	keyboard.done = true
	if !sender.devicesReady() {
		t.Fatal("completed keyboard and pointer should remain ready")
	}
}
