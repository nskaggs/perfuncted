//go:build linux

package input

import (
	"context"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// A successful X server reply with no keysyms per keycode cannot be used for
// injection or cached. Once a valid map arrives, the same live backend must
// recover without retaining the malformed layout as authority.
func TestXTestZeroKeysymsPerKeycodeFailsClosedThenRecovers(t *testing.T) {
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{'a'}, nil)
	conn, ok := b.conn.(*x11.MockConnection)
	if !ok {
		t.Fatalf("unexpected test connection: %T", b.conn)
	}
	requests := 0
	conn.GetKeyboardMappingFunc = func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
		requests++
		if requests <= 2 {
			return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 0, Keysyms: []xproto.Keysym{'a'}})
		}
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: []xproto.Keysym{'a'}})
	}
	for i := 0; i < 2; i++ {
		if err := b.KeyDown(context.Background(), "a"); err == nil || !strings.Contains(err.Error(), "keysyms_per_keycode=0") {
			t.Fatalf("malformed server keyboard mapping attempt %d accepted: %v", i, err)
		}
		if len(*events) != 0 || b.keymap != nil {
			t.Fatalf("malformed keyboard mapping authorized key events=%+v cache=%+v", *events, b.keymap)
		}
	}
	if err := b.KeyDown(context.Background(), "a"); err != nil {
		t.Fatalf("recovered X11 map rejected: %v", err)
	}
	if requests != 3 || len(*events) != 1 || (*events)[0].eventType != xproto.KeyPress {
		t.Fatalf("transient keyboard map not retried: requests=%d events=%+v", requests, *events)
	}
}
