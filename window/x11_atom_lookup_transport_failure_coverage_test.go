//go:build linux

package window

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

type failingX11AtomCookie struct{ err error }

func (c failingX11AtomCookie) Reply() (*xproto.InternAtomReply, error) {
	return nil, c.err
}

func TestX11WindowMutationsStopWhenAtomLookupFails(t *testing.T) {
	lookupErr := errors.New("synthetic atom lookup transport failure")
	for _, tc := range []struct {
		name, failedAtom string
		minimize         bool
	}{
		{name: "close delete atom", failedAtom: "WM_DELETE_WINDOW"},
		{name: "close protocols atom", failedAtom: "WM_PROTOCOLS"},
		{name: "minimize state atom", failedAtom: "WM_CHANGE_STATE", minimize: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			var lookups []string
			var sentEvents int
			conn.InternAtomFunc = func(_ bool, _ uint16, atom string) x11.InternAtomCookie {
				lookups = append(lookups, atom)
				if atom == tc.failedAtom {
					return failingX11AtomCookie{err: lookupErr}
				}
				return cancelAwareAtomReply{}
			}
			conn.SendEventCheckedFunc = func(_ bool, _ xproto.Window, _ uint32, _ string) x11.SendEventCookie {
				sentEvents++
				return &x11.MockCheckCookie{}
			}

			var err error
			if tc.minimize {
				err = b.MinimizeByID(context.Background(), "50")
			} else {
				err = b.CloseWindowByID(context.Background(), "50")
			}
			if !errors.Is(err, lookupErr) || !strings.Contains(err.Error(), tc.failedAtom) {
				t.Fatalf("mutation error = %v, want wrapped %s lookup error", err, tc.failedAtom)
			}
			if sentEvents != 0 {
				t.Fatalf("mutation sent %d X11 events after atom lookup failed", sentEvents)
			}
			if len(lookups) == 0 || lookups[len(lookups)-1] != tc.failedAtom {
				t.Fatalf("atom lookups = %v, want failure at %s", lookups, tc.failedAtom)
			}
			if tc.failedAtom == "WM_DELETE_WINDOW" && len(lookups) != 1 {
				t.Fatalf("close looked up atoms after delete-atom failure: %v", lookups)
			}
		})
	}
}
