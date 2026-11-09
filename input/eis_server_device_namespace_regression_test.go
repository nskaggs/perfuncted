package input

import (
	"strings"
	"testing"
)

// A server-announced ei_device must use the server object namespace; it may
// otherwise alias client callbacks used during setup and future requests.
func TestEISDeviceAnnouncementRejectsClientObjectNamespace(t *testing.T) {
	const seat = eiServerIDFloor + 11
	for _, tc := range []struct {
		name string
		id   uint64
	}{
		{"callback object", 1},
		{"later client object", 17},
		{"client namespace boundary", eiServerIDFloor - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &eiSender{seat: seat, connection: eiServerIDFloor + 10, devices: make(map[uint64]*eiDevice)}
			err := s.consumeSetupEvent(eiMessage{object: seat, opcode: 4, body: append(eiU64(tc.id), eiU32(1)...)})
			if err == nil || !strings.Contains(err.Error(), "invalid device") {
				t.Fatalf("client-owned object %x accepted as device: %v", tc.id, err)
			}
			if len(s.devices) != 0 {
				t.Fatalf("rejected device retained authority: %v", s.devices)
			}
		})
	}
}

func TestEISDeviceAnnouncementAcceptsServerNamespaceBoundary(t *testing.T) {
	s := &eiSender{seat: eiServerIDFloor + 11, connection: eiServerIDFloor + 10, devices: make(map[uint64]*eiDevice)}
	err := s.consumeSetupEvent(eiMessage{object: s.seat, opcode: 4, body: append(eiU64(eiServerIDFloor), eiU32(1)...)})
	if err != nil || s.devices[eiServerIDFloor] == nil {
		t.Fatalf("server device namespace boundary rejected: %v", err)
	}
}
