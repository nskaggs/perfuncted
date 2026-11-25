package xgb

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func fakeXServerSetup(peer net.Conn, status byte, extra []byte) error {
	header := make([]byte, 12)
	if _, err := io.ReadFull(peer, header); err != nil {
		return err
	}
	nameSize := Pad(int(Get16(header[6:8])))
	authSize := Pad(int(Get16(header[8:10])))
	if _, err := io.CopyN(io.Discard, peer, int64(nameSize+authSize)); err != nil {
		return err
	}
	setup := make([]byte, 32)
	setup[0] = status
	Put16(setup[2:4], 11)
	Put16(setup[4:6], 0)
	Put16(setup[6:8], 6)
	Put32(setup[12:16], 0x1000000)
	Put32(setup[16:20], 0x3ffff)
	if status == 0 {
		setup[1] = byte(len(extra))
		copy(setup[8:], extra)
	}
	_, err := peer.Write(setup)
	return err
}

func TestHandshakeContextSuccessAndAuthenticationFailure(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status byte
		want   string
	}{
		{"success", 1, ""}, {"auth-denied", 0, "authentication refused"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			left, right := net.Pipe()
			defer left.Close()
			defer right.Close()
			result := make(chan error, 1)
			go func() { result <- fakeXServerSetup(right, tt.status, []byte("not authorized")) }()
			conn := &Conn{conn: left}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := conn.handshakeContext(ctx)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if len(conn.SetupBytes) != 32 {
					t.Fatal("setup bytes missing")
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %s, got %v", tt.want, err)
			}
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("fake X server setup blocked")
			}
		})
	}
}

func TestContextConstructorOnIsolatedX11(t *testing.T) {
	if os.Getenv("PF_X11_OBSERVATION_INTEGRATION") != "1" {
		t.Skip("requires Xvfb")
	}
	ctx, cancel := context.WithCancel(context.Background())
	c, err := NewConnDisplayContext(ctx, os.Getenv("DISPLAY"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.SetupBytes) < 24 {
		t.Fatal("setup missing")
	}
	cancel()
	waitAbortDone(t, c)
	c.Abort()
}

func TestContextConstructorInvalidDisplay(t *testing.T) {
	if _, err := NewConnDisplayContext(context.Background(), "no-display"); err == nil {
		t.Fatal("accepted invalid display")
	}
	if _, err := NewConnDisplayContext(context.Background(), ":not-a-number"); err == nil {
		t.Fatal("accepted malformed display")
	}
}

func TestAbortNilAndUninitializedConnection(t *testing.T) {
	(*Conn)(nil).Abort()
	c := &Conn{}
	c.Abort()
	c.Abort()
	if c.stopAbort != nil {
		t.Fatal("unexpected cancellation registration")
	}
}

func TestHandshakeContextCanceledBeforeConnection(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &Conn{conn: a}
	if err := c.handshakeContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type cancelAfterSetupRead struct {
	net.Conn
	cancel context.CancelFunc
	reads  int
}

func (c *cancelAfterSetupRead) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.reads++
	if c.reads == 2 && n > 0 {
		c.cancel()
	}
	return n, err
}
func TestHandshakeContextCancelImmediatelyAfterSetup(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- fakeXServerSetup(right, 1, nil) }()
	wrapped := &cancelAfterSetupRead{Conn: left, cancel: cancel}
	c := &Conn{conn: wrapped}
	if err := c.handshakeContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("late setup cancellation: %v", err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fake X server blocked")
	}
}
