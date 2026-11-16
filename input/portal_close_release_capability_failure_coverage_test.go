//go:build linux

package input

import (
	"errors"
	"testing"
)

func TestRemoteDesktopCloseReportsHeldInputReleaseCapabilityFailure(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setHeld    func(*RemoteDesktopBackend)
		dropDevice func(*RemoteDesktopBackend)
		stillHeld  func(*RemoteDesktopBackend) bool
	}{
		{
			name: "keyboard",
			setHeld: func(b *RemoteDesktopBackend) {
				b.heldKeys[30] = true
			},
			dropDevice: func(b *RemoteDesktopBackend) {
				b.sender.keyboardDev = 0
			},
			stillHeld: func(b *RemoteDesktopBackend) bool {
				return b.heldKeys[30]
			},
		},
		{
			name: "pointer button",
			setHeld: func(b *RemoteDesktopBackend) {
				b.buttons[0x110] = true
			},
			dropDevice: func(b *RemoteDesktopBackend) {
				b.sender.buttonDev = 0
			},
			stillHeld: func(b *RemoteDesktopBackend) bool {
				return b.buttons[0x110]
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, _, _ := newSyntheticRevocableRemoteDesktop(t)
			tc.setHeld(backend)
			tc.dropDevice(backend)

			err := backend.Close()
			if !errors.Is(err, ErrNotSupported) {
				t.Fatalf("Close error = %v, want missing-device capability error", err)
			}
			if !tc.stillHeld(backend) {
				t.Fatal("Close discarded held-input ownership after release failed")
			}
			if repeatedErr := backend.Close(); !errors.Is(repeatedErr, ErrNotSupported) {
				t.Fatalf("repeated Close error = %v, want the original cleanup failure", repeatedErr)
			}
		})
	}
}
