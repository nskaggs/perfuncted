package output

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11ListerFallsBackWhenRandRResourcesRequestHasNoCookie(t *testing.T) {
	var queriedRoot xproto.Window
	lister := &X11Lister{
		randrAvailable: true,
		conn: &x11.MockConnection{
			DefaultScreenFunc: func() *xproto.ScreenInfo {
				return &xproto.ScreenInfo{Root: 99, WidthInPixels: 2560, HeightInPixels: 1440}
			},
			GetScreenResourcesCurrentFunc: func(root xproto.Window) x11.RandRScreenResourcesCookie {
				queriedRoot = root
				return nil
			},
		},
	}

	outputs, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("List after missing RandR resources cookie: %v", err)
	}
	if queriedRoot != 99 {
		t.Fatalf("RandR resources queried root %d, want 99", queriedRoot)
	}
	if len(outputs) != 1 || outputs[0].Name != "x11-root" || outputs[0].Geometry != (Geometry{W: 2560, H: 1440}) || !outputs[0].Available {
		t.Fatalf("missing-cookie fallback = %+v, want available root geometry 2560x1440", outputs)
	}
}
