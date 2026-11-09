package input

import (
	"strings"
	"testing"
)

// The server owns the interface objects it announces on an ei_device. An
// interface from the client namespace could alias the device-setup callback.
func TestEISRejectsInterfaceInClientObjectNamespace(t *testing.T) {
	const dev = eiServerIDFloor + 32
	for _, tc := range []struct {
		name string
		id   uint64
	}{
		{"callback", 1},
		{"subsequent client object", 17},
		{"client namespace boundary", eiServerIDFloor - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{seat: eiServerIDFloor + 11, connection: eiServerIDFloor + 10, devices: map[uint64]*eiDevice{dev: {id: dev, version: 1, interfaces: make(map[string]uint64)}}}
			err := sender.consumeSetupEvent(eiMessage{object: dev, opcode: 5, body: append(eiU64(tc.id), eiString("ei_keyboard")...)})
			if err == nil || !strings.Contains(err.Error(), "invalid interface") {
				t.Fatalf("client-owned interface %x accepted: %v", tc.id, err)
			}
			if len(sender.devices[dev].interfaces) != 0 {
				t.Fatalf("invalid interface retained: %v", sender.devices[dev].interfaces)
			}
		})
	}
}

func TestEISAcceptsInterfaceAtServerNamespaceBoundary(t *testing.T) {
	const dev = eiServerIDFloor + 32
	sender := &eiSender{seat: eiServerIDFloor + 11, connection: eiServerIDFloor + 10, devices: map[uint64]*eiDevice{dev: {id: dev, version: 1, interfaces: make(map[string]uint64)}}}
	err := sender.consumeSetupEvent(eiMessage{object: dev, opcode: 5, body: append(eiU64(eiServerIDFloor), eiString("ei_keyboard")...)})
	if err != nil || sender.devices[dev].interfaces["ei_keyboard"] != eiServerIDFloor {
		t.Fatalf("server-owned interface rejected: %v %v", err, sender.devices[dev].interfaces)
	}
}
