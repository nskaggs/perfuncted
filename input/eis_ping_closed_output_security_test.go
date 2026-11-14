package input

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

// After EI has learned the authoritative connection ID, a valid server
// ping must not be acknowledged if the local reply stream has been retired.
// The reply failure must be returned and the connection identity preserved.
func TestEIServerPingFailsClosedWhenReplyTransportIsUnavailable(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer clientConn.Close()
	defer serverConn.Close()
	const connection = eiServerIDFloor + 31
	const ping = eiServerIDFloor + 32
	client := &eiClient{conn: clientConn}
	first := eiMessage{object: 0, opcode: 2, body: append(append(eiU32(17), eiU64(connection)...), eiU32(1)...)}
	if err := client.handleProtocolMessage(first); err != nil {
		t.Fatalf("valid connection event rejected: %v", err)
	}
	if err := clientConn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response := eiMessage{object: connection, opcode: 3, body: append(eiU64(ping), eiU32(1)...)}
	err := client.handleProtocolMessage(response)
	if !errors.Is(err, unix.EPIPE) {
		t.Fatalf("unavailable reply transport acknowledged ping: %v", err)
	}
	if got := client.connection.Load(); got != connection {
		t.Fatalf("closed reply transport changed connection authority: %d", got)
	}
}
