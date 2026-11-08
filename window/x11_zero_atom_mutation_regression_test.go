//go:build linux

package window

import (
	"context"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

type zeroX11AtomCookie struct{}

func (zeroX11AtomCookie) Reply() (*xproto.InternAtomReply, error) {
	return &xproto.InternAtomReply{Atom: xproto.AtomNone}, nil
}

// A successful InternAtom(onlyIfExists=false) must return a nonzero atom ID.
// AtomNone cannot be used in client-message types or protocol payloads.
func TestX11WindowMutationsRejectZeroAtomReplies(t *testing.T) {
	for _, tc := range []struct {
		name, failAt string
		minimize     bool
	}{
		{"close delete atom none", "WM_DELETE_WINDOW", false},
		{"close protocols atom none", "WM_PROTOCOLS", false},
		{"minimize change state atom none", "WM_CHANGE_STATE", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			events := 0
			conn.InternAtomFunc = func(_ bool, _ uint16, name string) x11.InternAtomCookie {
				if name == tc.failAt {
					return zeroX11AtomCookie{}
				}
				return cancelAwareAtomReply{}
			}
			conn.SendEventCheckedFunc = func(_ bool, _ xproto.Window, _ uint32, _ string) x11.SendEventCookie {
				events++
				return &x11.MockCheckCookie{}
			}
			var err error
			if tc.minimize {
				err = b.MinimizeByID(context.Background(), "50")
			} else {
				err = b.CloseWindowByID(context.Background(), "50")
			}
			if err == nil || !strings.Contains(err.Error(), tc.failAt) || events != 0 {
				t.Fatalf("zero atom reply mishandled: err=%v events=%d", err, events)
			}
		})
	}
}
