package window

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
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
	writeLimit       int
	closed           bool
}

func (c *stubSwayConn) Read([]byte) (int, error) {
	return 0, io.EOF
}

func (c *stubSwayConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeCalls++
	if c.writeLimit > 0 && len(p) > c.writeLimit {
		return c.writeLimit, nil
	}
	return len(p), nil
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

type successSwayConn struct {
	stubSwayConn
	readBuf bytes.Buffer
}

func newSuccessSwayConn(body []byte) *successSwayConn {
	hdr := make([]byte, 14)
	copy(hdr[0:6], swayMagic[:])
	binary.LittleEndian.PutUint32(hdr[6:10], uint32(len(body)))
	binary.LittleEndian.PutUint32(hdr[10:14], swayMsgGetTree)
	c := &successSwayConn{}
	c.readBuf.Write(hdr)
	c.readBuf.Write(body)
	return c
}

func (c *successSwayConn) Read(p []byte) (int, error) {
	return c.readBuf.Read(p)
}

func (c *successSwayConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeCalls++
	return len(p), nil
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

func TestWriteSwayMessageRejectsShortWrite(t *testing.T) {
	conn := &stubSwayConn{writeLimit: 1}

	err := writeSwayMessage(conn, swayMsgGetTree, "payload")
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("writeSwayMessage error = %v, want io.ErrShortWrite", err)
	}
}

func TestSwayQueryConnRejectsUnexpectedResponseType(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	serverDone := make(chan error, 1)
	go func() {
		_, _, err := readSwayMessage(server)
		if err != nil {
			serverDone <- err
			return
		}
		serverDone <- writeSwayMessage(server, swayMsgRunCommand, `{"unexpected":true}`)
	}()

	_, err := swayQueryConn(context.Background(), client, swayMsgGetTree, "")
	if err == nil || !strings.Contains(err.Error(), "unexpected response type") {
		t.Fatalf("swayQueryConn error = %v, want unexpected response type", err)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatalf("server: %v", serverErr)
	}
}

func TestSwayCmdRequiresSuccessfulCommandResults(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{name: "empty result list", response: `[]`, want: "no command result"},
		{name: "later command failure", response: `[{"success":true},{"success":false,"error":"rejected"}]`, want: "command failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()

			serverDone := make(chan error, 1)
			go func() {
				_, _, err := readSwayMessage(server)
				if err != nil {
					serverDone <- err
					return
				}
				serverDone <- writeSwayMessage(server, swayMsgRunCommand, tt.response)
			}()

			m := &SwayManager{conn: client}
			err := m.swayCmd(context.Background(), "focus")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("swayCmd error = %v, want %q", err, tt.want)
			}
			if serverErr := <-serverDone; serverErr != nil {
				t.Fatalf("server: %v", serverErr)
			}
		})
	}
}

func TestSwayQueryCancelableContextDoesNotReusePersistentConn(t *testing.T) {
	origDial := swayDialContext
	defer func() { swayDialContext = origDial }()

	persistent := &stubSwayConn{}
	transient := newSuccessSwayConn([]byte(`{"ok":true}`))
	swayDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return transient, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	m := &SwayManager{sock: "ignored", conn: persistent}
	body, err := m.query(ctx, swayMsgGetTree, "")
	if err != nil {
		t.Fatalf("query error = %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("query body = %s, want JSON body", body)
	}
	if _, _, persistentWrites, _ := persistent.snapshot(); persistentWrites != 0 {
		t.Fatalf("persistent conn writeCalls = %d, want 0", persistentWrites)
	}
	if _, _, transientWrites, transientClosed := transient.snapshot(); transientWrites == 0 || !transientClosed {
		t.Fatalf("transient conn writeCalls=%d closed=%v, want write and close", transientWrites, transientClosed)
	}
	if m.conn != persistent {
		t.Fatal("query replaced persistent conn for cancelable context")
	}
}

func TestSwayMoveByIDPropagatesReflowListError(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		responses := []struct {
			messageType uint32
			body        []byte
		}{
			{messageType: swayMsgGetTree, body: []byte(`{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"window"}]} `)},
			{messageType: swayMsgRunCommand, body: []byte(`[{"success":true}]`)},
			{messageType: swayMsgGetTree, body: []byte(`not-json`)},
			{messageType: swayMsgRunCommand, body: []byte(`[{"success":true}]`)},
		}
		for _, response := range responses {
			header := make([]byte, 14)
			if _, err := io.ReadFull(server, header); err != nil {
				return
			}
			body := make([]byte, binary.LittleEndian.Uint32(header[6:10]))
			if _, err := io.ReadFull(server, body); err != nil {
				return
			}
			if err := writeSwayMessage(server, response.messageType, string(response.body)); err != nil {
				return
			}
		}
	}()

	m := &SwayManager{conn: client, ReflowTimeout: 20 * time.Millisecond}
	err := m.MoveByID(context.Background(), "42", 10, 20)
	if err == nil || !strings.Contains(err.Error(), "window/sway: parse tree") {
		t.Fatalf("MoveByID error = %v, want reflow list parse error", err)
	}
	_ = client.Close()
	<-serverDone
}
