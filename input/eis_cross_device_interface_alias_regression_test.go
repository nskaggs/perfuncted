package input

import (
	"strings"
	"testing"
)

// EIS object IDs identify protocol objects across the entire connection,
// not just within a single device's interface map. A malformed second device
// must not reuse an already authorized interface object ID.
func TestEISRejectsInterfaceObjectReusedAcrossDevices(t *testing.T) {
	const firstID = eiServerIDFloor + 21
	const secondID = eiServerIDFloor + 22
	first := &eiDevice{id: firstID, version: 1, interfaces: map[string]uint64{"ei_keyboard": 101}}
	second := &eiDevice{id: secondID, version: 1, interfaces: map[string]uint64{}}
	sender := &eiSender{devices: map[uint64]*eiDevice{firstID: first, secondID: second}}
	for _, name := range []string{"ei_keyboard", "ei_button", "ei_pointer_absolute", "ei_text"} {
		err := sender.consumeSetupEvent(eiMessage{object: secondID, opcode: 5, body: append(eiU64(101), eiString(name)...)})
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Errorf("cross-device object alias %q accepted: %v", name, err)
		}
		if len(second.interfaces) != 0 || first.interfaces["ei_keyboard"] != 101 {
			t.Errorf("alias corrupted authorization: first=%v second=%v", first.interfaces, second.interfaces)
		}
	}
	if err := sender.consumeSetupEvent(eiMessage{object: secondID, opcode: 5, body: append(eiU64(102), eiString("ei_button")...)}); err != nil {
		t.Fatalf("distinct second-device interface rejected: %v", err)
	}
}
