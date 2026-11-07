package window

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
)

// The peer can finish replying at the exact moment the caller loses its
// lifetime. A completed read must not publish that response after cancellation.
type swayCancelAfterBodyConn struct {
	net.Conn
	cancel context.CancelFunc
	reads  int
}

func (c *swayCancelAfterBodyConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.reads++
		if c.reads == 2 {
			c.cancel()
		}
	}
	return n, err
}
func TestSwayQueryNeverPublishesResponseCanceledDuringFinalRead(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		request := make([]byte, 14)
		if _, err := server.Read(request); err != nil {
			done <- err
			return
		}
		response := make([]byte, 16)
		copy(response[:6], swayMagic[:])
		binary.LittleEndian.PutUint32(response[6:10], 2)
		binary.LittleEndian.PutUint32(response[10:14], swayMsgGetTree)
		copy(response[14:], "{}")
		_, err := server.Write(response)
		done <- err
	}()
	body, err := swayQueryConn(ctx, &swayCancelAfterBodyConn{Conn: client, cancel: cancel}, swayMsgGetTree, "")
	if err != context.Canceled {
		t.Fatalf("response %q published with error %v after final read canceled", body, err)
	}
	if serverErr := <-done; serverErr != nil {
		t.Fatal(serverErr)
	}
}
