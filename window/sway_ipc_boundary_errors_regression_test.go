package window

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

type failingSwayConn struct {
	stubSwayConn
	deadlineErr error
	writeErr    error
	cancel      context.CancelFunc
}

func (c *failingSwayConn) SetDeadline(deadline time.Time) error {
	c.stubSwayConn.SetDeadline(deadline)
	return c.deadlineErr
}
func (c *failingSwayConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.writeCalls++
	c.mu.Unlock()
	if c.cancel != nil {
		c.cancel()
	}
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return len(p), nil
}

// Failed admission to a transport deadline must not send any IPC request.
func TestSwayQueryRejectsDeadlineSetupFailureWithoutWrite(t *testing.T) {
	sentinel := errors.New("synthetic deadline rejected")
	conn := &failingSwayConn{deadlineErr: sentinel}
	data, err := swayQueryConnContext(context.Background(), conn, swayMsgGetTree, "private command")
	if len(data) != 0 || !errors.Is(err, sentinel) {
		t.Fatalf("unexpected result: %q %v", data, err)
	}
	_, deadlines, writes, _ := conn.snapshot()
	if deadlines != 1 || writes != 0 {
		t.Fatalf("failed deadline touched transport: deadlines=%d writes=%d", deadlines, writes)
	}
}

// Cancellation during the write takes priority over a transport-level failure;
// this keeps cancellation from being reported as an unrelated IPC error.
func TestSwayQueryPrefersCancellationOverConcurrentWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := &failingSwayConn{cancel: cancel, writeErr: io.ErrClosedPipe}
	data, err := swayQueryConnContext(ctx, conn, swayMsgRunCommand, "sensitive")
	if len(data) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %q %v", data, err)
	}
	_, _, writes, _ := conn.snapshot()
	if writes != 1 {
		t.Fatalf("canceled send executed %d writes", writes)
	}
}

// The second query on the same manager must never reuse a connection after an
// invalid IPC response, even when the fake server remains connected.
func TestSwayManagerRetiresConnectionAfterMalformedResponse(t *testing.T) {
	original := swayDialContext
	t.Cleanup(func() { swayDialContext = original })
	firstClient, firstPeer := net.Pipe()
	secondClient, secondPeer := net.Pipe()
	t.Cleanup(func() {
		_ = firstClient.Close()
		_ = firstPeer.Close()
		_ = secondClient.Close()
		_ = secondPeer.Close()
	})
	dials := 0
	swayDialContext = func(context.Context, string, string) (net.Conn, error) {
		dials++
		if dials == 1 {
			return firstClient, nil
		}
		if dials == 2 {
			return secondClient, nil
		}
		return nil, errors.New("unexpected third dial")
	}
	serverResult := make(chan error, 2)
	go func() {
		_, _, err := readSwayMessage(firstPeer)
		if err == nil {
			err = writeSwayMessage(firstPeer, swayMsgSubscribe, `{ "success": true }`)
		}
		serverResult <- err
	}()
	go func() {
		_, _, err := readSwayMessage(secondPeer)
		if err == nil {
			err = writeSwayMessage(secondPeer, swayMsgGetTree, `{ "id": 1, "type": "root" }`)
		}
		serverResult <- err
	}()
	manager := &SwayManager{sock: "private"}
	defer manager.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := manager.query(ctx, swayMsgGetTree, ""); err == nil {
		t.Fatal("accepted wrong response type")
	}
	raw, err := manager.query(ctx, swayMsgGetTree, "")
	if err != nil || string(raw) != `{ "id": 1, "type": "root" }` {
		t.Fatalf("new connection not used: %q %v", raw, err)
	}
	if dials != 2 {
		t.Fatalf("failed response did not force fresh dial: %d", dials)
	}
	for i := 0; i < 2; i++ {
		if err := <-serverResult; err != nil {
			t.Fatal(err)
		}
	}
}
