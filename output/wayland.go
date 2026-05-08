package output

import (
	"context"
	"fmt"

	"github.com/nskaggs/perfuncted/internal/capability"
	"github.com/nskaggs/perfuncted/internal/wl"
)

type waylandOutput struct {
	info Info
}

type WaylandLister struct {
	session *wl.Session
	outputs []*waylandOutput
}

func NewWaylandLister(sock string) (*WaylandLister, error) {
	if sock == "" {
		return nil, fmt.Errorf("output/wayland: WAYLAND_DISPLAY not set")
	}
	ctx, err := wl.Connect(sock)
	if err != nil {
		return nil, fmt.Errorf("output/wayland: connect: %w", err)
	}
	display := wl.NewDisplay(ctx)
	registry, err := display.GetRegistry()
	if err != nil {
		_ = ctx.Close()
		return nil, fmt.Errorf("output/wayland: get registry: %w", err)
	}

	l := &WaylandLister{
		session: &wl.Session{Sock: sock, Ctx: ctx, Display: display, Registry: registry, Globals: make(map[string]wl.GlobalEvent)},
	}
	var globals []wl.GlobalEvent
	registry.SetGlobalHandler(func(ev wl.GlobalEvent) {
		globals = append(globals, ev)
	})
	if err := display.RoundTrip(); err != nil {
		_ = ctx.Close()
		return nil, fmt.Errorf("output/wayland: registry round-trip: %w", err)
	}

	for _, ev := range globals {
		if ev.Interface != "wl_output" {
			continue
		}
		out := &waylandOutput{
			info: Info{
				Name:    fmt.Sprintf("wl-output-%d", ev.Name),
				Backend: "wayland",
				Scale:   1,
			},
		}
		l.outputs = append(l.outputs, out)
		proxy := &wl.RawProxy{}
		ctx.Register(proxy)
		if err := registry.Bind(ev.Name, "wl_output", minUint32(ev.Version, 4), proxy.ID()); err != nil {
			_ = ctx.Close()
			return nil, fmt.Errorf("output/wayland: bind wl_output: %w", err)
		}
		out.updateProxy(proxy)
	}
	if len(l.outputs) == 0 {
		_ = ctx.Close()
		return nil, capability.Unsupported("output", "wayland", "no wl_output globals advertised")
	}
	if err := display.RoundTrip(); err != nil {
		_ = ctx.Close()
		return nil, fmt.Errorf("output/wayland: initial round-trip: %w", err)
	}
	return l, nil
}

func (o *waylandOutput) updateProxy(proxy *wl.RawProxy) {
	proxy.OnEvent = func(opcode uint32, _ int, data []byte) {
		switch opcode {
		case 0: // geometry
			if len(data) < 20 {
				return
			}
			o.info.Geometry.X = int(int32(wl.Uint32(data[0:4])))
			o.info.Geometry.Y = int(int32(wl.Uint32(data[4:8])))
			o.info.PhysicalW = int(int32(wl.Uint32(data[8:12])))
			o.info.PhysicalH = int(int32(wl.Uint32(data[12:16])))
			if make, model, ok := readWlStrings(data, 20); ok {
				o.info.Make = make
				o.info.Model = model
			}
		case 1: // mode
			if len(data) >= 16 {
				flags := wl.Uint32(data[0:4])
				w := int(wl.Uint32(data[4:8]))
				h := int(wl.Uint32(data[8:12]))
				if flags&1 != 0 {
					o.info.ResolutionW = w
					o.info.ResolutionH = h
					o.info.Geometry.W = w / maxInt(1, o.info.Scale)
					o.info.Geometry.H = h / maxInt(1, o.info.Scale)
				}
			}
		case 3: // scale
			if len(data) >= 4 {
				scale := int(wl.Uint32(data[0:4]))
				if scale <= 0 {
					scale = 1
				}
				o.info.Scale = scale
				if o.info.ResolutionW > 0 {
					o.info.Geometry.W = o.info.ResolutionW / scale
				}
				if o.info.ResolutionH > 0 {
					o.info.Geometry.H = o.info.ResolutionH / scale
				}
			}
		case 4: // name
			if name, _, ok := readWlString(data, 0); ok {
				o.info.Name = name
			}
		case 5: // description
			if desc, _, ok := readWlString(data, 0); ok {
				o.info.Description = desc
			}
		}
	}
}

func readWlStrings(data []byte, off int) (first, second string, ok bool) {
	a, next, ok := readWlString(data, off)
	if !ok {
		return "", "", false
	}
	b, _, ok := readWlString(data, next)
	if !ok {
		return a, "", false
	}
	return a, b, true
}

func readWlString(data []byte, off int) (string, int, bool) {
	if off+4 > len(data) {
		return "", off, false
	}
	n := int(wl.Uint32(data[off : off+4]))
	off += 4
	if n <= 0 || off+n > len(data)+1 {
		return "", off, false
	}
	end := off + n - 1
	if end > len(data) {
		return "", off, false
	}
	raw := string(data[off:end])
	padded := (n + 3) &^ 3
	return raw, off + int(padded), true
}

func (l *WaylandLister) List(ctx context.Context) ([]Info, error) {
	if l == nil || l.session == nil {
		return nil, capability.Unsupported("output", "wayland", "not available")
	}
	for _, out := range l.outputs {
		out.info.Backend = "wayland"
	}
	if err := l.session.Sync(); err != nil {
		return nil, err
	}
	out := make([]Info, 0, len(l.outputs))
	for _, o := range l.outputs {
		out = append(out, o.info)
	}
	return out, nil
}

func (l *WaylandLister) Close() error {
	if l.session != nil {
		return l.session.Close()
	}
	return nil
}

func minUint32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
