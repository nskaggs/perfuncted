package output

import (
	"context"
	"testing"

	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// A malformed RandR resource list can repeat the same physical output ID.
// Expose it once, regardless of how often the server repeats that identity.
func TestX11ListerDeduplicatesRepeatedRandROutputIDs(t *testing.T) {
	queries := 0
	conn := &x11.MockConnection{
		DefaultScreenFunc: func() *xproto.ScreenInfo {
			return &xproto.ScreenInfo{Root: 99, WidthInPixels: 1920, HeightInPixels: 1080}
		},
		GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
			return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{ConfigTimestamp: 7, Outputs: []randr.Output{20, 20}}, nil)
		},
		GetOutputInfoFunc: func(id randr.Output, _ xproto.Timestamp) x11.RandROutputInfoCookie {
			queries++
			if id != 20 {
				t.Fatalf("unexpected output %d", id)
			}
			return x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionConnected, Crtc: 200, Name: []byte("DP-1")}, nil)
		},
		GetCrtcInfoFunc: func(randr.Crtc, xproto.Timestamp) x11.RandRCrtcInfoCookie {
			return x11.NewMockRandRCrtcInfoCookie(&randr.GetCrtcInfoReply{Width: 1920, Height: 1080}, nil)
		},
	}
	lister := &X11Lister{conn: conn, randrAvailable: true}
	outputs, err := lister.List(context.Background())
	if err != nil || len(outputs) != 1 || outputs[0].Name != "DP-1" || queries != 1 {
		t.Fatalf("repeated RandR output published twice: outputs=%+v err=%v queries=%d", outputs, err, queries)
	}
}
