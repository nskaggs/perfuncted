package xgb

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

type timeoutHandshakeConn struct{ net.Conn }

func (timeoutHandshakeConn) Write([]byte) (int, error) { return 0, os.ErrDeadlineExceeded }

func TestContextConnectionAdditionalFailurePaths(t *testing.T) {
	t.Run("closed-socket-deadline", func(t *testing.T) {
		left, right := net.Pipe()
		_ = left.Close()
		defer right.Close()
		c := &Conn{conn: left}
		if err := c.handshakeContext(context.Background()); err == nil {
			t.Fatal("closed pipe accepted")
		}
	})
	t.Run("timeout-write-before-canceled-context", func(t *testing.T) {
		left, right := net.Pipe()
		defer left.Close()
		defer right.Close()
		c := &Conn{conn: timeoutHandshakeConn{Conn: left}}
		if err := c.handshakeContext(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
	t.Run("dial-original-wrapper", func(t *testing.T) {
		if os.Getenv("PF_X11_OBSERVATION_INTEGRATION") != "1" {
			t.Skip("requires isolated Xvfb")
		}
		c, err := NewConnDisplay(os.Getenv("DISPLAY"))
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
	})
	t.Run("dial-unix-socket-path", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		c := &Conn{}
		if err := c.dialContext(ctx, "/tmp/i128-nonexistent-socket/:0"); err == nil {
			t.Fatal("unexpected Unix socket accepted")
		}
	})
	t.Run("dial-tcp-localhost", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		c := &Conn{}
		if err := c.dialContext(ctx, "tcp/127.0.0.1:50999"); err == nil {
			if c.conn != nil {
				_ = c.conn.Close()
			}
			t.Fatal("unexpected local Xorg TCP socket")
		}
	})
}
