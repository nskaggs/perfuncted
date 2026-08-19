package output

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/nskaggs/perfuncted/internal/capability"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/wl"
)

type waylandOutput struct {
	globalID uint32
	mu       sync.RWMutex
	info     Info
}

// WaylandLister lists outputs through the wl_output protocol.
type WaylandLister struct {
	session *wl.Session
	outputs []*waylandOutput
}

// NewWaylandLister connects to the Wayland socket and discovers outputs.
func NewWaylandLister(sock string) (*WaylandLister, error) {
	if sock == "" {
		return nil, fmt.Errorf("output/wayland: WAYLAND_DISPLAY not set")
	}
	s, err := wl.NewSession(sock)
	if err != nil {
		return nil, fmt.Errorf("output/wayland: connect: %w", err)
	}
	l := &WaylandLister{session: s}

	initErr := wl.WithOperation(s.Ctx, func() error {
		for _, ev := range outputGlobals(s.GlobalsSnapshot()) {
			out := &waylandOutput{
				globalID: ev.Name,
				info: Info{
					Name:    fmt.Sprintf("wl-output-%d", ev.Name),
					Backend: "wayland",
					Scale:   1,
				},
			}
			l.outputs = append(l.outputs, out)
			proxy := &wl.RawProxy{}
			s.Ctx.Register(proxy)
			if err := s.Registry.Bind(ev.Name, "wl_output", minUint32(ev.Version, 4), proxy.ID()); err != nil {
				return fmt.Errorf("output/wayland: bind wl_output: %w", err)
			}
			out.updateProxy(proxy)
		}
		if len(l.outputs) == 0 {
			return capability.Unsupported("output", "wayland", "no wl_output globals advertised")
		}
		return s.Display.RoundTrip()
	})
	if initErr != nil {
		_ = s.Close()
		return nil, fmt.Errorf("output/wayland: initialize: %w", initErr)
	}
	return l, nil
}

func (o *waylandOutput) updateProxy(proxy *wl.RawProxy) { //nolint:gocyclo
	proxy.OnEvent = func(opcode uint32, _ int, data []byte) {
		o.mu.Lock()
		defer o.mu.Unlock()

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

func outputGlobals(globals []wl.GlobalEvent) []wl.GlobalEvent {
	outputGlobals := make([]wl.GlobalEvent, 0, len(globals))
	for _, ev := range globals {
		if ev.Interface == "wl_output" {
			outputGlobals = append(outputGlobals, ev)
		}
	}
	return outputGlobals
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
	if n <= 0 || off+n > len(data) {
		return "", off, false
	}
	end := off + n - 1
	if end >= len(data) || data[end] != 0 {
		return "", off, false
	}
	raw := string(data[off:end])
	padded := (n + 3) &^ 3
	if off+padded > len(data) {
		return "", off, false
	}
	return raw, off + padded, true
}

// List returns the outputs discovered from the Wayland compositor.
func (l *WaylandLister) List(ctx context.Context) ([]Info, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("output/wayland: list canceled: %w", err)
	}
	if l == nil || l.session == nil {
		return nil, capability.Unsupported("output", "wayland", "not available")
	}
	if err := l.session.SyncContext(ctx); err != nil {
		return nil, err
	}
	return l.snapshotOutputs(), nil
}

type outputSnapshot struct {
	globalID uint32
	info     Info
}

func (l *WaylandLister) snapshotOutputs() []Info {
	snapshots := make([]outputSnapshot, 0, len(l.outputs))
	for _, output := range l.outputs {
		if output != nil {
			output.mu.RLock()
		}
	}
	defer func() {
		for i := len(l.outputs) - 1; i >= 0; i-- {
			if l.outputs[i] != nil {
				l.outputs[i].mu.RUnlock()
			}
		}
	}()

	for _, output := range l.outputs {
		if output == nil {
			continue
		}
		snapshots = append(snapshots, outputSnapshot{
			globalID: output.globalID,
			info:     output.info,
		})
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].globalID < snapshots[j].globalID
	})
	out := make([]Info, len(snapshots))
	for i, snapshot := range snapshots {
		out[i] = snapshot.info
	}
	return out
}

// Close releases the Wayland connection.
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
