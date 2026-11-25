package output

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted/internal/third_party/xgb/randr"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11ListerOrdersDuplicateOutputNamesByGeometry(t *testing.T) {
	for _, tc := range []struct {
		name string
		crtc map[randr.Crtc]*randr.GetCrtcInfoReply
		want []Geometry
	}{
		{
			name: "horizontal position",
			crtc: map[randr.Crtc]*randr.GetCrtcInfoReply{
				100: {X: 1920, Width: 1920, Height: 1080},
				200: {X: 0, Width: 1920, Height: 1080},
			},
			want: []Geometry{{X: 0, W: 1920, H: 1080}, {X: 1920, W: 1920, H: 1080}},
		},
		{
			name: "vertical position after equal horizontal position",
			crtc: map[randr.Crtc]*randr.GetCrtcInfoReply{
				100: {Y: 1080, Width: 1920, Height: 1080},
				200: {Y: 0, Width: 1920, Height: 1080},
			},
			want: []Geometry{{X: 0, Y: 0, W: 1920, H: 1080}, {X: 0, Y: 1080, W: 1920, H: 1080}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lister := &X11Lister{randrAvailable: true, conn: &x11.MockConnection{
				DefaultScreenFunc: func() *xproto.ScreenInfo {
					return &xproto.ScreenInfo{Root: 99, WidthInPixels: 3840, HeightInPixels: 2160}
				},
				GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
					return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{
						ConfigTimestamp: 7,
						Outputs:         []randr.Output{10, 20},
					}, nil)
				},
				GetOutputInfoFunc: func(id randr.Output, _ xproto.Timestamp) x11.RandROutputInfoCookie {
					crtc := map[randr.Output]randr.Crtc{10: 100, 20: 200}[id]
					return x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{
						Connection: randr.ConnectionConnected,
						Crtc:       crtc,
						Name:       []byte("DUP-1"),
					}, nil)
				},
				GetCrtcInfoFunc: func(id randr.Crtc, _ xproto.Timestamp) x11.RandRCrtcInfoCookie {
					return x11.NewMockRandRCrtcInfoCookie(tc.crtc[id], nil)
				},
			}}

			got, err := lister.List(context.Background())
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("List returned %d outputs, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i].Name != "DUP-1" || got[i].Geometry != tc.want[i] {
					t.Fatalf("output %d = %+v, want duplicate-name geometry %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
