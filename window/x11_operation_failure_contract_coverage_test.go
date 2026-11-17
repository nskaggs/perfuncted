//go:build linux

package window

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

type x11OperationErrorCookie struct{ err error }

func (c x11OperationErrorCookie) Check() error { return c.err }

func TestX11OperationsRejectInvalidIDsAndPreserveFirstFailure(t *testing.T) {
	t.Run("fullscreen rejects invalid ID before transport", func(t *testing.T) {
		b, conn := newStubX11Backend(t, false, "")
		sent := 0
		conn.SendEventCheckedFunc = func(bool, xproto.Window, uint32, string) x11.SendEventCookie {
			sent++
			return &x11.MockCheckCookie{}
		}
		if err := b.FullscreenByID(context.Background(), "bad-window-id"); err == nil {
			t.Fatal("FullscreenByID accepted an invalid native ID")
		}
		if sent != 0 {
			t.Fatalf("FullscreenByID sent %d events for an invalid ID", sent)
		}
	})

	t.Run("unfullscreen rejects invalid ID before transport", func(t *testing.T) {
		b, conn := newStubX11Backend(t, false, "")
		sent := 0
		conn.SendEventCheckedFunc = func(bool, xproto.Window, uint32, string) x11.SendEventCookie {
			sent++
			return &x11.MockCheckCookie{}
		}
		if err := b.UnfullscreenByID(context.Background(), "bad-window-id"); err == nil {
			t.Fatal("UnfullscreenByID accepted an invalid native ID")
		}
		if sent != 0 {
			t.Fatalf("UnfullscreenByID sent %d events for an invalid ID", sent)
		}
	})

	t.Run("restore returns first checked event error without mapping", func(t *testing.T) {
		wantErr := errors.New("synthetic state event rejected")
		b, conn := newStubX11Backend(t, false, "")
		sent, mapped := 0, 0
		conn.SendEventCheckedFunc = func(bool, xproto.Window, uint32, string) x11.SendEventCookie {
			sent++
			return x11OperationErrorCookie{err: wantErr}
		}
		conn.MapWindowCheckedFunc = func(xproto.Window) x11.MapWindowCookie {
			mapped++
			return &x11.MockCheckCookie{}
		}
		if err := b.RestoreByID(context.Background(), "50"); !errors.Is(err, wantErr) {
			t.Fatalf("RestoreByID error=%v, want checked-event error", err)
		}
		if sent != 1 || mapped != 0 {
			t.Fatalf("RestoreByID continued after failed state event: sent=%d mapped=%d", sent, mapped)
		}
	})

	t.Run("info rejects invalid ID before listing", func(t *testing.T) {
		b, conn := newStubX11Backend(t, false, "")
		queries := 0
		conn.GetPropertyFunc = func(bool, xproto.Window, xproto.Atom, xproto.Atom, uint32, uint32) x11.GetPropertyCookie {
			queries++
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32})
		}
		if info, err := b.InfoByID(context.Background(), "bad-window-id"); err == nil || info != (Info{}) {
			t.Fatalf("InfoByID returned info=%+v err=%v for an invalid native ID", info, err)
		}
		if queries != 0 {
			t.Fatalf("InfoByID made %d X11 queries for an invalid ID", queries)
		}
	})

	t.Run("info preserves list query failure", func(t *testing.T) {
		wantErr := errors.New("synthetic client-list transport failure")
		b, conn := newStubX11Backend(t, false, "")
		queries := 0
		conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
			if property == b.atomNetClientList {
				queries++
				return x11.NewMockGetPropertyCookieError(wantErr)
			}
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32})
		}
		if info, err := b.InfoByID(context.Background(), "50"); !errors.Is(err, wantErr) || info != (Info{}) {
			t.Fatalf("InfoByID returned info=%+v err=%v, want client-list transport failure", info, err)
		}
		if queries != 1 {
			t.Fatalf("InfoByID made %d client-list queries after the failure", queries)
		}
	})
}
