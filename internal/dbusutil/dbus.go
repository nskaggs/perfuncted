// Package dbusutil provides shared D-Bus utilities for perfuncted.
//go:build linux
// +build linux

package dbusutil

import (
	"slices"

	"github.com/godbus/dbus/v5"
)

// SessionBusAddress returns a session bus connection using addr when provided.
// When addr is empty it falls back to the current process session bus.
func SessionBusAddress(addr string) (*dbus.Conn, error) {
	if addr == "" {
		return dbus.SessionBus()
	}
	conn, err := dbus.Dial(addr)
	if err != nil {
		return nil, err
	}
	if err := conn.Auth(nil); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.Hello(); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
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
