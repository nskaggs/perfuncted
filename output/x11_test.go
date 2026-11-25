package output

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/randr"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
	"net"
	"strings"
	"testing"
	"unicode/utf8"
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

// The root fallback represents a drawable screen. A zero-sized root from a
// broken X server is not an available display and must not be advertised.
func TestX11ListerRejectsZeroSizedRootGeometry(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height uint16
		randr         bool
	}{
		{"missing width no RandR", 0, 1080, false},
		{"missing height no RandR", 1920, 0, false},
		{"zero root RandR unavailable", 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &x11.MockConnection{DefaultScreenFunc: func() *xproto.ScreenInfo {
				return &xproto.ScreenInfo{Root: 99, WidthInPixels: tc.width, HeightInPixels: tc.height}
			}}
			if tc.randr {
				conn.GetScreenResourcesCurrentFunc = func(xproto.Window) x11.RandRScreenResourcesCookie {
					return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{}, nil)
				}
			}
			lister := &X11Lister{conn: conn, randrAvailable: tc.randr}
			outputs, err := lister.List(context.Background())
			if err == nil || len(outputs) != 0 || !strings.Contains(err.Error(), "root screen") {
				t.Fatalf("invalid root geometry exposed outputs=%+v err=%v", outputs, err)
			}
		})
	}
}

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
