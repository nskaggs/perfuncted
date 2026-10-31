//go:build linux

package input

import (
	"context"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestXTestPointerLocationFailsClosedOnEmptySuccessfulReply(t *testing.T) {
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
	mock, ok := backend.conn.(*x11.MockConnection)
	if !ok {
		t.Fatalf("unexpected X11 synthetic transport %T", backend.conn)
	}
	mock.QueryPointerFunc = func(xproto.Window) x11.QueryPointerCookie { return syntheticPointerReply{} }
	x, y, err := backend.PointerLocation(context.Background())
	if err == nil || !strings.Contains(err.Error(), "empty") || x != 0 || y != 0 {
		t.Fatalf("empty pointer reply returned x=%d y=%d err=%v", x, y, err)
	}
	if len(*events) != 0 {
		t.Fatalf("pointer query injected input events: %+v", *events)
	}
}
