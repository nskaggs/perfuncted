package input

import (
	"strings"
	"testing"
)

// A compositor may not authorize two unrelated input interfaces using the
// same protocol object ID. Otherwise messages for one interface may be sent
// with opcodes belonging to another.
func TestEISRejectsAliasedInterfaceObjectAcrossCapabilities(t *testing.T) {
	const deviceID = eiServerIDFloor + 20
	sender := &eiSender{devices: map[uint64]*eiDevice{deviceID: {id: deviceID, version: 1, interfaces: map[string]uint64{}}}}
	device := sender.devices[deviceID]
	first := eiMessage{object: deviceID, opcode: 5, body: append(eiU64(eiServerIDFloor+101), eiString("ei_keyboard")...)}
	if err := sender.consumeSetupEvent(first); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ei_button", "ei_scroll", "ei_pointer_absolute", "ei_text"} {
		err := sender.consumeSetupEvent(eiMessage{object: deviceID, opcode: 5, body: append(eiU64(eiServerIDFloor+101), eiString(name)...)})
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Errorf("aliased interface %q accepted: %v", name, err)
		}
		if len(device.interfaces) != 1 || device.interfaces["ei_keyboard"] != eiServerIDFloor+101 {
			t.Errorf("alias changed existing authorization: %+v", device.interfaces)
		}
	}
	if err := sender.consumeSetupEvent(eiMessage{object: deviceID, opcode: 5, body: append(eiU64(eiServerIDFloor+102), eiString("ei_button")...)}); err != nil {
		t.Fatalf("distinct legitimate interface rejected: %v", err)
	}
}
