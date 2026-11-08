package output

import (
	"context"
	"testing"

	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// RandR None (zero) is not a physical monitor. A malformed resource list
// containing it must not prevent discovery of valid displays or trigger an
// invalid X11 GetOutputInfo request.
func TestX11ListerIgnoresInvalidZeroRandROutputID(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []randr.Output
		want string
	}{
		{"valid output after None", []randr.Output{0, 20}, "DP-1"},
		{"only None falls back to root", []randr.Output{0}, "x11-root"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			conn := &x11.MockConnection{
				DefaultScreenFunc: func() *xproto.ScreenInfo {
					return &xproto.ScreenInfo{Root: 99, WidthInPixels: 1920, HeightInPixels: 1080}
				},
				GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
					return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{ConfigTimestamp: 17, Outputs: tc.ids}, nil)
				},
				GetOutputInfoFunc: func(id randr.Output, _ xproto.Timestamp) x11.RandROutputInfoCookie {
					calls++
					if id == 0 {
						t.Fatalf("invalid output ID 0 queried")
					}
					return x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionConnected, Crtc: 200, Name: []byte("DP-1")}, nil)
				},
				GetCrtcInfoFunc: func(randr.Crtc, xproto.Timestamp) x11.RandRCrtcInfoCookie {
					return x11.NewMockRandRCrtcInfoCookie(&randr.GetCrtcInfoReply{Width: 1920, Height: 1080}, nil)
				},
			}
			lister := &X11Lister{conn: conn, randrAvailable: true}
			outputs, err := lister.List(context.Background())
			if err != nil || len(outputs) != 1 || outputs[0].Name != tc.want || calls != len(tc.ids)-1 {
				t.Fatalf("invalid RandR output disrupted list: outputs=%+v err=%v output queries=%d", outputs, err, calls)
			}
		})
	}
}
