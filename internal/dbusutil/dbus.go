// Package dbusutil provides shared D-Bus utilities for perfuncted.
//go:build linux
// +build linux

package dbusutil

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/godbus/dbus/v5"
)

const handshakeCleanupTimeout = 250 * time.Millisecond

// SessionBusAddress returns a session bus connection using addr when provided.
// When addr is empty it falls back to the current process session bus.
func SessionBusAddress(addr string) (*dbus.Conn, error) {
	return SessionBusAddressContext(context.Background(), addr)
}

// SessionBusAddressContext connects and handshakes with addr while ctx is
// active. A successful connection is owned by the caller and is not tied to
// ctx after this function returns.
func SessionBusAddressContext(ctx context.Context, addr string) (*dbus.Conn, error) {
	if ctx == nil {
		return nil, errors.New("dbusutil: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if addr == "" {
		// SessionBus returns a shared, already-handshaken connection. It cannot
		// be made caller-cancellable without affecting other users.
		return dbus.SessionBus()
	}
	return ConnectContext(ctx, addr)
}

// ConnectContext creates a private D-Bus connection and performs its
// authentication handshake under ctx. The returned connection is independent
// of ctx after success.
func ConnectContext(ctx context.Context, addr string) (*dbus.Conn, error) {
	if ctx == nil {
		return nil, errors.New("dbusutil: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := dbus.Dial(addr)
	if err != nil {
		return nil, err
	}
	if err := handshakeContext(ctx, conn); err != nil {
		return nil, errors.Join(err, conn.Close())
	}
	return conn, nil
}

// Auth and Hello predate context-aware godbus APIs. Run them under a small
// cancellation bridge and close the transport on cancellation so blocked
// authentication reads are interrupted and the worker can be joined.
func handshakeContext(ctx context.Context, conn *dbus.Conn) error {
	return runHandshakeContext(ctx, func() error {
		err := conn.Auth(nil)
		if err == nil {
			err = conn.Hello()
		}
		return err
	}, conn.Close)
}

func runHandshakeContext(ctx context.Context, handshake func() error, closeConn func() error) error {
	done := make(chan error, 1)
	go func() {
		done <- handshake()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = closeConn()
		timer := time.NewTimer(handshakeCleanupTimeout)
		select {
		case <-done:
			timer.Stop()
		case <-timer.C:
		}
		return ctx.Err()
	}
}

// HasService reports whether the given service name is present on the session bus.
func HasService(conn *dbus.Conn, name string) bool {
	if conn == nil {
		return false
	}
	var names []string
	if err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return false
	}
	return slices.Contains(names, name)
}
