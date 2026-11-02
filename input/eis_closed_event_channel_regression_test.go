package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Closing a server event channel is terminal: it is not a valid object-zero
// protocol event and must not be interpreted as successful input state.
func TestEISNextRejectsClosedMessageStreamWithoutAdvancingSerial(t *testing.T) {
	c := &eiClient{messages: make(chan eiMessage), done: make(chan struct{})}
	c.serial.Store(77)
	close(c.messages)
	message, err := c.next(context.Background())
	if err == nil || message.object != 0 || message.opcode != 0 || c.serial.Load() != 77 {
		t.Fatalf("closed event stream accepted: event=%+v err=%v serial=%d", message, err, c.serial.Load())
	}
}

func TestRemoteDesktopRevokesSessionWhenEISMessagesCloseUnexpectedly(t *testing.T) {
	b, _, c := newSyntheticRevocableRemoteDesktop(t)
	close(c.messages)
	select {
	case <-b.revoked:
	case <-time.After(150 * time.Millisecond):
		t.Fatal("closed EIS event stream left authorized input live")
	}
	if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("closed EIS messages kept session valid: %v", err)
	}
}
