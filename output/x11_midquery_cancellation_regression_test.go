package output

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11OutputListRejectsCancellationAfterDefaultScreenRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lister := &X11Lister{conn: &x11.MockConnection{DefaultScreenFunc: func() *xproto.ScreenInfo {
		cancel()
		return &xproto.ScreenInfo{Root: 42, WidthInPixels: 1920, HeightInPixels: 1080}
	}}}
	got, err := lister.List(ctx)
	if !errors.Is(err, context.Canceled) || len(got) != 0 {
		t.Fatalf("canceled output list returned %#v, %v", got, err)
	}
}

func TestX11OutputListStopsBeforeNextRandRRequestAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	secondQuery := false
	lister := &X11Lister{randrAvailable: true, conn: &x11.MockConnection{
		DefaultScreenFunc: func() *xproto.ScreenInfo {
			return &xproto.ScreenInfo{Root: 42, WidthInPixels: 1920, HeightInPixels: 1080}
		},
		GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
			return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{Outputs: []randr.Output{10, 20}}, nil)
		},
		GetOutputInfoFunc: func(id randr.Output, _ xproto.Timestamp) x11.RandROutputInfoCookie {
			if id == 20 {
				secondQuery = true
			}
			if id == 10 {
				cancel()
			}
			return x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionConnected, Crtc: 10, Name: []byte("DP-1")}, nil)
		},
		GetCrtcInfoFunc: func(randr.Crtc, xproto.Timestamp) x11.RandRCrtcInfoCookie {
			return x11.NewMockRandRCrtcInfoCookie(&randr.GetCrtcInfoReply{Width: 1920, Height: 1080}, nil)
		},
	}}
	got, err := lister.List(ctx)
	if !errors.Is(err, context.Canceled) || len(got) != 0 || secondQuery {
		t.Fatalf("canceled RandR query disclosed outputs=%#v error=%v contactedNext=%v", got, err, secondQuery)
	}
}
