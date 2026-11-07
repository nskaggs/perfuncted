package input

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// Cancellation during a synchronous X11 pointer reply revokes permission to
// publish its coordinates, even if the transport successfully completed.
func TestXTestPointerLocationHidesCoordinatesCanceledDuringReply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mock := &x11.MockConnection{}
	mock.QueryPointerFunc = func(xproto.Window) x11.QueryPointerCookie {
		cancel()
		return syntheticPointerReply{reply: &xproto.QueryPointerReply{RootX: 321, RootY: 654}}
	}
	backend := &XTestBackend{conn: mock, root: 1}
	x, y, err := backend.PointerLocation(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("query returned error %v", err)
	}
	if x != 0 || y != 0 {
		t.Fatalf("canceled query revealed pointer position (%d,%d)", x, y)
	}
}
