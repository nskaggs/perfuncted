package output

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11ListerListRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	lister := &X11Lister{conn: &x11.MockConnection{}}
	if _, err := lister.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("List error = %v, want context.Canceled", err)
	}
}

func TestX11ListerRejectsListAfterClose(t *testing.T) {
	lister := &X11Lister{conn: &x11.MockConnection{}}
	if err := lister.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := lister.List(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("List error = %v, want net.ErrClosed", err)
	}
}

func TestX11ListerListsActiveRandROutputs(t *testing.T) { //nolint:gocyclo // table-driven mock protocol setup.
	conn := &x11.MockConnection{
		DefaultScreenFunc: func() *xproto.ScreenInfo {
			return &xproto.ScreenInfo{Root: 99, WidthInPixels: 3840, HeightInPixels: 2160}
		},
		GetScreenResourcesCurrentFunc: func(window xproto.Window) x11.RandRScreenResourcesCookie {
			if window != 99 {
				t.Fatalf("resources window = %d, want root 99", window)
			}
			return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{
				ConfigTimestamp: 17,
				Outputs:         []randr.Output{20, 10, 30},
			}, nil)
		},
		GetOutputPrimaryFunc: func(window xproto.Window) x11.RandROutputPrimaryCookie {
			if window != 99 {
				t.Fatalf("primary window = %d, want root 99", window)
			}
			return x11.NewMockRandROutputPrimaryCookie(&randr.GetOutputPrimaryReply{Output: 20}, nil)
		},
		GetOutputInfoFunc: func(id randr.Output, timestamp xproto.Timestamp) x11.RandROutputInfoCookie {
			if timestamp != 17 {
				t.Fatalf("output %d timestamp = %d, want 17", id, timestamp)
			}
			infos := map[randr.Output]*randr.GetOutputInfoReply{
				10: {Connection: randr.ConnectionConnected, Crtc: 100, MmWidth: 600, MmHeight: 340, Name: []byte("DP-2\x00")},
				20: {Connection: randr.ConnectionConnected, Crtc: 200, MmWidth: 700, MmHeight: 400, Name: []byte("DP-1\x00")},
				30: {Connection: randr.ConnectionDisconnected, Name: []byte("HDMI-3\x00")},
			}
			return x11.NewMockRandROutputInfoCookie(infos[id], nil)
		},
		GetCrtcInfoFunc: func(id randr.Crtc, timestamp xproto.Timestamp) x11.RandRCrtcInfoCookie {
			if timestamp != 17 {
				t.Fatalf("CRTC %d timestamp = %d, want 17", id, timestamp)
			}
			infos := map[randr.Crtc]*randr.GetCrtcInfoReply{
				100: {X: 1920, Y: 0, Width: 1920, Height: 1080},
				200: {X: 0, Y: 0, Width: 1920, Height: 1080},
			}
			return x11.NewMockRandRCrtcInfoCookie(infos[id], nil)
		},
	}
	lister := &X11Lister{conn: conn, randrAvailable: true}

	got, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d outputs, want 2: %+v", len(got), got)
	}
	if got[0].Name != "DP-1" || got[1].Name != "DP-2" {
		t.Fatalf("output names = %q, %q, want DP-1, DP-2", got[0].Name, got[1].Name)
	}
	if got[0].Geometry != (Geometry{X: 0, Y: 0, W: 1920, H: 1080}) ||
		got[1].Geometry != (Geometry{X: 1920, Y: 0, W: 1920, H: 1080}) {
		t.Fatalf("output geometry = %+v, %+v", got[0].Geometry, got[1].Geometry)
	}
	if !got[0].Primary || got[1].Primary {
		t.Fatalf("primary flags = %v, %v, want true, false", got[0].Primary, got[1].Primary)
	}
	for _, info := range got {
		if !info.Available || info.Scale != 1 || info.ScaleNumerator != 1 || info.ScaleDenominator != 1 {
			t.Fatalf("output availability/scale = %+v", info)
		}
	}
}

func TestX11ListerFallsBackWhenRandRResourcesFail(t *testing.T) {
	want := errors.New("resources failed")
	lister := &X11Lister{randrAvailable: true, conn: &x11.MockConnection{
		DefaultScreenFunc: func() *xproto.ScreenInfo {
			return &xproto.ScreenInfo{Root: 99, WidthInPixels: 1280, HeightInPixels: 720}
		},
		GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
			return x11.NewMockRandRScreenResourcesCookie(nil, want)
		},
	}}
	got, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("List error = %v, want root fallback", err)
	}
	if len(got) != 1 || got[0].Name != "x11-root" || got[0].Geometry != (Geometry{W: 1280, H: 720}) {
		t.Fatalf("fallback output = %+v, want x11-root 1280x720 after %v", got, want)
	}
}

func TestX11ListerFallsBackToRootWithoutRandR(t *testing.T) {
	lister := &X11Lister{conn: &x11.MockConnection{
		DefaultScreenFunc: func() *xproto.ScreenInfo {
			return &xproto.ScreenInfo{Root: 99, WidthInPixels: 1920, HeightInPixels: 1080}
		},
	}}

	got, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != "x11-root" || got[0].Geometry != (Geometry{W: 1920, H: 1080}) || !got[0].Available {
		t.Fatalf("fallback output = %+v, want available x11-root 1920x1080", got)
	}
}

func TestX11ListerFallsBackToRootWhenNoActiveOutputs(t *testing.T) {
	lister := &X11Lister{randrAvailable: true, conn: &x11.MockConnection{
		DefaultScreenFunc: func() *xproto.ScreenInfo {
			return &xproto.ScreenInfo{Root: 99, WidthInPixels: 2560, HeightInPixels: 1440}
		},
		GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
			return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{Outputs: []randr.Output{10}}, nil)
		},
		GetOutputInfoFunc: func(randr.Output, xproto.Timestamp) x11.RandROutputInfoCookie {
			return x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionDisconnected}, nil)
		},
	}}

	got, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Name != "x11-root" {
		t.Fatalf("fallback output = %+v, want x11-root", got)
	}
}
