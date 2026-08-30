//go:build linux
// +build linux

package dbusutil

import "testing"

func TestHasServiceNil(t *testing.T) {
	if HasService(nil, "org.freedesktop.DBus") {
		t.Fatal("HasService(nil) = true, want false")
	}
}

func TestSessionBusAddressInvalid(t *testing.T) {
	_, err := SessionBusAddress("unix:path=/tmp/nonexistent-dbus-socket-12345")
	if err == nil {
		t.Fatal("SessionBusAddress with invalid path succeeded, want error")
	}
}
