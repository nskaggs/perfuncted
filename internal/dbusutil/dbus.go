// Package dbusutil provides shared D-Bus utilities for perfuncted.
//go:build linux
// +build linux

package dbusutil

import "github.com/godbus/dbus/v5"

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
