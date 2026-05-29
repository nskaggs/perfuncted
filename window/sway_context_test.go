package window

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type stubSwayAddr string

func (a stubSwayAddr) Network() string { return "unix" }
func (a stubSwayAddr) String() string  { return string(a) }

type stubSwayConn struct {
	mu               sync.Mutex
	deadline         time.Time
	setDeadlineCalls int
	writeCalls       int
	closed           bool
}

func (c *stubSwayConn) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (c *stubSwayConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeCalls++
	return 0, io.EOF
}

func (c *stubSwayConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *stubSwayConn) LocalAddr() net.Addr                { return stubSwayAddr("local") }
func (c *stubSwayConn) RemoteAddr() net.Addr               { return stubSwayAddr("remote") }
func (c *stubSwayConn) SetReadDeadline(t time.Time) error  { return c.SetDeadline(t) }
func (c *stubSwayConn) SetWriteDeadline(t time.Time) error { return c.SetDeadline(t) }

func (c *stubSwayConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = t
	c.setDeadlineCalls++
	return nil
}

func (c *stubSwayConn) snapshot() (time.Time, int, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deadline, c.setDeadlineCalls, c.writeCalls, c.closed
}

func TestSwayQueryConnCanceledContextShortCircuits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn := &stubSwayConn{}
	_, err := swayQueryConn(ctx, conn, swayMsgGetTree, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("swayQueryConn error = %v, want context.Canceled", err)
	}

	_, setDeadlineCalls, writeCalls, closed := conn.snapshot()
	if setDeadlineCalls != 0 || writeCalls != 0 || closed {
		t.Fatalf("swayQueryConn touched connection after cancellation: setDeadline=%d write=%d closed=%v", setDeadlineCalls, writeCalls, closed)
	}
}

func TestSwayActiveTitleCanceledContextShortCircuits(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn := &stubSwayConn{}
	_, err := (&SwayManager{conn: conn}).ActiveTitle(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ActiveTitle error = %v, want context.Canceled", err)
	}

	_, setDeadlineCalls, writeCalls, closed := conn.snapshot()
	if setDeadlineCalls != 0 || writeCalls != 0 || closed {
		t.Fatalf("ActiveTitle touched connection after cancellation: setDeadline=%d write=%d closed=%v", setDeadlineCalls, writeCalls, closed)
	}
}

func TestSwayQueryConnUsesContextDeadline(t *testing.T) {
	deadline := time.Now().Add(75 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	conn := &stubSwayConn{}
	_, err := swayQueryConn(ctx, conn, swayMsgGetTree, "")
	if !errors.Is(err, io.EOF) {
		t.Fatalf("swayQueryConn error = %v, want io.EOF", err)
	}

	gotDeadline, setDeadlineCalls, writeCalls, _ := conn.snapshot()
	if setDeadlineCalls == 0 {
		t.Fatal("swayQueryConn did not set a deadline")
	}
	if writeCalls == 0 {
		t.Fatal("swayQueryConn did not attempt to write the IPC request")
	}
	if delta := gotDeadline.Sub(deadline); delta < -20*time.Millisecond || delta > 20*time.Millisecond {
		t.Fatalf("deadline delta = %v, want within +/-20ms of context deadline", delta)
	}
	if until := time.Until(gotDeadline); until > time.Second {
		t.Fatalf("deadline too far in the future: %v", until)
	}
}
