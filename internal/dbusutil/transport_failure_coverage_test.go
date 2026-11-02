//go:build linux

package dbusutil

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestSessionBusWithoutAddressFailsClosedOnUnreachableDefault(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "missing"))
	conn, err := SessionBusAddressContext(context.Background(), "")
	if conn != nil {
		defer conn.Close()
	}
	if conn != nil || err == nil {
		t.Fatalf("unreachable default bus accepted: conn=%v err=%v", conn, err)
	}
}

func TestConnectContextRejectsPeerThatClosesBeforeAuthentication(t *testing.T) {
	listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "fakebus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		peer, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		done <- peer.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := ConnectContext(ctx, "unix:path="+listener.Addr().String())
	if conn != nil {
		defer conn.Close()
	}
	if conn != nil || err == nil {
		t.Fatalf("unauthenticated peer accepted: conn=%v err=%v", conn, err)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatalf("fake bus not reached: %v", ctx.Err())
	}
}

func TestHasServiceOnClosedAuthenticatedBusReturnsError(t *testing.T) {
	address := startSyntheticDefaultBus(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	var connErr error
	var conn *dbus.Conn
	// Use the actual authenticated D-Bus connection rather than a mock method
	// result; failed ListNames calls must return errors rather than false success.
	for ctx.Err() == nil {
		c, e := ConnectContext(ctx, address)
		connErr = e
		if e == nil {
			conn = c
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	if conn == nil {
		t.Fatalf("private bus authentication unavailable: %v", connErr)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	// A closed concrete connection still reaches the D-Bus method-error path.
	present, err := HasServiceContext(context.Background(), conn, "org.freedesktop.DBus")
	if present || err == nil {
		t.Fatalf("dead D-Bus transport advertised service: %v %v", present, err)
	}
}

func TestHandshakeCanceledBetweenSuccessfulResponseAndPublication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := runHandshakeContext(ctx, func() error { cancel(); return nil }, func() error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("late canceled handshake accepted: %v", err)
	}
}

func TestDefaultBusRejectsConnectionClosedBeforeAuthentication(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "fake-default")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+socket)
	done := make(chan error, 1)
	go func() {
		peer, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		done <- peer.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := SessionBusAddressContext(ctx, "")
	if conn != nil {
		defer conn.Close()
	}
	if conn != nil || err == nil {
		t.Fatalf("incomplete default bus auth succeeded: %v %v", conn, err)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("did not reach malformed default bus peer")
	}
}

// This context cancels precisely at the post-handshake Err inspection. It
// models cancellation after the authentication reply but before the API
// publishes a connection, without relying on goroutine scheduling races.
type cancelOnPostHandshakeCheck struct {
	done   <-chan struct{}
	cancel context.CancelFunc
	checks atomic.Int32
}

func (c *cancelOnPostHandshakeCheck) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelOnPostHandshakeCheck) Done() <-chan struct{}       { return c.done }
func (c *cancelOnPostHandshakeCheck) Value(any) any               { return nil }
func (c *cancelOnPostHandshakeCheck) Err() error {
	if c.checks.Add(1) == 2 {
		c.cancel()
	}
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func TestHandshakeRejectsCancellationAfterReplyBeforePublication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wrapper := &cancelOnPostHandshakeCheck{done: ctx.Done(), cancel: cancel}
	calls := 0
	err := runHandshakeContext(wrapper, func() error { calls++; return nil }, func() error { return nil })
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("late cancellation not authoritative: calls=%d err=%v", calls, err)
	}
}
