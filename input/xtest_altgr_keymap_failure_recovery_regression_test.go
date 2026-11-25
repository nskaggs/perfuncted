//go:build linux

package input

import (
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestXTestAltGrPropagatesTransientKeymapFailureAndRecovers(t *testing.T) {
	failure := errors.New("synthetic X11 keyboard mapping failure")
	reads := 0
	conn := &x11.MockConnection{
		SetupFunc: func() *xproto.SetupInfo {
			return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 8}
		},
		GetKeyboardMappingFunc: func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
			reads++
			if reads == 1 {
				return x11.NewMockGetKeyboardMappingErrorCookie(failure)
			}
			return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
				KeysymsPerKeycode: 1,
				Keysyms:           []xproto.Keysym{isoLevel3ShiftKeysym},
			})
		},
	}
	backend := &XTestBackend{conn: conn}
	if code, err := backend.altGrKeycode(); code != 0 || !errors.Is(err, failure) {
		t.Fatalf("failed keymap resolved AltGr as %d, err=%v", code, err)
	}
	if code, err := backend.altGrKeycode(); code != 8 || err != nil {
		t.Fatalf("AltGr did not recover after transient failure: code=%d err=%v", code, err)
	}
	if reads != 2 {
		t.Fatalf("keymap read %d times, want one failed and one successful read", reads)
	}
}
