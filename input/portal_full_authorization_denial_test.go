package input

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestSyntheticPortalFailsClosedAcrossAuthorizationStages(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*syntheticPortal)
		want      error
	}{
		{"session denied", func(p *syntheticPortal) { p.code = 1 }, ErrRemoteDesktopDenied},
		{"session malformed", func(p *syntheticPortal) {
			p.result = map[string]dbus.Variant{"session_handle": dbus.MakeVariant(uint32(12))}
		}, ErrRemoteDesktopUnavailable},
		{"select devices denied", func(p *syntheticPortal) { p.selectCode = 1 }, ErrRemoteDesktopDenied},
		{"start denied", func(p *syntheticPortal) { p.startCode = 1 }, ErrRemoteDesktopDenied},
		{"no keyboard", func(p *syntheticPortal) { p.deviceMask = portalDevicePointer }, ErrRemoteDesktopDenied},
		{"no pointer", func(p *syntheticPortal) { p.deviceMask = portalDeviceKeyboard }, ErrRemoteDesktopDenied},
		{"no devices", func(p *syntheticPortal) { p.deviceMask = 0 }, ErrRemoteDesktopDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := startSyntheticPortalBus(t)
			p.mu.Lock()
			p.deviceMask = portalDeviceKeyboard | portalDevicePointer
			tc.configure(p)
			p.mu.Unlock()
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=/tmp/synthetic-wayland-socket",
				"DBUS_SESSION_BUS_ADDRESS=" + p.busAddress,
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			inputter, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{})
			if inputter != nil {
				t.Fatalf("failed grant returned input backend %T", inputter)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("OpenRemoteDesktopContext=%v, want %v", err, tc.want)
			}
		})
	}
}
