//go:build linux

package window

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// Restore first clears maximization and then maps the window. If the caller
// cancels while the first mutation's X11 event completes, the second mutation
// must not be sent after the request is withdrawn.
func TestX11RestoreSkipsMapAfterCancellationDuringStateEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, conn := newStubX11Backend(t, false, "")
	stateEvents := 0
	mapCalls := 0
	conn.SendEventCheckedFunc = func(_ bool, _ xproto.Window, _ uint32, _ string) x11.SendEventCookie {
		stateEvents++
		cancel()
		return &x11.MockCheckCookie{}
	}
	conn.MapWindowCheckedFunc = func(_ xproto.Window) x11.MapWindowCookie {
		mapCalls++
		return &x11.MockCheckCookie{}
	}
	err := b.RestoreByID(ctx, "50")
	if !errors.Is(err, context.Canceled) || stateEvents != 1 || mapCalls != 0 {
		t.Fatalf("restore continued after cancellation: err=%v events=%d mapCalls=%d", err, stateEvents, mapCalls)
	}
}
