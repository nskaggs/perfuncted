package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A slow event consumer can fill the reader's bounded queue. Closing the
// transport must always terminate the reader without waiting for the caller
// to drain or accept an event from a session that no longer exists.
func TestEISReaderShutdownWithFullIncomingEventQueue(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	const count = 160
	payload := make([]byte, 0, count*20)
	for index := uint32(0); index < count; index++ {
		payload = append(payload, eiRequest(eiServerIDFloor+7, 5, eiU32(index+1))...)
	}
	if _, err := serverConn.Write(payload); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for len(client.messages) != cap(client.messages) {
		select {
		case <-client.done:
			t.Fatalf("reader terminated before queue filled: %v", client.readError())
		case <-deadline:
			t.Fatalf("queue failed to fill: buffered %d/%d", len(client.messages), cap(client.messages))
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := client.close(); err != nil {
		t.Fatalf("shutdown full EIS queue: %v", err)
	}
	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("reader blocked on a full queue after shutdown")
	}
	if client.serial.Load() != 0 {
		t.Fatalf("reader advanced serial without consumer authorization: %d", client.serial.Load())
	}
	event, err := client.next(context.Background())
	if err == nil || !errors.Is(err, client.readError()) && client.readError() != nil || event.object != 0 {
		t.Fatalf("terminal reader exposed queued event: %+v %v (read error %v)", event, err, client.readError())
	}
}
