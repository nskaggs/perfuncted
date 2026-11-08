//go:build linux

package window

import (
	"context"
	"strings"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

type nilX11AtomCookie struct{}

func (nilX11AtomCookie) Reply() (*xproto.InternAtomReply, error) { return nil, nil }

// An absent atom reply is a protocol failure, not a valid reply with Atom 0.
// Reject it without sending a close/minimize mutation or panicking.
func TestX11WindowMutationsRejectMissingAtomReplies(t *testing.T) {
	for _, tc := range []struct {
		name, failAt string
		minimize     bool
	}{
		{"close missing delete atom", "WM_DELETE_WINDOW", false},
		{"close missing protocols atom", "WM_PROTOCOLS", false},
		{"minimize missing change state atom", "WM_CHANGE_STATE", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			events := 0
			conn.InternAtomFunc = func(_ bool, _ uint16, name string) x11.InternAtomCookie {
				if name == tc.failAt {
					return nilX11AtomCookie{}
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
				t.Fatalf("nil atom reply mishandled: err=%v events=%d", err, events)
			}
		})
	}
}
