//go:build linux

package input

import (
	"context"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// A transient malformed keyboard map must be reported without panicking and
// must not be cached, allowing a later valid map to recover key injection.
func TestXTestKeyboardMappingNilSuccessReplyFailsClosedAndCanRecover(t *testing.T) {
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
	mock, ok := backend.conn.(*x11.MockConnection)
	if !ok {
		t.Fatalf("unexpected X11 transport %T", backend.conn)
	}
	queries := 0
	mock.GetKeyboardMappingFunc = func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
		queries++
		if queries == 1 {
			return x11.NewMockGetKeyboardMappingCookie(nil)
		}
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: []xproto.Keysym{0x61}})
	}
	err := backend.KeyDown(context.Background(), "a")
	if err == nil || !strings.Contains(err.Error(), "keyboard mapping") {
		t.Fatalf("nil map reply accepted: %v", err)
	}
	if len(*events) != 0 {
		t.Fatalf("failed map injected events: %+v", *events)
	}
	if err := backend.KeyDown(context.Background(), "a"); err != nil {
		t.Fatalf("recovered map query: %v", err)
	}
	if queries != 2 || len(*events) != 1 || (*events)[0].eventType != xproto.KeyPress {
		t.Fatalf("map retry=%d events=%+v", queries, *events)
	}
}
