//go:build linux

package window

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11CloseDoesNotSendEventWhenAtomLookupCancelsRequest(t *testing.T) {
	for _, stage := range []string{"WM_DELETE_WINDOW", "WM_PROTOCOLS"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, conn := newStubX11Backend(t, false, "")
			calls := 0
			events := 0
			conn.InternAtomFunc = func(_ bool, _ uint16, name string) x11.InternAtomCookie {
				calls++
				if name == stage {
					cancel()
				}
				return cancelAwareAtomReply{}
			}
			conn.SendEventCheckedFunc = func(_ bool, _ xproto.Window, _ uint32, _ string) x11.SendEventCookie {
				events++
				return &x11.MockCheckCookie{}
			}
			err := b.CloseWindowByID(ctx, "50")
			if !errors.Is(err, context.Canceled) || events != 0 {
				t.Fatalf("canceled close continued: err=%v events=%d atomLookups=%d", err, events, calls)
			}
			if stage == "WM_DELETE_WINDOW" && calls != 1 {
				t.Fatalf("canceled after first atom lookup but made %d lookups", calls)
			}
		})
	}
}

type cancelAwareAtomReply struct{}

func (cancelAwareAtomReply) Reply() (*xproto.InternAtomReply, error) {
	return &xproto.InternAtomReply{Atom: 42}, nil
}
