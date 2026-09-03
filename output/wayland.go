package output

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/nskaggs/perfuncted/internal/capability"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/wl"
)

type waylandOutput struct {
	globalID       uint32
	version        uint32
	proxy          *wl.RawProxy
	xdgProxy       *wl.RawProxy
	baseX          int
	baseY          int
	logicalX       int
	logicalY       int
	logicalW       int
	logicalH       int
	hasLogicalPos  bool
	hasLogicalSize bool
	mu             sync.RWMutex
	info           Info
}

// WaylandLister lists outputs through the wl_output protocol.
type WaylandLister struct {
	session    *wl.Session
	outputs    []*waylandOutput
	xdgManager *wl.RawProxy
	topologyMu sync.Mutex
	outputsMu  sync.RWMutex
	closed     atomic.Bool
	stopCtx    context.Context //nolint:containedctx // lister owns this lifecycle context.
	stopCancel context.CancelFunc
	closeOnce  sync.Once
	closeErr   error
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
	stopCtx, stopCancel := context.WithCancel(context.Background())
	l := &WaylandLister{session: s, stopCtx: stopCtx, stopCancel: stopCancel}

	initErr := wl.WithOperation(s.Ctx, func() error {
		globals := s.GlobalsSnapshot()
		for _, ev := range outputGlobals(globals) {
			out := newWaylandOutput(ev)
			if err := l.bindOutput(context.Background(), out, ev); err != nil {
				return err
			}
			l.outputs = append(l.outputs, out)
		}
		if len(l.outputs) == 0 {
			return capability.Unsupported("output", "wayland", "no wl_output globals advertised")
		}
		for _, ev := range globals {
			if ev.Interface != "zxdg_output_manager_v1" {
				continue
			}
			l.xdgManager = &wl.RawProxy{}
			s.Ctx.Register(l.xdgManager)
			if err := s.Registry.Bind(ev.Name, ev.Interface, minUint32(ev.Version, 3), l.xdgManager.ID()); err != nil {
				return fmt.Errorf("output/wayland: bind xdg-output manager: %w", err)
			}
			break
		}
		if err := s.Display.RoundTrip(); err != nil {
			return err
		}
		if l.xdgManager != nil {
			for _, out := range l.outputs {
				if err := l.bindXDGOutput(context.Background(), out); err != nil {
					return err
				}
			}
			return s.Display.RoundTrip()
		}
		return nil
	})
	if initErr != nil {
		_ = s.Close()
		return nil, fmt.Errorf("output/wayland: initialize: %w", initErr)
	}
	return l, nil
}

func newWaylandOutput(ev wl.GlobalEvent) *waylandOutput {
	return &waylandOutput{
		globalID: ev.Name,
		version:  minUint32(ev.Version, 4),
		info: Info{
			Name:             fmt.Sprintf("wl-output-%d", ev.Name),
			Backend:          "wayland",
			Scale:            1,
			ScaleNumerator:   1,
			ScaleDenominator: 1,
			Available:        true,
		},
	}
}

func (l *WaylandLister) bindOutput(ctx context.Context, out *waylandOutput, ev wl.GlobalEvent) error {
	proxy := &wl.RawProxy{}
	l.session.Ctx.Register(proxy)
	version := minUint32(ev.Version, 4)
	if err := l.session.Registry.BindContext(ctx, ev.Name, "wl_output", version, proxy.ID()); err != nil {
		wl.Unregister(l.session.Ctx, proxy)
		return fmt.Errorf("output/wayland: bind wl_output: %w", err)
	}
	out.version = version
	out.proxy = proxy
	out.updateProxy(proxy)
	return nil
}

func (l *WaylandLister) bindXDGOutput(ctx context.Context, out *waylandOutput) error {
	if l.xdgManager == nil || out == nil || out.proxy == nil {
		return nil
	}
	proxy := &wl.RawProxy{}
	l.session.Ctx.Register(proxy)
	buf := make([]byte, 0, 16)
	buf = appendUint32(buf, l.xdgManager.ID())
	buf = appendUint32(buf, 16<<16|1) // zxdg_output_manager_v1.get_xdg_output
	buf = appendUint32(buf, proxy.ID())
	buf = appendUint32(buf, out.proxy.ID())
	if err := l.session.Ctx.WriteMsgContext(ctx, buf, nil); err != nil {
		wl.Unregister(l.session.Ctx, proxy)
		return fmt.Errorf("output/wayland: bind xdg output: %w", err)
	}
	out.xdgProxy = proxy
	out.updateXDGProxy(proxy)
	return nil
}

