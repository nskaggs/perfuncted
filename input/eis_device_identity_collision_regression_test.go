package input

import (
	"strings"
	"testing"
)

// The server must not announce a device with an ID already assigned to the
// connection's seat, connection, or an authorized interface object.
func TestEISRejectsDeviceObjectIdentityCollisions(t *testing.T) {
	const seat = eiServerIDFloor + 11
	const connection = eiServerIDFloor + 10
	const device = eiServerIDFloor + 12
	const interfaceID = eiServerIDFloor + 30
	for _, tc := range []struct {
		name string
		id   uint64
	}{{"seat", seat}, {"connection", connection}, {"authorized interface", interfaceID}} {
		t.Run(tc.name, func(t *testing.T) {
			existing := &eiDevice{id: device, version: 1, interfaces: map[string]uint64{"ei_keyboard": interfaceID}}
			sender := &eiSender{seat: seat, connection: connection, devices: map[uint64]*eiDevice{device: existing}}
			err := sender.consumeSetupEvent(eiMessage{object: seat, opcode: 4, body: append(eiU64(tc.id), eiU32(1)...)})
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("colliding device %s accepted: %v", tc.name, err)
			}
			if len(sender.devices) != 1 || sender.devices[device] != existing {
				t.Fatalf("device collision changed previous authority: %v", sender.devices)
			}
		})
	}
}

func TestEISRejectsInterfaceIdentityMatchingDeviceSeatOrConnection(t *testing.T) {
	const seat = eiServerIDFloor + 11
	const connection = eiServerIDFloor + 10
	const device = eiServerIDFloor + 12
	for _, tc := range []struct {
		name string
		id   uint64
	}{{"seat", seat}, {"connection", connection}, {"device", device}} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{seat: seat, connection: connection, devices: map[uint64]*eiDevice{device: {id: device, version: 1, interfaces: map[string]uint64{}}}}
			err := sender.consumeSetupEvent(eiMessage{object: device, opcode: 5, body: append(eiU64(tc.id), eiString("ei_keyboard")...)})
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("colliding interface %s accepted: %v", tc.name, err)
			}
			if len(sender.devices[device].interfaces) != 0 {
				t.Fatalf("collision granted keyboard authority: %v", sender.devices[device].interfaces)
			}
		})
	}
}
