package output

import (
	"testing"
	"unicode/utf8"

	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// RandR output names cross a protocol boundary and are exposed as public
// identifiers. Reject invalid UTF-8 and embedded NUL to avoid ambiguous IDs.
func TestX11RandROutputNamesRejectMalformedWireStrings(t *testing.T) {
	cases := []struct {
		name string
		wire []byte
		want string
	}{
		{"invalid UTF-8", []byte{'D', 'P', '-', 0xff}, "x11-output-23"},
		{"embedded NUL", []byte("DP-1\x00forged"), "x11-output-23"},
		{"valid trailing padding", []byte("DP-1\x00\x00"), "DP-1"},
		{"empty name", []byte{0}, "x11-output-23"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &x11.MockConnection{
				GetOutputInfoFunc: func(randr.Output, xproto.Timestamp) x11.RandROutputInfoCookie {
					return x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionConnected, Crtc: 7, Name: tc.wire}, nil)
				},
				GetCrtcInfoFunc: func(randr.Crtc, xproto.Timestamp) x11.RandRCrtcInfoCookie {
					return x11.NewMockRandRCrtcInfoCookie(&randr.GetCrtcInfoReply{Width: 1920, Height: 1080}, nil)
				},
			}
			info, active, err := (&X11Lister{conn: conn}).randrOutput(23, 19, 0)
			if err != nil || !active || info.Name != tc.want || !utf8.ValidString(info.Name) {
				t.Fatalf("output name %q (active=%v err=%v), want %q", info.Name, active, err, tc.want)
			}
		})
	}
}
