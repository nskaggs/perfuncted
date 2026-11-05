package input

import (
	"strings"
	"testing"
)

// A compositor must not reserve an interface protocol object under an empty
// name, since it cannot represent any EIS capability and can block a later
// authorized interface from using its object ID.
func TestEISRejectsUnnamedInterfaceWithoutReservingItsObject(t *testing.T) {
	const device = eiServerIDFloor + 31
	const object = eiServerIDFloor + 45
	sender := &eiSender{devices: map[uint64]*eiDevice{device: {id: device, version: 1, interfaces: make(map[string]uint64)}}}
	err := sender.consumeSetupEvent(eiMessage{object: device, opcode: 5, body: append(eiU64(object), eiString("")...)})
	if err == nil || !strings.Contains(err.Error(), "interface") {
		t.Fatalf("unnamed EIS interface accepted: %v", err)
	}
	if len(sender.devices[device].interfaces) != 0 || sender.isReservedEISObject(object) {
		t.Fatalf("rejected interface reserved identity: %v", sender.devices[device].interfaces)
	}
	if err := sender.consumeSetupEvent(eiMessage{object: device, opcode: 5, body: append(eiU64(object), eiString("ei_keyboard")...)}); err != nil {
		t.Fatalf("valid interface not accepted after malformed one: %v", err)
	}
}
