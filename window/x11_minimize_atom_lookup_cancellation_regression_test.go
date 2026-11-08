//go:build linux

package window

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// A request canceled while resolving WM_CHANGE_STATE must not minimize a
// window after the caller has withdrawn the operation.
func TestX11MinimizeDoesNotSendEventAfterAtomLookupCancelsRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, conn := newStubX11Backend(t, false, "")
	atomCalls := 0
	events := 0
	conn.InternAtomFunc = func(_ bool, _ uint16, name string) x11.InternAtomCookie {
		atomCalls++
		if name != "WM_CHANGE_STATE" {
			t.Fatalf("unexpected atom lookup %q", name)
		}
		cancel()
		return minimizeAtomReply{}
	}
	conn.SendEventCheckedFunc = func(_ bool, _ xproto.Window, _ uint32, _ string) x11.SendEventCookie {
		events++
		return &x11.MockCheckCookie{}
	}
	err := b.MinimizeByID(ctx, "50")
	if !errors.Is(err, context.Canceled) || events != 0 || atomCalls != 1 {
		t.Fatalf("canceled minimize performed mutation: err=%v events=%d atomCalls=%d", err, events, atomCalls)
	}
}

type minimizeAtomReply struct{}

func (minimizeAtomReply) Reply() (*xproto.InternAtomReply, error) {
	return &xproto.InternAtomReply{Atom: 42}, nil
}
