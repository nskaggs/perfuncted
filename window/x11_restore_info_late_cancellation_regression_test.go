//go:build linux

package window

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// A checked map can finish after the request is withdrawn. Restore should
// report that cancellation instead of confirming a completed operation.
func TestX11RestoreReportsCancellationDuringFinalMapRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, conn := newStubX11Backend(t, false, "")
	stateCalls, mapCalls := 0, 0
	conn.SendEventCheckedFunc = func(bool, xproto.Window, uint32, string) x11.SendEventCookie {
		stateCalls++
		return &x11.MockCheckCookie{}
	}
	conn.MapWindowCheckedFunc = func(xproto.Window) x11.MapWindowCookie {
		mapCalls++
		cancel()
		return &x11.MockCheckCookie{}
	}
	err := b.RestoreByID(ctx, "50")
	if !errors.Is(err, context.Canceled) || stateCalls != 1 || mapCalls != 1 {
		t.Fatalf("restore confirmed cancellation as success: err=%v state=%d map=%d", err, stateCalls, mapCalls)
	}
}

// The client list may be empty and the final active-window metadata round
// trip may cancel. A requested InfoByID should return cancellation instead
// of incorrectly classifying the window as absent.
func TestX11InfoByIDReportsCancellationAfterEmptyClientList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, conn := newStubX11Backend(t, false, "")
	original := conn.GetPropertyFunc
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, typ xproto.Atom, offset, length uint32) x11.GetPropertyCookie {
		if p == b.atomNetActiveWindow {
			cancel()
		}
		return original(del, w, p, typ, offset, length)
	}
	info, err := b.InfoByID(ctx, "50")
	if !errors.Is(err, context.Canceled) || info != (Info{}) {
		t.Fatalf("late cancellation classified as not-found: info=%+v err=%v", info, err)
	}
}
