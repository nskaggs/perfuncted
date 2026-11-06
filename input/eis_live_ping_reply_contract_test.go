package input

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

// The peer's ping must be answered on the negotiated connection socket, not
// merely acknowledged as an incoming event. The reply is an actual EI frame.
func TestEISClientRepliesToAuthorizedPingOnRealUnixSocket(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	const connection = eiServerIDFloor + 11
	const ping = eiServerIDFloor + 12
	handshake := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	if _, err := serverConn.Write(handshake); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.next(ctx); err != nil {
		t.Fatal(err)
	}
	if got := client.connection.Load(); got != connection {
		t.Fatalf("negotiated connection=%x", got)
	}
	frame := eiRequest(connection, 3, append(eiU64(ping), eiU32(1)...))
	if _, err := serverConn.Write(frame); err != nil {
		t.Fatal(err)
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 24)
	total := 0
	for total < len(reply) {
		n, err := serverConn.Read(reply[total:])
		if err != nil {
			t.Fatalf("missing ping response after %d bytes: %v", total, err)
		}
		total += n
	}
	if got := binary.NativeEndian.Uint64(reply[0:8]); got != ping {
		t.Fatalf("pong target=%x want=%x", got, ping)
	}
	if got := binary.NativeEndian.Uint32(reply[8:12]); got != 24 {
		t.Fatalf("pong frame size=%d", got)
	}
	if got := binary.NativeEndian.Uint32(reply[12:16]); got != 0 {
		t.Fatalf("pong opcode=%d", got)
	}
	if got := binary.NativeEndian.Uint64(reply[16:24]); got != 0 {
		t.Fatalf("pong payload=%d", got)
	}
	event, err := client.next(ctx)
	if err != nil || event.object != connection || event.opcode != 3 {
		t.Fatalf("ping event lost or altered %+v %v", event, err)
	}
}

func TestEISClientRejectsZeroPingObjectBeforeResponding(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	const connection = eiServerIDFloor + 11
	handshake := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	if _, err := serverConn.Write(handshake); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.next(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := serverConn.Write(eiRequest(connection, 3, append(eiU64(0), eiU32(1)...))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
		if err := client.readError(); err == nil || !strings.Contains(err.Error(), "unsupported connection ping") {
			t.Fatalf("zero ping not rejected: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("invalid ping did not close client")
	}
}
