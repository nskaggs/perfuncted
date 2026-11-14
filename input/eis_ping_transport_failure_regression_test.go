package input

import (
	"errors"
	"net"
	"strings"
	"testing"
)

// A ping is acknowledged only after a successful transport write. When the
// server disappears, the authenticated peer must not be told that it received
// an EIS pong, and the underlying socket error must remain observable.
func TestEISAuthorizedPingReportsBrokenReplyTransport(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	client := &eiClient{conn: clientConn}
	t.Cleanup(func() { _ = clientConn.Close() })
	const connection = eiServerIDFloor + 21
	const ping = eiServerIDFloor + 22
	client.connection.Store(connection)
	if err := serverConn.Close(); err != nil {
		t.Fatal(err)
	}
	err := client.handleProtocolMessage(eiMessage{object: connection, opcode: 3, body: append(eiU64(ping), eiU32(1)...)})
	if err == nil || !strings.Contains(err.Error(), "answer connection ping") {
		t.Fatalf("broken reply transport accepted ping: %v", err)
	}
	var netErr *net.OpError
	if !errors.As(err, &netErr) {
		t.Fatalf("lost socket transport failure: %T %v", err, err)
	}
}
