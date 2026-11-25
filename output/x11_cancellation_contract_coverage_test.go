package output

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/third_party/xgb/randr"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11ListerRejectsUninitializedBackend(t *testing.T) {
	for _, lister := range []*X11Lister{nil, &X11Lister{}} {
		outputs, err := lister.List(context.Background())
		if len(outputs) != 0 || err == nil {
			t.Fatalf("uninitialized lister returned outputs=%+v err=%v", outputs, err)
		}
	}
}

func TestX11ListerCancellationAtProtocolBoundariesDiscardsSnapshot(t *testing.T) {
	t.Run("screen lookup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		resourceCalls := 0
		conn := &x11.MockConnection{
			DefaultScreenFunc: func() *xproto.ScreenInfo {
				cancel()
				return &xproto.ScreenInfo{Root: 99, WidthInPixels: 1920, HeightInPixels: 1080}
			},
			GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
				resourceCalls++
				return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{}, nil)
			},
		}
		outputs, err := (&X11Lister{conn: conn, randrAvailable: true}).List(ctx)
		if len(outputs) != 0 || !errors.Is(err, context.Canceled) || resourceCalls != 0 {
			t.Fatalf("canceled screen lookup returned outputs=%+v err=%v resource calls=%d", outputs, err, resourceCalls)
		}
	})

	t.Run("resource lookup", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		primaryCalls := 0
		conn := &x11.MockConnection{
			DefaultScreenFunc: func() *xproto.ScreenInfo {
				return &xproto.ScreenInfo{Root: 99, WidthInPixels: 1920, HeightInPixels: 1080}
			},
			GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
				cancel()
				return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{
					Outputs: []randr.Output{10},
				}, nil)
			},
			GetOutputPrimaryFunc: func(xproto.Window) x11.RandROutputPrimaryCookie {
				primaryCalls++
				return x11.NewMockRandROutputPrimaryCookie(&randr.GetOutputPrimaryReply{Output: 10}, nil)
			},
		}
		outputs, err := (&X11Lister{conn: conn, randrAvailable: true}).List(ctx)
		if len(outputs) != 0 || !errors.Is(err, context.Canceled) || primaryCalls != 0 {
			t.Fatalf("canceled resource lookup returned outputs=%+v err=%v primary calls=%d", outputs, err, primaryCalls)
		}
	})

	t.Run("before output enumeration", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		outputInfoCalls := 0
		conn := &x11.MockConnection{
			DefaultScreenFunc: func() *xproto.ScreenInfo {
				return &xproto.ScreenInfo{Root: 99, WidthInPixels: 1920, HeightInPixels: 1080}
			},
			GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
				return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{
					Outputs: []randr.Output{10},
				}, nil)
			},
			GetOutputPrimaryFunc: func(xproto.Window) x11.RandROutputPrimaryCookie {
				cancel()
				return x11.NewMockRandROutputPrimaryCookie(&randr.GetOutputPrimaryReply{Output: 10}, nil)
			},
			GetOutputInfoFunc: func(randr.Output, xproto.Timestamp) x11.RandROutputInfoCookie {
				outputInfoCalls++
				return x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionDisconnected}, nil)
			},
		}
		outputs, err := (&X11Lister{conn: conn, randrAvailable: true}).List(ctx)
		if len(outputs) != 0 || !errors.Is(err, context.Canceled) || outputInfoCalls != 0 {
			t.Fatalf("canceled enumeration returned outputs=%+v err=%v output info calls=%d", outputs, err, outputInfoCalls)
		}
	})

	t.Run("during output resolution", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		conn := &x11.MockConnection{
			DefaultScreenFunc: func() *xproto.ScreenInfo {
				return &xproto.ScreenInfo{Root: 99, WidthInPixels: 1920, HeightInPixels: 1080}
			},
			GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
				return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{
					Outputs: []randr.Output{10}, ConfigTimestamp: 17,
				}, nil)
			},
			GetOutputInfoFunc: func(randr.Output, xproto.Timestamp) x11.RandROutputInfoCookie {
				return x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{
					Connection: randr.ConnectionConnected, Crtc: 20, Name: []byte("DP-1"),
				}, nil)
			},
			GetCrtcInfoFunc: func(randr.Crtc, xproto.Timestamp) x11.RandRCrtcInfoCookie {
				cancel()
				return x11.NewMockRandRCrtcInfoCookie(&randr.GetCrtcInfoReply{Width: 1920, Height: 1080}, nil)
			},
		}
		outputs, err := (&X11Lister{conn: conn, randrAvailable: true}).List(ctx)
		if len(outputs) != 0 || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled output resolution published a partial snapshot: outputs=%+v err=%v", outputs, err)
		}
	})
}

func TestX11ListerPropagatesRandROutputFailureWithoutRootFallback(t *testing.T) {
	wantErr := errors.New("RandR output query failed")
	conn := &x11.MockConnection{
		DefaultScreenFunc: func() *xproto.ScreenInfo {
			return &xproto.ScreenInfo{Root: 99, WidthInPixels: 1920, HeightInPixels: 1080}
		},
		GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
			return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{
				Outputs: []randr.Output{10},
			}, nil)
		},
		GetOutputInfoFunc: func(randr.Output, xproto.Timestamp) x11.RandROutputInfoCookie {
			return x11.NewMockRandROutputInfoCookie(nil, wantErr)
		},
	}
	outputs, err := (&X11Lister{conn: conn, randrAvailable: true}).List(context.Background())
	if len(outputs) != 0 || !errors.Is(err, wantErr) {
		t.Fatalf("RandR failure returned outputs=%+v err=%v", outputs, err)
	}
}
