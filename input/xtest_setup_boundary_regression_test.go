//go:build linux

package input

import (
	"context"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestXTestKeyboardSetupFailsClosedOnMissingOrInvertedKeycodeRange(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup *xproto.SetupInfo
	}{
		{"missing", nil},
		{"inverted", &xproto.SetupInfo{MinKeycode: 12, MaxKeycode: 8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
			conn, ok := b.conn.(*x11.MockConnection)
			if !ok {
				t.Fatalf("unexpected X11 connection %T", b.conn)
			}
			conn.SetupFunc = func() *xproto.SetupInfo { return tc.setup }
			calls := 0
			conn.GetKeyboardMappingFunc = func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
				calls++
				return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: []xproto.Keysym{0x61}})
			}
			err := b.KeyDown(context.Background(), "a")
			if err == nil || !strings.Contains(err.Error(), "keyboard") {
				t.Fatalf("invalid setup accepted: %v", err)
			}
			if calls != 0 || len(*events) != 0 {
				t.Fatalf("invalid setup queried map %d times, injected %+v", calls, *events)
			}
		})
	}
}