func appendUint32(buf []byte, value uint32) []byte {
	var data [4]byte
	wl.PutUint32(data[:], value)
	return append(buf, data[:]...)
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
			o.baseX = int(int32(wl.Uint32(data[0:4])))
			o.baseY = int(int32(wl.Uint32(data[4:8])))
			if !o.hasLogicalPos {
				o.info.Geometry.X = o.baseX
				o.info.Geometry.Y = o.baseY
			}
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
					o.recomputeGeometryLocked()
				}
			}
		case 3: // scale
			if len(data) >= 4 {
				scale := int(wl.Uint32(data[0:4]))
				if scale <= 0 {
					scale = 1
				}
				o.info.Scale = scale
				o.info.ScaleNumerator = scale
				o.info.ScaleDenominator = 1
				o.recomputeGeometryLocked()
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

func (o *waylandOutput) updateXDGProxy(proxy *wl.RawProxy) {
	proxy.OnEvent = func(opcode uint32, _ int, data []byte) {
		o.mu.Lock()
		defer o.mu.Unlock()

		switch opcode {
		case 0: // logical_position
			if len(data) < 8 {
				return
			}
			o.logicalX = int(int32(wl.Uint32(data[0:4])))
			o.logicalY = int(int32(wl.Uint32(data[4:8])))
			o.hasLogicalPos = true
			o.recomputeGeometryLocked()
		case 1: // logical_size
			if len(data) < 8 {
				return
			}
			w := int(int32(wl.Uint32(data[0:4])))
			h := int(int32(wl.Uint32(data[4:8])))
			if w <= 0 || h <= 0 {
				return
			}
			o.logicalW = w
			o.logicalH = h
			o.hasLogicalSize = true
			o.recomputeGeometryLocked()
		case 3: // name
			if name, _, ok := readWlString(data, 0); ok {
				o.info.Name = name
			}
		case 4: // description
			if desc, _, ok := readWlString(data, 0); ok {
				o.info.Description = desc
			}
		}
	}
}

func (o *waylandOutput) recomputeGeometryLocked() {
	if o.hasLogicalPos {
		o.info.Geometry.X = o.logicalX
		o.info.Geometry.Y = o.logicalY
	} else {
		o.info.Geometry.X = o.baseX
		o.info.Geometry.Y = o.baseY
	}
	if o.hasLogicalSize {
		o.info.Geometry.W = o.logicalW
		o.info.Geometry.H = o.logicalH
		o.updateLogicalScaleLocked()
		return
	}
	if o.info.ResolutionW == 0 || o.info.ResolutionH == 0 {
		return
	}
	scale := maxInt(1, o.info.Scale)
	w, h := o.info.ResolutionW, o.info.ResolutionH
	o.info.Geometry.W = ceilDiv(w, scale)
	o.info.Geometry.H = ceilDiv(h, scale)
}

func (o *waylandOutput) updateLogicalScaleLocked() {
	if !o.hasLogicalSize || o.logicalW <= 0 || o.logicalH <= 0 || o.info.ResolutionW <= 0 || o.info.ResolutionH <= 0 {
		return
	}
	physicalW, physicalH := o.info.ResolutionW, o.info.ResolutionH
	if physicalW*o.logicalH != physicalH*o.logicalW {
		return
	}
	if physicalW%o.logicalW != 0 || physicalH%o.logicalH != 0 {
		numerator, denominator := reducedRatio(physicalW, o.logicalW)
		if numerator <= 0 || denominator <= 0 {
			return
		}
		o.info.Scale = 0
		o.info.ScaleNumerator = numerator
		o.info.ScaleDenominator = denominator
		return
	}
	if physicalW/o.logicalW != physicalH/o.logicalH {
		return
	}
	scale := physicalW / o.logicalW
	if scale <= 0 {
		return
	}
	o.info.Scale = scale
	o.info.ScaleNumerator = scale
	o.info.ScaleDenominator = 1
}

func ceilDiv(value, divisor int) int {
	if value <= 0 || divisor <= 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}

func reducedRatio(numerator, denominator int) (int, int) {
	if numerator <= 0 || denominator <= 0 {
		return 0, 0
	}
	divisor := gcd(numerator, denominator)
	return numerator / divisor, denominator / divisor
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
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
	if off < 0 || off > len(data)-4 {
		return "", off, false
	}
	n := int(wl.Uint32(data[off : off+4]))
	off += 4
	remaining := len(data) - off
	if n <= 0 || n > remaining {
		return "", off, false
	}
	end := off + n - 1
	if data[end] != 0 {
		return "", off, false
	}
	raw := string(data[off:end])
	extra := (4 - n%4) % 4
	if extra > remaining-n {
		return "", off, false
	}
	padded := n + extra
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
	if l.closed.Load() {
		return nil, fmt.Errorf("output/wayland: lister is closed: %w", net.ErrClosed)
	}
	if l.stopCtx != nil {
		operationCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(l.stopCtx, cancel) //nolint:contextcheck // derived context is canceled by lister lifecycle.
		defer func() {
			stop()
			cancel()
		}()
		ctx = operationCtx
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("output/wayland: list canceled: %w", err)
		}
	}
	if err := l.session.SyncContext(ctx); err != nil {
		return nil, err
	}
	if err := l.reconcileTopology(ctx); err != nil {
		return nil, err
	}
	if l.closed.Load() {
		return nil, fmt.Errorf("output/wayland: lister is closed: %w", net.ErrClosed)
	}
	return l.snapshotOutputs(), nil
}

