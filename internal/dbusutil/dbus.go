// Package dbusutil provides shared D-Bus utilities for perfuncted.
//go:build linux
// +build linux

package dbusutil

import "github.com/godbus/dbus/v5"

var sessionAddressOverride string

// SetSessionAddressOverride sets an explicit DBus session address to use for
// subsequent SessionBus calls. Pass empty string to clear the override.
func SetSessionAddressOverride(addr string) { sessionAddressOverride = addr }

// SessionBus returns a session bus connection. If a session address override
// has been configured, it connects directly to that address instead of using
// dbus.SessionBus().
func SessionBus() (*dbus.Conn, error) {
	if sessionAddressOverride == "" {
		return dbus.SessionBus()
	}
	conn, err := dbus.Dial(sessionAddressOverride)
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
	var names []string
	if err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return false
	}
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
