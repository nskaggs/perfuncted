//go:build linux

package input

import (
	"testing"
)

// Only protocol events that carry a serial may advance the outgoing EIS
// sequence. In particular, a disconnect body contains a reason, not a serial.
func TestEISSerialAuthorityProtocolEventMatrix(t *testing.T) {
	const connection = eiServerIDFloor + 16
	cases := []struct {
		name  string
		event eiMessage
		want  uint32
	}{
		{"handshake connection", eiMessage{object: 0, opcode: 2, body: eiU32(101)}, 101},
		{"new device", eiMessage{object: connection + 1, opcode: 0, body: eiU32(102)}, 102},
		{"device resumed", eiMessage{object: connection + 2, opcode: 7, body: eiU32(103)}, 103},
		{"device paused", eiMessage{object: connection + 3, opcode: 8, body: eiU32(104)}, 104},
		{"disconnect reason not serial", eiMessage{object: connection, opcode: 0, body: eiU32(888)}, 77},
		{"short device serial", eiMessage{object: connection + 1, opcode: 0, body: []byte{8, 9, 10}}, 77},
		{"unrelated client object", eiMessage{object: 4, opcode: 7, body: eiU32(888)}, 77},
		{"unrelated global opcode", eiMessage{object: 0, opcode: 7, body: eiU32(888)}, 77},
		{"unrelated server opcode", eiMessage{object: connection + 1, opcode: 10, body: eiU32(888)}, 77},
		{"empty packet", eiMessage{}, 77},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &eiClient{}
			c.connection.Store(connection)
			c.serial.Store(77)
			c.updateSerial(tc.event)
			if actual := c.serial.Load(); actual != tc.want {
				t.Fatalf("serial=%d want=%d for event %+v", actual, tc.want, tc.event)
			}
		})
	}
}
