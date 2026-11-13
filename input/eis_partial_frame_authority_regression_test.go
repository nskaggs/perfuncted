package input

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// A partial EIS message never authorizes a protocol event. If the peer closes
// before completing the frame, pending reads must terminate rather than
// waiting for a frame that no live peer can finish.
func TestEISPartialFramePeerCloseNeverPublishesEvent(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	client := newEIClient(clientConn)
	defer client.close()
	frame := eiRequest(eiServerIDFloor+7, 5, eiU32(9))
	if _, err := serverConn.Write(frame[:len(frame)-4]); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-client.messages:
		t.Fatalf("partial frame published event: %+v", message)
	case <-time.After(10 * time.Millisecond):
	}
	if err := serverConn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("reader survived peer close with incomplete frame")
	}
	if err := client.readError(); !errors.Is(err, io.EOF) {
		t.Fatalf("partial frame terminal error=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if message, err := client.next(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) || message.object != 0 {
		t.Fatalf("terminal partial frame yielded event=%+v err=%v", message, err)
	}
}

// Once the parser observes a malformed message, even a preceding valid event
// already buffered by the reader cannot be treated as current authorization.
func TestEISMalformedFrameRevokesQueuedEarlierEvent(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	valid := eiRequest(eiServerIDFloor+8, 5, eiU32(11))
	invalid := eiRequest(eiServerIDFloor+9, 5, eiU32(12))
	binary.NativeEndian.PutUint32(invalid[8:12], 12)
	if _, err := serverConn.Write(append(valid, invalid...)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("reader did not retire malformed frame")
	}
	if err := client.readError(); err == nil || !strings.Contains(err.Error(), "invalid message size") {
		t.Fatalf("parser terminal error=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	message, err := client.next(ctx)
	if err == nil || errors.Is(err, context.DeadlineExceeded) || message.object != 0 || client.serial.Load() != 0 {
		t.Fatalf("queued event escaped failed transport: message=%+v err=%v serial=%d", message, err, client.serial.Load())
	}
}

func TestEISenderWithoutAuthorizedPointerRejectsNilAndEmptyDevices(t *testing.T) {
	var nilSender *eiSender
	if nilSender.hasAuthorizedPointer() {
		t.Fatal("nil sender advertised pointer authorization")
	}
	for _, sender := range []*eiSender{
		{},
		{pointers: []eiPointerDevice{{device: 3, pointer: 4, regions: []eiRegion{{width: 0, height: 1}}}}},
		{pointers: []eiPointerDevice{{device: 3, regions: []eiRegion{{width: 1, height: 1}}}}},
	} {
		if sender.hasAuthorizedPointer() {
			t.Fatalf("incomplete EIS pointer advertised capability: %+v", sender)
		}
	}
}
