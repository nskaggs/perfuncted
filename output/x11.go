package output

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"slices"
	"sync/atomic"

	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// X11Lister lists active RandR outputs as display outputs.
type X11Lister struct {
	conn           x11.Connection
	randrAvailable bool
	closed         atomic.Bool
}

// NewX11Lister connects to an X11 display.
func NewX11Lister(display string) (*X11Lister, error) {
	conn, err := x11.NewXgbConnection(display)
	if err != nil {
		return nil, fmt.Errorf("output/x11: connect to display %q: %w", display, err)
	}
	return &X11Lister{conn: conn, randrAvailable: conn.InitRandR() == nil}, nil
}

// List returns the active connected RandR outputs in deterministic order.
func (l *X11Lister) List(ctx context.Context) ([]Info, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("output/x11: list canceled: %w", err)
	}
	if l == nil || l.conn == nil {
		return nil, fmt.Errorf("output/x11: lister is not initialised")
	}
	if l.closed.Load() {
		return nil, fmt.Errorf("output/x11: lister is closed: %w", net.ErrClosed)
	}
	screen := l.conn.DefaultScreen()
	if screen == nil {
		return nil, fmt.Errorf("output/x11: no default screen")
	}
	if !l.randrAvailable {
		return []Info{rootOutput(screen)}, nil
	}
	resources, ok := l.randrResources(screen.Root)
	if !ok {
		return []Info{rootOutput(screen)}, nil
	}
	outputs, err := l.listRandROutputs(screen.Root, resources)
	if err != nil {
		return nil, err
	}
	if len(outputs) == 0 {
		return []Info{rootOutput(screen)}, nil
	}
	return outputs, nil
}

func (l *X11Lister) randrResources(root xproto.Window) (*randr.GetScreenResourcesCurrentReply, bool) {
	cookie := l.conn.GetScreenResourcesCurrent(root)
	if cookie == nil {
		return nil, false
	}
	resources, err := cookie.Reply()
	return resources, err == nil && resources != nil
}

func (l *X11Lister) listRandROutputs(root xproto.Window, resources *randr.GetScreenResourcesCurrentReply) ([]Info, error) {

	primary := randr.Output(0)
	if primaryCookie := l.conn.GetOutputPrimary(root); primaryCookie != nil {
		if reply, primaryErr := primaryCookie.Reply(); primaryErr == nil && reply != nil {
			primary = reply.Output
		}
	}

	outputs := make([]Info, 0, len(resources.Outputs))
	for _, id := range resources.Outputs {
		info, active, err := l.randrOutput(id, resources.ConfigTimestamp, primary)
		if err != nil {
			return nil, err
		}
		if !active {
			continue
		}
		outputs = append(outputs, info)
	}
	slices.SortFunc(outputs, func(a, b Info) int {
		if c := cmp.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Geometry.X, b.Geometry.X); c != 0 {
			return c
		}
		return cmp.Compare(a.Geometry.Y, b.Geometry.Y)
	})
	return outputs, nil
}

func (l *X11Lister) randrOutput(id randr.Output, timestamp xproto.Timestamp, primary randr.Output) (Info, bool, error) {
	cookie := l.conn.GetOutputInfo(id, timestamp)
	if cookie == nil {
		return Info{}, false, fmt.Errorf("output/x11: get output info for %d returned no cookie", id)
	}
	outputInfo, err := cookie.Reply()
	if err != nil {
		return Info{}, false, fmt.Errorf("output/x11: get output info for %d: %w", id, err)
	}
	if outputInfo == nil {
		return Info{}, false, fmt.Errorf("output/x11: get output info for %d returned no reply", id)
	}
	// A connected output without a CRTC is present but inactive, and cannot
	// currently be targeted as a monitor.
	if outputInfo.Connection != randr.ConnectionConnected || outputInfo.Crtc == 0 {
		return Info{}, false, nil
	}
	crtcCookie := l.conn.GetCrtcInfo(outputInfo.Crtc, timestamp)
	if crtcCookie == nil {
		return Info{}, false, fmt.Errorf("output/x11: get CRTC info for %d returned no cookie", outputInfo.Crtc)
	}
	crtcInfo, err := crtcCookie.Reply()
	if err != nil {
		return Info{}, false, fmt.Errorf("output/x11: get CRTC info for %d: %w", outputInfo.Crtc, err)
	}
	if crtcInfo == nil || crtcInfo.Width == 0 || crtcInfo.Height == 0 {
		return Info{}, false, nil
	}
	name := trimX11Name(outputInfo.Name)
	if name == "" {
		name = fmt.Sprintf("x11-output-%d", id)
	}
	return Info{
		Name:             name,
		Backend:          "x11",
		Geometry:         Geometry{X: int(crtcInfo.X), Y: int(crtcInfo.Y), W: int(crtcInfo.Width), H: int(crtcInfo.Height)},
		ResolutionW:      int(crtcInfo.Width),
		ResolutionH:      int(crtcInfo.Height),
		Scale:            1,
		ScaleNumerator:   1,
		ScaleDenominator: 1,
		PhysicalW:        int(outputInfo.MmWidth),
		PhysicalH:        int(outputInfo.MmHeight),
		Primary:          id == primary,
		Available:        true,
	}, true, nil
}

// rootOutput preserves useful single-canvas behavior on X servers that do
// not expose a usable RandR 1.3+ output topology.
func rootOutput(screen *xproto.ScreenInfo) Info {
	return Info{
		Name:             "x11-root",
		Backend:          "x11",
		Geometry:         Geometry{W: int(screen.WidthInPixels), H: int(screen.HeightInPixels)},
		ResolutionW:      int(screen.WidthInPixels),
		ResolutionH:      int(screen.HeightInPixels),
		Scale:            1,
		ScaleNumerator:   1,
		ScaleDenominator: 1,
		Primary:          true,
		Available:        true,
	}
}

func trimX11Name(raw []byte) string {
	for len(raw) > 0 && raw[len(raw)-1] == 0 {
		raw = raw[:len(raw)-1]
	}
	return string(raw)
}

// Close releases the X11 connection.
func (l *X11Lister) Close() error {
	if l == nil || l.closed.Swap(true) {
		return nil
	}
	if l.conn != nil {
		l.conn.Close()
	}
	return nil
}
