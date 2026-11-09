package input

import (
	"context"
	"testing"
)

// An ei_connection.disconnect is a terminal lifecycle event whose first
// payload word is a reason code, not an input-device event serial. It must
// not replace the last negotiated serial as next drains pending events.
func TestEIConnectionDisconnectReasonCannotReplaceAuthorizedSerial(t *testing.T) {
	const connection = eiServerIDFloor + 21
	c := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
	c.connection.Store(connection)
	c.serial.Store(73)
	c.messages <- eiMessage{object: connection, opcode: 0, body: eiU32(0xdeadbeef)}
	msg, err := c.next(context.Background())
	if err != nil || msg.object != connection || msg.opcode != 0 {
		t.Fatalf("lost lifecycle event: %+v %v", msg, err)
	}
	if got := c.serial.Load(); got != 73 {
		t.Fatalf("disconnect reason corrupted negotiated serial: %d", got)
	}
}

func TestEIConnectionSerialStillAdvancesFromHandshake(t *testing.T) {
	c := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
	c.messages <- eiMessage{object: 0, opcode: 2, body: append(append(eiU32(91), eiU64(eiServerIDFloor+10)...), eiU32(1)...)}
	if _, err := c.next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.serial.Load(); got != 91 {
		t.Fatalf("handshake serial was lost: %d", got)
	}
}