func (l *WaylandLister) reconcileTopology(ctx context.Context) error {
	l.topologyMu.Lock()
	defer l.topologyMu.Unlock()
	if l.closed.Load() {
		return fmt.Errorf("output/wayland: lister is closed: %w", net.ErrClosed)
	}
	if l.session.Ctx == nil || l.session.Registry == nil || l.session.Display == nil {
		// Hand-built listers used by unit tests may only provide the output
		// collection. There is no live registry to reconcile in that case.
		return nil
	}
	desired, added, removed := l.topologyChanges(outputGlobals(l.session.GlobalsSnapshot()))
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}
	return wl.WithOperationContext(ctx, l.session.Ctx, func() error {
		errs := l.bindAddedOutputs(ctx, added, desired)
		errs = append(errs, l.releaseRemovedOutputs(ctx, removed)...)
		if len(added) > 0 {
			if err := l.session.Display.RoundTripContext(ctx); err != nil {
				errs = append(errs, fmt.Errorf("output/wayland: synchronize new output: %w", err))
			}
		}
		return errors.Join(errs...)
	})
}

func (l *WaylandLister) topologyChanges(globals []wl.GlobalEvent) (map[uint32]wl.GlobalEvent, []*waylandOutput, []*waylandOutput) {
	desired := make(map[uint32]wl.GlobalEvent, len(globals))
	for _, ev := range globals {
		desired[ev.Name] = ev
	}

	l.outputsMu.Lock()
	defer l.outputsMu.Unlock()
	current := make(map[uint32]struct{}, len(l.outputs))
	for _, out := range l.outputs {
		if out != nil {
			current[out.globalID] = struct{}{}
		}
	}
	added := make([]*waylandOutput, 0, len(globals))
	for _, ev := range globals {
		if _, ok := current[ev.Name]; !ok {
			added = append(added, newWaylandOutput(ev))
		}
	}
	removed := make([]*waylandOutput, 0)
	remaining := l.outputs[:0]
	for _, out := range l.outputs {
		if out == nil {
			continue
		}
		if _, ok := desired[out.globalID]; ok {
			remaining = append(remaining, out)
		} else {
			removed = append(removed, out)
		}
	}
	l.outputs = remaining
	return desired, added, removed
}

