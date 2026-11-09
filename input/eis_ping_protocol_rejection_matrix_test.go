package input

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// Once the server's connection object is authenticated, every malformed
// ping must close the stream without making an acknowledgment on that socket.
// The peer must not be able to keep using stale input authority afterwards.
func TestEISLiveConnectionRejectsMalformedPingWithoutAcknowledging(t *testing.T) {
	const ping = eiServerIDFloor + 22
	for _, tc := range []struct {
		name string
		body []byte
		want string
	}{
		{"missing version", eiU64(ping), "invalid connection ping size"},
		{"short ping object", eiU32(1), "invalid connection ping size"},
		{"extended ping", append(append(eiU64(ping), eiU32(1)...), eiU32(0)...), "invalid connection ping size"},
		{"zero object", append(eiU64(0), eiU32(1)...), "unsupported connection ping"},
		{"zero version", append(eiU64(ping), eiU32(0)...), "unsupported connection ping"},
		{"future version", append(eiU64(ping), eiU32(2)...), "unsupported connection ping"},
	} {
		t.Run(tc.name, func(t *testing.T) { checkEISInvalidPing(t, tc.body, tc.want) })
	}
}

// checkEISInvalidPing exercises an independently negotiated real socket.
func checkEISInvalidPing(t *testing.T, body []byte, want string) {
	const connection = eiServerIDFloor + 21
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	handshake := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	if _, err := serverConn.Write(handshake); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.next(ctx); err != nil {
		t.Fatal(err)
	}
	if client.connection.Load() != connection {
		t.Fatal("handshake failed to establish connection")
	}
	if _, err := serverConn.Write(eiRequest(connection, 3, body)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
		if err := client.readError(); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("malformed ping error=%v, want %q", err, want)
		}
	case <-ctx.Done():
		t.Fatal("malformed ping left EIS stream active")
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 64)
	count, err := serverConn.Read(data)
	if count != 0 || err == nil {
		t.Fatalf("malformed ping received successful reply: n=%d, err=%v", count, err)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("expected no ping response before deadline: %v", err)
	}
}
