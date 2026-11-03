package output

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11RandROutputAdversarialReplyMatrix(t *testing.T) {
	wireErr := errors.New("private RandR transport failed")
	cases := []struct {
		name         string
		info         x11.RandROutputInfoCookie
		crtc         x11.RandRCrtcInfoCookie
		wantError    string
		wantInactive bool
	}{
		{name: "missing output cookie", wantError: "no cookie"},
		{name: "output reply failure", info: x11.NewMockRandROutputInfoCookie(nil, wireErr), wantError: "private RandR transport failed"},
		{name: "missing output reply", info: x11.NewMockRandROutputInfoCookie(nil, nil), wantError: "no reply"},
		{name: "disconnected output", info: x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionDisconnected, Crtc: 7}, nil), wantInactive: true},
		{name: "connected but no CRTC", info: x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionConnected}, nil), wantInactive: true},
		{name: "missing CRTC cookie", info: x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionConnected, Crtc: 7}, nil), wantError: "no cookie"},
		{name: "CRTC reply error", info: x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionConnected, Crtc: 7}, nil), crtc: x11.NewMockRandRCrtcInfoCookie(nil, wireErr), wantError: "private RandR transport failed"},
		{name: "CRTC reply missing", info: x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionConnected, Crtc: 7}, nil), crtc: x11.NewMockRandRCrtcInfoCookie(nil, nil), wantInactive: true},
		{name: "zero CRTC width", info: x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionConnected, Crtc: 7}, nil), crtc: x11.NewMockRandRCrtcInfoCookie(&randr.GetCrtcInfoReply{Height: 1080}, nil), wantInactive: true},
		{name: "zero CRTC height", info: x11.NewMockRandROutputInfoCookie(&randr.GetOutputInfoReply{Connection: randr.ConnectionConnected, Crtc: 7}, nil), crtc: x11.NewMockRandRCrtcInfoCookie(&randr.GetCrtcInfoReply{Width: 1920}, nil), wantInactive: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &x11.MockConnection{
				GetOutputInfoFunc: func(id randr.Output, ts xproto.Timestamp) x11.RandROutputInfoCookie {
					if id != 23 || ts != 19 {
						t.Fatalf("wrong RandR output identity: %d, %d", id, ts)
					}
					return tc.info
				},
				GetCrtcInfoFunc: func(id randr.Crtc, ts xproto.Timestamp) x11.RandRCrtcInfoCookie {
					if id != 7 || ts != 19 {
						t.Fatalf("wrong CRTC identity: %d, %d", id, ts)
					}
					return tc.crtc
				},
			}
			out, active, err := (&X11Lister{conn: conn}).randrOutput(23, 19, 23)
			if tc.wantError != "" {
				if active || err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("reply error: out=%+v active=%v err=%v", out, active, err)
				}
			} else if !tc.wantInactive || active || err != nil || out != (Info{}) {
				t.Fatalf("inactive reply: out=%+v active=%v err=%v", out, active, err)
			}
		})
	}
}

func TestX11ListMissingDefaultScreenDoesNotPublishFallback(t *testing.T) {
	l := &X11Lister{conn: &x11.MockConnection{DefaultScreenFunc: func() *xproto.ScreenInfo { return nil }}}
	if out, err := l.List(context.Background()); len(out) != 0 || err == nil || !strings.Contains(err.Error(), "no default screen") {
		t.Fatalf("missing screen returned outputs=%+v error=%v", out, err)
	}
}