func (l *WaylandLister) bindAddedOutputs(ctx context.Context, added []*waylandOutput, desired map[uint32]wl.GlobalEvent) []error {
	var errs []error
	for _, out := range added {
		if err := l.bindOutput(ctx, out, desired[out.globalID]); err != nil {
			errs = append(errs, err)
			continue
		}
		if l.xdgManager != nil {
			if err := l.bindXDGOutput(ctx, out); err != nil {
				errs = append(errs, err)
			}
		}
		l.outputsMu.Lock()
		l.outputs = append(l.outputs, out)
		l.outputsMu.Unlock()
	}
	return errs
}

func (l *WaylandLister) releaseRemovedOutputs(ctx context.Context, removed []*waylandOutput) []error {
	var errs []error
	for _, out := range removed {
		if err := l.releaseOutput(ctx, out); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	return errs
}

func (l *WaylandLister) releaseOutput(ctx context.Context, out *waylandOutput) error {
	if out == nil || l.session == nil || l.session.Ctx == nil {
		return nil
	}
	var err error
	if out.xdgProxy != nil {
		if releaseErr := sendWaylandDestructor(ctx, l.session.Ctx, out.xdgProxy); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
		wl.Unregister(l.session.Ctx, out.xdgProxy)
	}
	if out.proxy != nil {
		if out.version >= 3 {
			if releaseErr := sendWaylandDestructor(ctx, l.session.Ctx, out.proxy); releaseErr != nil {
				err = errors.Join(err, releaseErr)
			}
		}
		wl.Unregister(l.session.Ctx, out.proxy)
	}
	return err
}

func sendWaylandDestructor(ctx context.Context, connection wl.Ctx, proxy *wl.RawProxy) error {
	if proxy == nil {
		return nil
	}
	buf := make([]byte, 8)
	wl.PutUint32(buf[0:4], proxy.ID())
	wl.PutUint32(buf[4:8], 8<<16) // destructor request opcode 0
	return connection.WriteMsgContext(ctx, buf, nil)
}

type outputSnapshot struct {
	globalID uint32
	info     Info
}

func (l *WaylandLister) snapshotOutputs() []Info {
	l.outputsMu.RLock()
	outputs := slices.Clone(l.outputs)
	l.outputsMu.RUnlock()
	snapshots := make([]outputSnapshot, 0, len(outputs))
	for _, output := range outputs {
		if output != nil {
			output.mu.RLock()
		}
	}
	defer func() {
		for i := len(outputs) - 1; i >= 0; i-- {
			if outputs[i] != nil {
				outputs[i].mu.RUnlock()
			}
		}
	}()

	for _, output := range outputs {
		if output == nil {
			continue
		}
		snapshots = append(snapshots, outputSnapshot{
			globalID: output.globalID,
			info:     output.info,
		})
	}
	slices.SortFunc(snapshots, func(a, b outputSnapshot) int {
		return cmp.Compare(a.globalID, b.globalID)
	})
	out := make([]Info, len(snapshots))
	for i, snapshot := range snapshots {
		out[i] = snapshot.info
	}
	return out
}

// Close releases the Wayland connection.
func (l *WaylandLister) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.closed.Store(true)
		if l.stopCancel != nil {
			l.stopCancel()
		}

		if l.session == nil || l.session.Ctx == nil {
			return
		}

		// Serialize collection changes with List, then release every protocol
		// object owned by this lister before releasing its session reference.
		l.topologyMu.Lock()
		l.outputsMu.Lock()
		outputs := slices.Clone(l.outputs)
		l.outputs = nil
		manager := l.xdgManager
		l.xdgManager = nil
		l.outputsMu.Unlock()
		l.topologyMu.Unlock()

		l.closeErr = wl.WithOperationContext(context.Background(), l.session.Ctx, func() error {
			var errs []error
			for _, out := range outputs {
				if err := l.releaseOutput(context.Background(), out); err != nil && !errors.Is(err, net.ErrClosed) {
					errs = append(errs, err)
				}
			}
			if manager != nil {
				if err := sendWaylandDestructor(context.Background(), l.session.Ctx, manager); err != nil && !errors.Is(err, net.ErrClosed) {
					errs = append(errs, err)
				}
				wl.Unregister(l.session.Ctx, manager)
			}
			return errors.Join(errs...)
		})
		l.closeErr = errors.Join(l.closeErr, l.session.Close())
	})
	return l.closeErr
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
