package output

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/internal/wl"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func countJoinedError(err, target error) int {
	if err == nil {
		return 0
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		count := 0
		for _, child := range joined.Unwrap() {
			count += countJoinedError(child, target)
		}
		return count
	}
	if errors.Is(err, target) {
		return 1
	}
	return countJoinedError(errors.Unwrap(err), target)
}

// A compositor disconnect during lister shutdown must still release every
// locally owned protocol proxy and report each failed destructor write.
func TestWaylandCloseReportsAllDestructorTransportErrorsAndUnregisters(t *testing.T) {
	socket := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan *net.UnixConn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	connection, err := wl.Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	var server *net.UnixConn
	select {
	case server = <-accepted:
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("Wayland connection was not accepted")
	}
	defer server.Close()

	outputProxy := &wl.RawProxy{}
	connection.Register(outputProxy)
	xdgProxy := &wl.RawProxy{}
	connection.Register(xdgProxy)
	manager := &wl.RawProxy{}
	connection.Register(manager)
	out := newWaylandOutput(wl.GlobalEvent{Name: 7, Interface: "wl_output", Version: 4})
	out.proxy = outputProxy
	out.xdgProxy = xdgProxy
	lister := &WaylandLister{
		session:    &wl.Session{Ctx: connection},
		outputs:    []*waylandOutput{out},
		xdgManager: manager,
	}

	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	peerClosed := make(chan error, 1)
	go func() { peerClosed <- connection.DispatchContext(context.Background()) }()
	select {
	case err := <-peerClosed:
		if err == nil {
			t.Fatal("closed compositor unexpectedly dispatched a message")
		}
	case <-time.After(time.Second):
		t.Fatal("Wayland client did not observe compositor disconnect")
	}

	closeErr := lister.Close()
	if got := countJoinedError(closeErr, syscall.EPIPE); got != 3 {
		t.Fatalf("lister close reported %d broken-pipe destructor errors, want output, XDG output, and manager: %v", got, closeErr)
	}
	for _, proxy := range []*wl.RawProxy{outputProxy, xdgProxy, manager} {
		id := proxy.ID()
		replacement := &wl.RawProxy{}
		connection.SetProxy(id, replacement)
		if replacement.ID() != id {
			t.Fatalf("failed shutdown retained protocol proxy %d", id)
		}
	}
}

// A canceled discovery request cannot touch a Wayland socket or disclose a
// partial output snapshot. Its cancellation takes priority over malformed
// runtime settings, including a missing WAYLAND_DISPLAY.
func TestWaylandOutputDiscoveryRejectsCanceledRequestsBeforeDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, socket := range []string{"", filepath.Join(t.TempDir(), "missing.sock")} {
		l, err := NewWaylandListerContext(ctx, socket)
		if l != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled discovery of %q: lister=%v err=%v", socket, l, err)
		}
	}
}

// Without a compositor connection, discovery must report absence, not return
// an initialized empty lister that callers could mistake for no monitors.
func TestWaylandOutputDiscoveryFailsClosedWithoutCompositor(t *testing.T) {
	for _, tc := range []struct {
		name, socket string
		contains     string
	}{
		{"missing WAYLAND_DISPLAY", "", "WAYLAND_DISPLAY"},
		{"unreachable socket", filepath.Join(t.TempDir(), "not-running.sock"), "connect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := NewWaylandListerContext(context.Background(), tc.socket)
			if l != nil || err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("unavailable output accepted: lister=%v err=%v", l, err)
			}
			if errors.Is(err, net.ErrClosed) {
				t.Fatalf("no compositor was reported as an existing closed lister: %v", err)
			}
		})
	}
}

func TestWaylandOutputGlobalsDoNotDuplicateOneRegistryObject(t *testing.T) {
	globals := []wl.GlobalEvent{
		{Name: 4, Interface: "wl_output", Version: 4},
		{Name: 9, Interface: "wl_seat", Version: 7},
		{Name: 4, Interface: "wl_output", Version: 3},
		{Name: 12, Interface: "wl_output", Version: 4},
		{Name: 12, Interface: "wl_output", Version: 4},
	}
	got := outputGlobals(globals)
	if len(got) != 2 || got[0].Name != 4 || got[1].Name != 12 {
		t.Fatalf("a repeated advertisement bound more than one proxy per global: %+v", got)
	}
}

func TestWaylandTopologyChangesDoNotReaddDuplicateGlobalIDs(t *testing.T) {
	l := &WaylandLister{}
	globals := []wl.GlobalEvent{{Name: 4, Interface: "wl_output", Version: 4}, {Name: 4, Interface: "wl_output", Version: 4}}
	desired, added, removed := l.topologyChanges(globals)
	if len(desired) != 1 || len(added) != 1 || len(removed) != 0 {
		t.Fatalf("duplicate global causes duplicate physical output: desired=%d added=%d removed=%d", len(desired), len(added), len(removed))
	}
}

// Output names are user-facing stable identifiers; an empty compositor name
// must not erase the unique registry-based fallback or a previous valid name.
func TestWaylandEmptyOutputNamesPreserveStableIdentity(t *testing.T) {
	out := newWaylandOutput(wl.GlobalEvent{Name: 23, Interface: "wl_output", Version: 4})
	fallback := out.info.Name
	if fallback == "" {
		t.Fatal("missing generated output identity")
	}
	protocol := &wl.RawProxy{}
	out.updateProxy(protocol)
	protocol.OnEvent(4, -1, wlStringData(""))
	if out.info.Name != fallback {
		t.Fatalf("empty wl_output name overwrote fallback: %q", out.info.Name)
	}
	protocol.OnEvent(4, -1, wlStringData("DP-1"))
	if out.info.Name != "DP-1" {
		t.Fatalf("valid output name lost: %q", out.info.Name)
	}
	protocol.OnEvent(4, -1, wlStringData(""))
	if out.info.Name != "DP-1" {
		t.Fatalf("empty wl_output name erased valid name: %q", out.info.Name)
	}
	xdg := &wl.RawProxy{}
	out.updateXDGProxy(xdg)
	xdg.OnEvent(3, -1, wlStringData(""))
	if out.info.Name != "DP-1" {
		t.Fatalf("empty xdg_output name erased valid name: %q", out.info.Name)
	}
	xdg.OnEvent(3, -1, wlStringData("HDMI-A-1"))
	if out.info.Name != "HDMI-A-1" {
		t.Fatalf("valid xdg name was not applied: %q", out.info.Name)
	}
}

// A canceled registry bind must not add an output, invoke the optional XDG
// binder, or retain the newly registered proxy.
func TestWaylandBindAddedOutputsRejectsCanceledRegistryBindWithoutPublishing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	connection := &wl.Context{}
	registry := &wl.Registry{}
	connection.Register(registry)
	lister := &WaylandLister{session: &wl.Session{Ctx: connection, Registry: registry}}
	event := wl.GlobalEvent{Name: 55, Interface: "wl_output", Version: 4}
	output := newWaylandOutput(event)
	xdgCalls := 0
	errs := lister.bindAddedOutputsWith(ctx, []*waylandOutput{output}, map[uint32]wl.GlobalEvent{55: event}, func(context.Context, *waylandOutput) error {
		xdgCalls++
		return nil
	})
	if len(errs) != 1 || !errors.Is(errs[0], context.Canceled) {
		t.Fatalf("canceled registry bind errors = %v, want context.Canceled", errs)
	}
	if len(lister.outputs) != 0 || output.proxy != nil || xdgCalls != 0 {
		t.Fatalf("failed registry bind published output/proxy or invoked XDG: outputs=%d proxy=%v xdgCalls=%d", len(lister.outputs), output.proxy, xdgCalls)
	}
}

func newWaylandOutputCleanupFixture(t *testing.T) (*net.UnixConn, *WaylandLister) {
	t.Helper()
	socket := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan *net.UnixConn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	connection, err := wl.Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	var server *net.UnixConn
	select {
	case server = <-accepted:
		t.Cleanup(func() { _ = server.Close() })
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("Wayland connection was not accepted")
	}

	registry := &wl.Registry{}
	connection.Register(registry)
	lister := &WaylandLister{
		session:    &wl.Session{Ctx: connection, Registry: registry},
		xdgManager: &wl.RawProxy{},
	}
	return server, lister
}

func TestWaylandFailedXDGEnrichmentCleansUpAfterCancellation(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		disconnectCompositor bool
	}{
		{name: "live compositor receives release"},
		{name: "disconnected compositor reports cleanup failure", disconnectCompositor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, lister := newWaylandOutputCleanupFixture(t)
			event := wl.GlobalEvent{Name: 17, Interface: "wl_output", Version: 4}
			output := newWaylandOutput(event)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bindErr := errors.New("synthetic XDG output enrichment failure")
			errs := lister.bindAddedOutputsWith(ctx, []*waylandOutput{output}, map[uint32]wl.GlobalEvent{event.Name: event}, func(context.Context, *waylandOutput) error {
				if tc.disconnectCompositor {
					if err := server.Close(); err != nil {
						t.Errorf("disconnect synthetic compositor: %v", err)
					}
				}
				cancel()
				return bindErr
			})
			if len(lister.outputs) != 0 {
				t.Fatalf("failed output enrichment published %d outputs", len(lister.outputs))
			}
			joined := errors.Join(errs...)
			if !errors.Is(joined, bindErr) {
				t.Fatalf("failed XDG enrichment errors = %v, want %v", errs, bindErr)
			}
			if tc.disconnectCompositor {
				if !errors.Is(joined, syscall.EPIPE) {
					t.Fatalf("cleanup errors = %v, want compositor write failure", errs)
				}
				return
			}
			if len(errs) != 1 {
				t.Fatalf("cleanup errors = %v, want only the XDG enrichment failure", errs)
			}

			if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			var header [8]byte
			if _, err := io.ReadFull(server, header[:]); err != nil {
				t.Fatalf("read output bind request: %v", err)
			}
			requestSize := int(wl.Uint32(header[4:8]) >> 16)
			if requestSize < len(header) {
				t.Fatalf("invalid output bind request size %d", requestSize)
			}
			if _, err := io.CopyN(io.Discard, server, int64(requestSize-len(header))); err != nil {
				t.Fatalf("read output bind request body: %v", err)
			}

			if _, err := io.ReadFull(server, header[:]); err != nil {
				t.Fatalf("read wl_output release after canceled XDG enrichment: %v", err)
			}
			if gotSender, gotOpcode := wl.Uint32(header[:4]), wl.Uint32(header[4:8])&0xffff; gotSender != output.proxy.ID() || gotOpcode != 0 {
				t.Fatalf("cleanup request sender/opcode = %d/%d, want output proxy %d release", gotSender, gotOpcode, output.proxy.ID())
			}
		})
	}
}

// A compositor's initial output geometry is not a physical mode. Until the
// latter arrives, an output must not invent a capturable logical resolution.
func TestWaylandOutputKeepsUnknownDimensionsUntilValidPhysicalMode(t *testing.T) {
	out := newWaylandOutput(wl.GlobalEvent{Name: 13, Interface: "wl_output", Version: 4})
	out.baseX, out.baseY = -20, 35
	out.recomputeGeometryLocked()
	if out.info.Geometry.X != -20 || out.info.Geometry.Y != 35 || out.info.Geometry.W != 0 || out.info.Geometry.H != 0 {
		t.Fatalf("unmeasured output incorrectly advertised geometry: %+v", out.info.Geometry)
	}
	out.hasLogicalSize = true
	out.logicalW, out.logicalH = 1280, 720
	out.updateLogicalScaleLocked()
	if out.info.Scale != 1 || out.info.ScaleNumerator != 1 || out.info.ScaleDenominator != 1 {
		t.Fatalf("unknown physical mode supplied invented scale: %+v", out.info)
	}
	out.info.ResolutionW, out.info.ResolutionH = 1920, 1080
	out.recomputeGeometryLocked()
	if out.info.Geometry.W != 1280 || out.info.Geometry.H != 720 || out.info.Scale != 0 || out.info.ScaleNumerator != 3 || out.info.ScaleDenominator != 2 {
		t.Fatalf("valid physical mode did not establish fractional scale: %+v", out.info)
	}
}

// Once shutdown begins, topology reconciliation cannot accept freshly
// discovered outputs or overwrite the last known display collection.
func TestWaylandClosedListerRejectsTopologyReconciliationWithoutMutation(t *testing.T) {
	existing := newWaylandOutput(wl.GlobalEvent{Name: 8, Interface: "wl_output", Version: 4})
	existing.info.Name = "prior-private-display"
	l := &WaylandLister{outputs: []*waylandOutput{existing}}
	l.closed.Store(true)
	if err := l.reconcileTopology(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("retired lister accepted topology reconciliation: %v", err)
	}
	outputs := l.snapshotOutputs()
	if len(outputs) != 1 || outputs[0].Name != "prior-private-display" {
		t.Fatalf("closed lister lost its preexisting metadata: %+v", outputs)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

// No Wayland session is not an empty display topology: it is unavailable.
func TestWaylandUnavailableListerRejectsObservationAndClosesSafely(t *testing.T) {
	var missing *WaylandLister
	outputs, err := missing.List(context.Background())
	if err == nil || len(outputs) != 0 {
		t.Fatalf("nil lister reported outputs: %+v err=%v", outputs, err)
	}
	if err := missing.Close(); err != nil {
		t.Fatalf("closing absent lister: %v", err)
	}
	l := &WaylandLister{}
	outputs, err = l.List(context.Background())
	if err == nil || len(outputs) != 0 {
		t.Fatalf("uninitialized lister reported outputs: %+v err=%v", outputs, err)
	}
	if err := sendWaylandDestructor(context.Background(), nil, nil); err != nil {
		t.Fatalf("nil output proxy required live connection: %v", err)
	}
}

func TestWaylandListerRejectsDisconnectDuringInitialOutputSync(t *testing.T) {
	socket := shortPrivateSocketPath(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		var registry uint32
		var syncs, outputBinds int
		for {
			message, readErr := readWaylandMessage(conn)
			if readErr != nil {
				serverDone <- readErr
				return
			}
			sender := wl.Uint32(message[:4])
			opcode := wl.Uint32(message[4:8]) & 0xffff
			body := message[8:]
			switch {
			case sender == 1 && opcode == 1:
				registry = wl.Uint32(body)
			case sender == registry && opcode == 0:
				global, _, bindErr := parseWaylandBind(body)
				if bindErr != nil || global != 4 {
					serverDone <- errors.New("unexpected output bind during initialization")
					return
				}
				outputBinds++
			case sender == 1 && opcode == 0:
				syncs++
				if len(body) < 4 {
					serverDone <- errors.New("display sync request omitted callback ID")
					return
				}
				callback := wl.Uint32(body)
				if syncs == 1 {
					global := appendUint32(nil, 4)
					global = appendWlStringData(global, "wl_output")
					global = appendUint32(global, 4)
					if writeErr := writeWaylandMessage(conn, registry, 0, global); writeErr != nil {
						serverDone <- writeErr
						return
					}
					if writeErr := writeWaylandMessage(conn, callback, 0, make([]byte, 4)); writeErr != nil {
						serverDone <- writeErr
						return
					}
					continue
				}
				if outputBinds != 1 {
					serverDone <- errors.New("output synchronization started before the output bind")
					return
				}
				serverDone <- nil
				return
			default:
				serverDone <- errors.New("unexpected Wayland request during initialization")
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lister, err := NewWaylandListerContext(ctx, socket)
	if lister != nil {
		_ = lister.Close()
		t.Fatal("lister published after the compositor disconnected during initial sync")
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("lister initialization error = %v, want compositor disconnect", err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatalf("synthetic compositor did not observe initial output sync: %v", ctx.Err())
	}
}

// A malformed compositor announcement is not an authorized output. ID zero
// is the Wayland display object, and an interface version of zero cannot be
// bound; neither may appear in a screen enumeration or topology delta.
func TestWaylandOutputRegistryIgnoresInvalidGlobalIdentityAndVersion(t *testing.T) {
	advertised := []wl.GlobalEvent{
		{Name: 0, Interface: "wl_output", Version: 4},
		{Name: 12, Interface: "wl_output", Version: 0},
		{Name: 13, Interface: "wl_output", Version: 4},
		{Name: 13, Interface: "wl_output", Version: 0},
		{Name: 14, Interface: "wl_seat", Version: 4},
	}
	filtered := outputGlobals(advertised)
	if len(filtered) != 1 || filtered[0].Name != 13 || filtered[0].Version != 4 {
		t.Fatalf("invalid registry outputs accepted: %+v", filtered)
	}
	l := &WaylandLister{}
	desired, added, removed := l.topologyChanges(filtered)
	if len(desired) != 1 || len(added) != 1 || len(removed) != 0 || added[0].globalID != 13 {
		t.Fatalf("invalid output topology accepted: desired=%v added=%v removed=%v", desired, added, removed)
	}
}

// Cancel at the sync/reconciliation boundary, after the initial admission
// check but before the output snapshot is published to the caller.
type cancelOutputContextAfterAdmission struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelOutputContextAfterAdmission) Err() error {
	c.checks++
	err := c.Context.Err()
	if c.checks == 1 {
		c.cancel()
	}
	return err
}

func TestWaylandListerRejectsCancellationAfterSessionSynchronization(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelOutputContextAfterAdmission{Context: base, cancel: cancel}
	l := &WaylandLister{
		session: &wl.Session{},
		outputs: []*waylandOutput{{globalID: 7, info: Info{Name: "private-display", Available: true}}},
	}
	outputs, err := l.List(ctx)
	if !errors.Is(err, context.Canceled) || len(outputs) != 0 {
		t.Fatalf("canceled output enumeration exposed outputs=%v err=%v", outputs, err)
	}
	if ctx.checks < 1 {
		t.Fatalf("cancellation boundary not reached: %d checks", ctx.checks)
	}
}

// wl_output.mode is controlled by the compositor. A malformed live mode
// update must not replace the last verified geometry with impossible bounds.
func TestWaylandOutputIgnoresInvalidCurrentModeDimensions(t *testing.T) {
	out := newWaylandOutput(wl.GlobalEvent{Name: 11, Interface: "wl_output", Version: 4})
	proxy := &wl.RawProxy{}
	out.updateProxy(proxy)
	mode := make([]byte, 16)
	wl.PutUint32(mode[:4], wl.OutputModeCurrent)
	wl.PutUint32(mode[4:8], 1920)
	wl.PutUint32(mode[8:12], 1080)
	wl.PutUint32(mode[12:16], 60000)
	proxy.OnEvent(1, 0, mode)
	if out.info.ResolutionW != 1920 || out.info.ResolutionH != 1080 || out.info.Geometry.W != 1920 {
		t.Fatalf("valid physical mode not initialized %+v", out.info)
	}
	for _, tc := range []struct {
		name          string
		width, height uint32
	}{
		{"negative width", ^uint32(0), 1080},
		{"negative height", 1920, ^uint32(0)},
		{"zero width", 0, 1080},
		{"zero height", 1920, 0},
		{"both invalid", ^uint32(0), ^uint32(0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wl.PutUint32(mode[4:8], tc.width)
			wl.PutUint32(mode[8:12], tc.height)
			proxy.OnEvent(1, 0, mode)
			if out.info.ResolutionW != 1920 || out.info.ResolutionH != 1080 || out.info.Geometry.W != 1920 || out.info.Geometry.H != 1080 {
				t.Fatalf("malformed current mode replaced valid resolution: %+v", out.info)
			}
		})
	}
}

func TestWaylandListReportsFailedSyncAfterBindingNewOutput(t *testing.T) {
	socket := shortPrivateSocketPath(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		var registryID uint32
		var roundTrips, outputBinds int
		for {
			message, readErr := readWaylandMessage(conn)
			if errors.Is(readErr, io.EOF) {
				serverDone <- nil
				return
			}
			if readErr != nil {
				serverDone <- fmt.Errorf("read Wayland request: %w", readErr)
				return
			}
			sender := wl.Uint32(message[:4])
			opcode := wl.Uint32(message[4:8]) & 0xffff
			body := message[8:]
			switch {
			case sender == 1 && opcode == 1:
				registryID = wl.Uint32(body)
			case sender == registryID && opcode == 0:
				globalName, _, bindErr := parseWaylandBind(body)
				if bindErr != nil {
					serverDone <- bindErr
					return
				}
				if globalName != 4 && globalName != 5 {
					serverDone <- fmt.Errorf("unexpected output global bind %d", globalName)
					return
				}
				outputBinds++
			case sender == 1 && opcode == 0:
				roundTrips++
				if len(body) < 4 {
					serverDone <- errors.New("display sync request omitted callback ID")
					return
				}
				callback := wl.Uint32(body[:4])
				switch roundTrips {
				case 1, 3:
					globalID := uint32(4)
					if roundTrips == 3 {
						globalID = 5
					}
					global := appendUint32(nil, globalID)
					global = appendWlStringData(global, "wl_output")
					global = appendUint32(global, 4)
					if writeErr := writeWaylandMessage(conn, registryID, 0, global); writeErr != nil {
						serverDone <- fmt.Errorf("announce output global: %w", writeErr)
						return
					}
				case 4:
					if outputBinds != 2 {
						serverDone <- fmt.Errorf("output binds = %d, want initial and new output", outputBinds)
						return
					}
					if closeErr := conn.Close(); closeErr != nil {
						serverDone <- closeErr
						return
					}
					serverDone <- nil
					return
				}
				if writeErr := writeWaylandMessage(conn, callback, 0, make([]byte, 4)); writeErr != nil {
					serverDone <- fmt.Errorf("complete display sync: %w", writeErr)
					return
				}
			default:
				serverDone <- fmt.Errorf("unexpected Wayland request sender=%d opcode=%d", sender, opcode)
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lister, err := NewWaylandListerContext(ctx, socket)
	if err != nil {
		t.Fatalf("initialize initial output: %v", err)
	}
	defer func() { _ = lister.Close() }()

	outputs, err := lister.List(ctx)
	if err == nil || !strings.Contains(err.Error(), "synchronize new output") {
		t.Fatalf("List returned outputs=%+v err=%v, want synchronized-add failure", outputs, err)
	}
	if outputs != nil {
		t.Fatalf("failed topology synchronization published partial outputs: %+v", outputs)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatalf("synthetic compositor did not reach the failing sync: %v", ctx.Err())
	}
}

// An otherwise responsive compositor may not advertise any usable output.
// Discovery must not mistake a successful registry sync for an initialized
// display lister or retain its socket after refusing the capability.
type unusableWaylandGlobal struct {
	id      uint32
	iface   string
	version uint32
}

func TestWaylandListerRejectsResponsiveCompositorWithoutUsableOutputs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		globals []unusableWaylandGlobal
	}{
		{name: "empty registry"},
		{name: "unrelated seat only", globals: []unusableWaylandGlobal{{7, "wl_seat", 7}}},
		{name: "zero output version", globals: []unusableWaylandGlobal{{9, "wl_output", 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := shortPrivateSocketPath(t)
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() { serverDone <- serveUnusableWaylandCompositor(listener, tc.globals) }()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			lister, openErr := NewWaylandListerContext(ctx, socket)
			if lister != nil || openErr == nil || !strings.Contains(openErr.Error(), "no wl_output globals advertised") {
				if lister != nil {
					_ = lister.Close()
				}
				t.Fatalf("unusable compositor produced lister=%v err=%v", lister, openErr)
			}

			select {
			case serveErr := <-serverDone:
				if serveErr != nil {
					t.Fatalf("compositor did not observe clean client disconnect: %v", serveErr)
				}
			case <-time.After(time.Second):
				t.Fatal("failed discovery leaked compositor connection")
			}
		})
	}
}

func serveUnusableWaylandCompositor(listener net.Listener, globals []unusableWaylandGlobal) error {
	conn, acceptErr := listener.Accept()
	if acceptErr != nil {
		return acceptErr
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	var registry uint32
	for {
		msg, readErr := readWaylandMessage(conn)
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		sender, opcode := wl.Uint32(msg[:4]), wl.Uint32(msg[4:8])&0xffff
		body := msg[8:]
		switch {
		case sender == 1 && opcode == 1:
			registry = wl.Uint32(body)
		case sender == 1 && opcode == 0:
			for _, ev := range globals {
				payload := appendUint32(nil, ev.id)
				payload = appendWlStringData(payload, ev.iface)
				payload = appendUint32(payload, ev.version)
				if writeErr := writeWaylandMessage(conn, registry, 0, payload); writeErr != nil {
					return writeErr
				}
			}
			if writeErr := writeWaylandMessage(conn, wl.Uint32(body), 0, make([]byte, 4)); writeErr != nil {
				return writeErr
			}
		default:
			return errors.New("unexpected client request to compositor without output")
		}
	}
}

// XDG-output is optional, and outputs can disappear between discovery and
// binding. No protocol operation should be attempted for an absent manager,
// absent output, or an output whose wl_output proxy was already removed.
func TestWaylandOptionalXDGBindingSkipsAbsentTopology(t *testing.T) {
	output := &waylandOutput{info: Info{Name: "survivor", Backend: "wayland"}}
	manager := &wl.RawProxy{}
	for _, tc := range []struct {
		name   string
		lister *WaylandLister
		output *waylandOutput
	}{
		{name: "manager absent", lister: &WaylandLister{}, output: output},
		{name: "output absent", lister: &WaylandLister{xdgManager: manager}},
		{name: "output proxy absent", lister: &WaylandLister{xdgManager: manager}, output: output},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.lister.bindXDGOutput(context.Background(), tc.output); err != nil {
				t.Fatalf("missing optional XDG topology should be skipped: %v", err)
			}
			if tc.output != nil && tc.output.xdgProxy != nil {
				t.Fatal("missing optional XDG topology registered a proxy")
			}
		})
	}
}

// wl_output.geometry's physical_width and physical_height are signed int32
// millimeters. A negative value is an unavailable measurement, not an
// advertised negative physical panel size.
func TestWaylandGeometryTreatsNegativePhysicalMillimetersAsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int32
		wantW, wantH  int
	}{
		{"unknown width", -1, 335, 0, 335},
		{"unknown height", 590, -1, 590, 0},
		{"both invalid", -2147483648, -9, 0, 0},
		{"valid dimensions", 600, 340, 600, 340},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := newWaylandOutput(wl.GlobalEvent{Name: 12, Interface: "wl_output", Version: 4})
			proxy := &wl.RawProxy{}
			out.updateProxy(proxy)
			wire := make([]byte, 20)
			wl.PutUint32(wire[8:12], uint32(tc.width))
			wl.PutUint32(wire[12:16], uint32(tc.height))
			proxy.OnEvent(0, 0, wire)
			if out.info.PhysicalW != tc.wantW || out.info.PhysicalH != tc.wantH {
				t.Fatalf("compositor physical millimeters (%d,%d) became (%d,%d); want (%d,%d)", tc.width, tc.height, out.info.PhysicalW, out.info.PhysicalH, tc.wantW, tc.wantH)
			}
		})
	}
}

// Shutdown must not silently discard destructor failures from a compositor
// transport. All retired outputs must still lose their proxy registrations,
// even when the transport refuses to send their destruction requests.
func TestWaylandRetiredOutputsPropagateTransportCancellationAndReleaseProxies(t *testing.T) {
	connection := &wl.Context{}
	makeOutput := func(global uint32, version uint32, withXDG bool) *waylandOutput {
		out := newWaylandOutput(wl.GlobalEvent{Name: global, Interface: "wl_output", Version: version})
		proxy := &wl.RawProxy{}
		connection.Register(proxy)
		out.proxy = proxy
		if withXDG {
			xdg := &wl.RawProxy{}
			connection.Register(xdg)
			out.xdgProxy = xdg
		}
		return out
	}
	first := makeOutput(11, 4, true)
	second := makeOutput(12, 2, false)
	lister := &WaylandLister{session: &wl.Session{Ctx: connection}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	errs := lister.releaseRemovedOutputs(ctx, []*waylandOutput{first, second})
	if len(errs) != 1 || !errors.Is(errs[0], context.Canceled) {
		t.Fatalf("retired Wayland destructor failures = %v, want one cancellation error", errs)
	}
	// Each retired output must relinquish its registration even on a failed
	// protocol write, so a late compositor event cannot mutate retired state.
	for _, proxy := range []*wl.RawProxy{first.proxy, first.xdgProxy, second.proxy} {
		if proxy.ID() == 0 {
			t.Fatal("test proxy had no registered identity")
		}
		replacement := &wl.RawProxy{}
		connection.SetProxy(proxy.ID(), replacement)
		if replacement.ID() != proxy.ID() {
			t.Fatalf("retired proxy %d still holds its registry identity", proxy.ID())
		}
	}
}

func TestWaylandOutputXDGLogicalGeometryEventMatrix(t *testing.T) {
	for _, tc := range []struct {
		name                                  string
		physicalW, physicalH                  uint32
		logicalW, logicalH                    int32
		wantN, wantD, wantScale, wantW, wantH int
	}{
		{"fractional five quarters", 2560, 1440, 2048, 1152, 5, 4, 0, 2048, 1152},
		{"fractional three halves", 1920, 1080, 1280, 720, 3, 2, 0, 1280, 720},
		{"integer double", 3840, 2160, 1920, 1080, 2, 1, 2, 1920, 1080},
		{"integer one", 1920, 1080, 1920, 1080, 1, 1, 1, 1920, 1080},
		{"incompatible aspect ratio", 1920, 1080, 1280, 700, 1, 1, 1, 1280, 700},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := newWaylandOutput(wl.GlobalEvent{Name: 14, Interface: "wl_output", Version: 4})
			phys, xdg := &wl.RawProxy{}, &wl.RawProxy{}
			out.updateProxy(phys)
			out.updateXDGProxy(xdg)
			mode := make([]byte, 16)
			wl.PutUint32(mode[:4], wl.OutputModeCurrent)
			wl.PutUint32(mode[4:8], tc.physicalW)
			wl.PutUint32(mode[8:12], tc.physicalH)
			phys.OnEvent(1, 0, mode)
			pos := make([]byte, 8)
			wl.PutUint32(pos[:4], uint32(100))
			wl.PutUint32(pos[4:8], uint32(200))
			xdg.OnEvent(0, 0, pos)
			sz := make([]byte, 8)
			wl.PutUint32(sz[:4], uint32(tc.logicalW))
			wl.PutUint32(sz[4:8], uint32(tc.logicalH))
			xdg.OnEvent(1, 0, sz)
			i := out.info
			if i.Geometry.X != 100 || i.Geometry.Y != 200 || i.Geometry.W != tc.wantW || i.Geometry.H != tc.wantH || i.ScaleNumerator != tc.wantN || i.ScaleDenominator != tc.wantD || i.Scale != tc.wantScale {
				t.Fatalf("xdg scale mismatch for %dx%d / %dx%d: %+v", tc.physicalW, tc.physicalH, tc.logicalW, tc.logicalH, i)
			}
			before := i
			for _, invalid := range [][]byte{nil, {1, 2, 3}, make([]byte, 8)} {
				if len(invalid) == 8 {
					wl.PutUint32(invalid[:4], 0)
					wl.PutUint32(invalid[4:8], 50)
				}
				xdg.OnEvent(1, 0, invalid)
				if out.info.Geometry != before.Geometry || out.info.Scale != before.Scale {
					t.Fatal("invalid XDG size corrupted prior geometry")
				}
			}
			phys.OnEvent(3, 0, []byte{0, 0, 0})
			if out.info.Scale != before.Scale {
				t.Fatal("invalid wl_output scale corrupted prior scale")
			}
		})
	}
}
func TestWaylandOutputRejectsMalformedXDGPositionAndPreservesPhysicalFallback(t *testing.T) {
	out := newWaylandOutput(wl.GlobalEvent{Name: 18, Interface: "wl_output", Version: 4})
	p, x := &wl.RawProxy{}, &wl.RawProxy{}
	out.updateProxy(p)
	out.updateXDGProxy(x)
	geometry := make([]byte, 20)
	wl.PutUint32(geometry[:4], uint32(5))
	wl.PutUint32(geometry[4:8], uint32(10))
	p.OnEvent(0, 0, geometry)
	mode := make([]byte, 16)
	wl.PutUint32(mode[:4], wl.OutputModeCurrent)
	wl.PutUint32(mode[4:8], 1920)
	wl.PutUint32(mode[8:12], 1080)
	p.OnEvent(1, 0, mode)
	scale := make([]byte, 4)
	wl.PutUint32(scale, 2)
	p.OnEvent(3, 0, scale)
	if got := out.info.Geometry; got.X != 5 || got.Y != 10 || got.W != 960 || got.H != 540 {
		t.Fatalf("fallback geometry not scaled: %+v", got)
	}
	for _, bad := range [][]byte{nil, {0, 0, 0, 0}} {
		x.OnEvent(0, 0, bad)
		if out.info.Geometry.X != 5 || out.info.Geometry.Y != 10 {
			t.Fatal("invalid logical position replaced physical coordinates")
		}
	}
	pos := make([]byte, 8)
	wl.PutUint32(pos[:4], uint32(45))
	wl.PutUint32(pos[4:8], uint32(30))
	x.OnEvent(0, 0, pos)
	if out.info.Geometry.X != 45 || out.info.Geometry.Y != 30 {
		t.Fatalf("logical position not used %+v", out.info)
	}
	x.OnEvent(3, 0, wlStringData("DP-test"))
	x.OnEvent(4, 0, wlStringData("Synthetic Display"))
	if out.info.Name != "DP-test" || out.info.Description != "Synthetic Display" {
		t.Fatalf("xdg name/description not propagated %+v", out.info)
	}
}

// The logical and physical dimensions of one output must describe the same
// scale on both axes before replacing the previously known scale metadata.
func TestWaylandLogicalScaleRejectsInconsistentAxes(t *testing.T) {
	out := newWaylandOutput(wl.GlobalEvent{Name: 21, Interface: "wl_output", Version: 4})
	out.hasLogicalSize = true
	out.logicalW, out.logicalH = 1280, 720
	out.info.ResolutionW, out.info.ResolutionH = 1920, 1080
	out.updateLogicalScaleLocked()
	if out.info.ScaleNumerator != 3 || out.info.ScaleDenominator != 2 || out.info.Scale != 0 {
		t.Fatalf("expected fractional 3/2 initial scale, got %+v", out.info)
	}
	// A compositor may deliver a new width before the matching height.
	out.logicalW, out.logicalH = 1000, 720
	out.updateLogicalScaleLocked()
	if out.info.ScaleNumerator != 3 || out.info.ScaleDenominator != 2 || out.info.Scale != 0 {
		t.Fatalf("inconsistent axes overwrote last consistent scale: %+v", out.info)
	}
	out.logicalW, out.logicalH = 960, 540
	out.updateLogicalScaleLocked()
	if out.info.Scale != 2 || out.info.ScaleNumerator != 2 || out.info.ScaleDenominator != 1 {
		t.Fatalf("consistent integral scale was not adopted: %+v", out.info)
	}
}

func TestWaylandScaleArithmeticRejectsNonpositiveInputs(t *testing.T) {
	for _, tc := range []struct{ width, divisor int }{
		{-1, 2}, {0, 2}, {2, 0}, {2, -1},
	} {
		if got := ceilDiv(tc.width, tc.divisor); got != 0 {
			t.Fatalf("ceilDiv(%d,%d)=%d", tc.width, tc.divisor, got)
		}
		a, b := reducedRatio(tc.width, tc.divisor)
		if a != 0 || b != 0 {
			t.Fatalf("reducedRatio(%d,%d)=(%d,%d)", tc.width, tc.divisor, a, b)
		}
	}
	if got := ceilDiv(1921, 2); got != 961 {
		t.Fatalf("ceilDiv(1921,2)=%d", got)
	}
	if a, b := reducedRatio(3840, 2560); a != 3 || b != 2 {
		t.Fatalf("reducedRatio(3840,2560)=(%d,%d)", a, b)
	}
}

// wl_output.scale is a signed protocol int32. Negative wire values are not
// legitimate 4-billion-fold scales and must not corrupt visible geometry.
func TestWaylandSignedOutputScaleRejectsNegativeProtocolValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value uint32
	}{
		{"minus one", ^uint32(0)},
		{"minimum signed", uint32(1) << 31},
		{"minus two", ^uint32(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := newWaylandOutput(wl.GlobalEvent{Name: 7, Interface: "wl_output", Version: 4})
			proxy := &wl.RawProxy{}
			out.updateProxy(proxy)
			mode := make([]byte, 16)
			wl.PutUint32(mode[0:4], wl.OutputModeCurrent)
			wl.PutUint32(mode[4:8], 1920)
			wl.PutUint32(mode[8:12], 1080)
			proxy.OnEvent(1, 0, mode)
			scale := make([]byte, 4)
			wl.PutUint32(scale, 2)
			proxy.OnEvent(3, 0, scale)
			wl.PutUint32(scale, tc.value)
			proxy.OnEvent(3, 0, scale)
			got := out.info
			if got.Scale != 2 || got.ScaleNumerator != 2 || got.ScaleDenominator != 1 || got.Geometry.W != 960 || got.Geometry.H != 540 {
				t.Fatalf("negative signed scale %08x corrupted live output: %+v", tc.value, got)
			}
		})
	}
}

// An already-ended observation is not permitted to return a cached output
// snapshot, even when the lister still has perfectly valid old metadata.
func TestWaylandListerCanceledObservationWithholdsCachedOutputs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l := &WaylandLister{session: &wl.Session{}, outputs: []*waylandOutput{
		{globalID: 7, info: Info{Name: "previous-private-display", Backend: "wayland", Available: true}},
	}}
	outputs, err := l.List(ctx)
	if outputs != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller exposed output metadata: %+v %v", outputs, err)
	}
}

// The lister's own stop signal also invalidates snapshots, independently of
// the observing caller's context. This models a session loss while clients
// retain their reference to an otherwise initialized output lister.
func TestWaylandListerLifecycleStopWithholdsCachedOutputs(t *testing.T) {
	stopCtx, stop := context.WithCancel(context.Background())
	stop()
	l := &WaylandLister{session: &wl.Session{}, stopCtx: stopCtx, outputs: []*waylandOutput{
		{globalID: 7, info: Info{Name: "stale-output", Backend: "wayland", Available: true}},
	}}
	outputs, err := l.List(context.Background())
	if outputs != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("stopped output lifecycle returned %+v %v", outputs, err)
	}
}

type cancelOutputContextAfterErrCheck struct {
	context.Context
	cancel      context.CancelFunc
	cancelAfter int
	checks      int
}

func (c *cancelOutputContextAfterErrCheck) Err() error {
	c.checks++
	err := c.Context.Err()
	if c.checks == c.cancelAfter {
		c.cancel()
	}
	return err
}

// Cancellation after synchronization and just before returning must not leak
// cached output metadata from the observation.
func TestWaylandListerWithholdsSnapshotWhenCanceledDuringPublication(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cancelAfter int
	}{
		{name: "after synchronization", cancelAfter: 2},
		{name: "between reconciliation and snapshot", cancelAfter: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelOutputContextAfterErrCheck{
				Context:     base,
				cancel:      cancel,
				cancelAfter: tc.cancelAfter,
			}
			lister := &WaylandLister{
				session: &wl.Session{},
				outputs: []*waylandOutput{{globalID: 7, info: Info{
					Name: "display-must-not-be-published", Backend: "wayland", Available: true,
				}}},
			}

			outputs, err := lister.List(ctx)
			if !errors.Is(err, context.Canceled) || outputs != nil {
				t.Fatalf("canceled observation returned outputs=%+v err=%v", outputs, err)
			}
			if ctx.checks != tc.cancelAfter+1 {
				t.Fatalf("cancellation check count=%d, want %d", ctx.checks, tc.cancelAfter+1)
			}
		})
	}
}

func wlStringData(s string) []byte {
	n := uint32(len(s) + 1)
	padded := (n + 3) &^ 3
	out := make([]byte, 4+int(padded))
	wl.PutUint32(out[0:4], n)
	copy(out[4:], s)
	return out
}

func TestWaylandLister_DoesNotKeepOutputAfterXDGBindFailure(t *testing.T) {
	waylandCtx := &wl.Context{}
	registry := &wl.Registry{}
	waylandCtx.Register(registry)
	manager := &wl.RawProxy{}
	waylandCtx.Register(manager)
	lister := &WaylandLister{
		session:    &wl.Session{Ctx: waylandCtx, Registry: registry},
		xdgManager: manager,
	}
	added := []*waylandOutput{newWaylandOutput(wl.GlobalEvent{Name: 7, Interface: "wl_output", Version: 4})}
	desired := map[uint32]wl.GlobalEvent{7: {Name: 7, Interface: "wl_output", Version: 4}}

	errs := lister.bindAddedOutputsWith(context.Background(), added, desired, func(context.Context, *waylandOutput) error {
		return errors.New("simulated xdg bind failure")
	})
	if len(errs) == 0 {
		t.Fatal("bindAddedOutputs returned no error after simulated xdg bind failure")
	}
	if len(lister.outputs) != 0 {
		t.Fatalf("outputs after xdg bind failure = %d, want 0", len(lister.outputs))
	}
}

func TestWaylandListerListCancelsBlockedRoundTrip(t *testing.T) {
	sock := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()

	serverConn := make(chan net.Conn, 1)
	requestRead := make(chan struct{})
	serverErr := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		serverConn <- conn
		var request [12]byte
		_, readErr := io.ReadFull(conn, request[:])
		if readErr != nil {
			serverErr <- readErr
			return
		}
		close(requestRead)
	}()

	waylandCtx, err := wl.Connect(sock)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer waylandCtx.Close()
	conn := <-serverConn
	defer conn.Close()

	lister := &WaylandLister{
		session: &wl.Session{
			Sock:    sock,
			Ctx:     waylandCtx,
			Display: wl.NewDisplay(waylandCtx),
		},
		outputs: []*waylandOutput{{info: Info{Name: "test", Backend: "wayland"}}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listDone := make(chan error, 1)
	go func() {
		_, listErr := lister.List(ctx)
		listDone <- listErr
	}()

	select {
	case <-requestRead:
	case err := <-serverErr:
		t.Fatalf("server read: %v", err)
	case <-time.After(time.Second):
		t.Fatal("Wayland sync request was not sent")
	}
	cancel()

	select {
	case err := <-listDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("List error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("List did not return after context cancellation")
	}
}

func TestWaylandListerRejectsListAfterClose(t *testing.T) {
	lister := &WaylandLister{
		session: &wl.Session{},
		outputs: []*waylandOutput{{info: Info{Name: "test", Backend: "wayland"}}},
	}
	if err := lister.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := lister.List(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("List error = %v, want net.ErrClosed", err)
	}
}

func TestNewWaylandListerPreservesRepeatedOutputs(t *testing.T) {
	sock := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- serveWaylandOutputs(listener) }()

	lister, err := NewWaylandLister(sock)
	if err != nil {
		listener.Close()
		t.Fatalf("NewWaylandLister: %v", err)
	}
	defer func() {
		_ = lister.Close()
		_ = listener.Close()
	}()
	globals := lister.session.GlobalsSnapshot()
	if len(globals) != 2 || globals[0].Name != 4 || globals[1].Name != 12 {
		t.Fatalf("session globals = %+v, want wl_output names 4, 12", globals)
	}

	got, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d outputs, want 2: %+v", len(got), got)
	}
	if got[0].Name != "DP-1" || got[1].Name != "DP-2" {
		t.Fatalf("output order = %q, %q; want DP-1, DP-2", got[0].Name, got[1].Name)
	}
	if err := lister.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("fake compositor: %v", err)
	}
}

func TestWaylandListerTracksLiveOutputTopology(t *testing.T) {
	sock := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	addOutput := make(chan struct{})
	serverDone := make(chan error, 1)
	serverReady := make(chan struct{})
	go func() {
		serverDone <- serveDynamicWaylandOutputs(listener, addOutput, serverReady)
	}()

	lister, err := NewWaylandLister(sock)
	if err != nil {
		listener.Close()
		t.Fatalf("NewWaylandLister: %v", err)
	}

	<-serverReady
	close(addOutput)
	got, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("List after output add: %v", err)
	}
	if len(got) != 3 || got[2].Name != "DP-3" {
		t.Fatalf("outputs after add = %+v, want DP-1, DP-2, DP-3", got)
	}
	if !got[2].Available {
		t.Fatalf("new output = %+v, want available", got[2])
	}

	got, err = lister.List(context.Background())
	if err != nil {
		t.Fatalf("List after output removal: %v", err)
	}
	if len(got) != 2 || got[0].Name != "DP-1" || got[1].Name != "DP-2" {
		t.Fatalf("outputs after removal = %+v, want DP-1, DP-2", got)
	}
	if err := lister.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("listener Close: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("dynamic fake compositor: %v", err)
	}
}

//nolint:gocyclo // fake compositor drives the protocol sequence explicitly.
func serveDynamicWaylandOutputs(
	listener *net.UnixListener,
	addOutput <-chan struct{},
	ready chan<- struct{},
) error {
	conn, err := listener.AcceptUnix()
	if err != nil {
		return err
	}
	defer conn.Close()

	var registryID uint32
	outputIDs := make(map[uint32]uint32, 3)
	var writeMu sync.Mutex

	roundTrips := 0
	for {
		message, err := readWaylandMessage(conn)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		sender := wl.Uint32(message[0:4])
		opcode := wl.Uint32(message[4:8]) & 0xffff
		payload := message[8:]
		switch {
		case sender == 1 && opcode == 1:
			if len(payload) < 4 {
				return errors.New("dynamic registry request missing object ID")
			}
			registryID = wl.Uint32(payload[:4])
		case sender == registryID && opcode == 0:
			name, objectID, err := parseWaylandBind(payload)
			if err != nil {
				return err
			}
			if name == 4 || name == 12 || name == 20 {
				outputIDs[name] = objectID
			}
		case sender == 1 && opcode == 0:
			roundTrips++
			switch roundTrips {
			case 1:
				if err := sendWaylandGlobalEventsLocked(&writeMu, conn, registryID); err != nil {
					return err
				}
			case 2:
				for _, output := range []struct {
					name uint32
					text string
				}{
					{name: 4, text: "DP-1"},
					{name: 12, text: "DP-2"},
				} {
					if err := sendWaylandOutputNameLocked(&writeMu, conn, outputIDs[output.name], output.text); err != nil {
						return err
					}
				}
			case 4:
				if err := sendWaylandOutputNameLocked(&writeMu, conn, outputIDs[20], "DP-3"); err != nil {
					return err
				}
			}
			if len(payload) < 4 {
				return errors.New("dynamic sync request missing callback ID")
			}
			if err := writeWaylandMessageLocked(&writeMu, conn, wl.Uint32(payload[:4]), 0, nil); err != nil {
				return err
			}
			if roundTrips == 2 {
				close(ready)
				<-addOutput
				if err := sendWaylandGlobalEventLocked(&writeMu, conn, registryID, 20, "wl_output", 4); err != nil {
					return err
				}
			}
			if roundTrips == 4 {
				if err := writeWaylandMessageLocked(&writeMu, conn, registryID, 1, appendUint32(nil, 20)); err != nil {
					return err
				}
			}
		}
	}
}

func writeWaylandMessageLocked(mu *sync.Mutex, conn net.Conn, sender, opcode uint32, payload []byte) error {
	mu.Lock()
	defer mu.Unlock()
	return writeWaylandMessage(conn, sender, opcode, payload)
}

func sendWaylandGlobalEventLocked(mu *sync.Mutex, conn net.Conn, registryID, name uint32, iface string, version uint32) error {
	data := appendUint32(nil, name)
	data = appendWlStringData(data, iface)
	data = appendUint32(data, version)
	return writeWaylandMessageLocked(mu, conn, registryID, 0, data)
}

func sendWaylandGlobalEventsLocked(mu *sync.Mutex, conn net.Conn, registryID uint32) error {
	for _, event := range []struct {
		name    uint32
		version uint32
	}{
		{name: 12, version: 4},
		{name: 4, version: 4},
	} {
		if err := sendWaylandGlobalEventLocked(mu, conn, registryID, event.name, "wl_output", event.version); err != nil {
			return err
		}
	}
	return nil
}

func sendWaylandOutputNameLocked(mu *sync.Mutex, conn net.Conn, objectID uint32, name string) error {
	if objectID == 0 {
		return errors.New("output was not bound")
	}
	return writeWaylandMessageLocked(mu, conn, objectID, 4, wlStringData(name))
}

func serveWaylandOutputs(listener *net.UnixListener) error {
	conn, err := listener.AcceptUnix()
	if err != nil {
		return err
	}
	defer conn.Close()

	var registryID uint32
	outputIDs := make(map[uint32]uint32, 2)
	roundTrips := 0
	for {
		message, err := readWaylandMessage(conn)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var handleErr error
		registryID, roundTrips, handleErr = handleWaylandOutputRequest(
			conn,
			message,
			registryID,
			outputIDs,
			roundTrips,
		)
		if handleErr != nil {
			return handleErr
		}
	}
}

func handleWaylandOutputRequest(
	conn net.Conn,
	message []byte,
	registryID uint32,
	outputIDs map[uint32]uint32,
	roundTrips int,
) (uint32, int, error) {
	sender := wl.Uint32(message[0:4])
	opcode := wl.Uint32(message[4:8]) & 0xffff
	payload := message[8:]
	switch {
	case sender == 1 && opcode == 1:
		if len(payload) < 4 {
			return registryID, roundTrips, errors.New("get_registry request missing new object ID")
		}
		return wl.Uint32(payload[:4]), roundTrips, nil
	case sender == 1 && opcode == 0:
		roundTrips++
		if err := sendWaylandRoundTripEvents(conn, payload, registryID, outputIDs, roundTrips); err != nil {
			return registryID, roundTrips, err
		}
		return registryID, roundTrips, nil
	case sender == registryID && opcode == 0:
		name, objectID, err := parseWaylandBind(payload)
		if err != nil {
			return registryID, roundTrips, err
		}
		if name == 4 || name == 12 {
			outputIDs[name] = objectID
		}
	}
	return registryID, roundTrips, nil
}

func sendWaylandRoundTripEvents(
	conn net.Conn,
	payload []byte,
	registryID uint32,
	outputIDs map[uint32]uint32,
	roundTrips int,
) error {
	switch roundTrips {
	case 1:
		if err := sendWaylandGlobalEvents(conn, registryID); err != nil {
			return err
		}
	case 2:
		if len(outputIDs) != 2 {
			return fmt.Errorf("bound output objects = %d, want 2", len(outputIDs))
		}
		for _, event := range []struct {
			name uint32
			text string
		}{
			{name: 12, text: "DP-2"},
			{name: 4, text: "DP-1"},
		} {
			if err := writeWaylandMessage(
				conn,
				outputIDs[event.name],
				4,
				wlStringData(event.text),
			); err != nil {
				return err
			}
		}
	}
	if len(payload) < 4 {
		return errors.New("sync request missing callback ID")
	}
	return writeWaylandMessage(conn, wl.Uint32(payload[:4]), 0, nil)
}

func sendWaylandGlobalEvents(conn net.Conn, registryID uint32) error {
	for _, event := range []struct {
		name    uint32
		version uint32
	}{
		{name: 12, version: 4},
		{name: 4, version: 4},
	} {
		data := make([]byte, 0, 32)
		var name [4]byte
		wl.PutUint32(name[:], event.name)
		data = append(data, name[:]...)
		data = appendWlStringData(data, "wl_output")
		var version [4]byte
		wl.PutUint32(version[:], event.version)
		data = append(data, version[:]...)
		if err := writeWaylandMessage(conn, registryID, 0, data); err != nil {
			return err
		}
	}
	return nil
}

func parseWaylandBind(payload []byte) (uint32, uint32, error) {
	if len(payload) < 8 {
		return 0, 0, errors.New("bind request missing interface")
	}
	name := wl.Uint32(payload[:4])
	length := int(wl.Uint32(payload[4:8]))
	if length <= 0 {
		return 0, 0, errors.New("bind request has empty interface")
	}
	padded := (length + 3) &^ 3
	offset := 4 + 4 + padded
	if offset+8 > len(payload) {
		return 0, 0, errors.New("bind request missing object ID")
	}
	return name, wl.Uint32(payload[offset+4 : offset+8]), nil
}

func readWaylandMessage(conn net.Conn) ([]byte, error) {
	var header [8]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, err
	}
	size := int(wl.Uint32(header[4:8]) >> 16)
	if size < len(header) {
		return nil, fmt.Errorf("invalid Wayland message size %d", size)
	}
	message := make([]byte, size)
	copy(message, header[:])
	if _, err := io.ReadFull(conn, message[len(header):]); err != nil {
		return nil, err
	}
	return message, nil
}

func writeWaylandMessage(conn net.Conn, sender, opcode uint32, payload []byte) error {
	message := make([]byte, 8+len(payload))
	wl.PutUint32(message[0:4], sender)
	wl.PutUint32(message[4:8], uint32(len(message))<<16|opcode)
	copy(message[8:], payload)
	for len(message) > 0 {
		n, err := conn.Write(message)
		if err != nil {
			return err
		}
		message = message[n:]
	}
	return nil
}

func appendWlStringData(dst []byte, s string) []byte {
	return append(dst, wlStringData(s)...)
}

func TestReadWlString(t *testing.T) {
	t.Parallel()

	data := wlStringData("HDMI-A-1")

	got, next, ok := readWlString(data, 0)
	if !ok {
		t.Fatal("readWlString returned ok=false")
	}
	if got != "HDMI-A-1" {
		t.Fatalf("string = %q, want %q", got, "HDMI-A-1")
	}
	if next != len(data) {
		t.Fatalf("next = %d, want %d", next, len(data))
	}
}

func TestReadWlStringRejectsMalformedData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "missing nul terminator",
			data: func() []byte {
				data := make([]byte, 8)
				wl.PutUint32(data[0:4], 4)
				copy(data[4:], "abcd")
				return data
			}(),
		},
		{
			name: "missing padding bytes",
			data: func() []byte {
				data := make([]byte, 6)
				wl.PutUint32(data[0:4], 2)
				data[4] = 'x'
				data[5] = 0
				return data
			}(),
		},
		{
			name: "length extends beyond data",
			data: func() []byte {
				data := make([]byte, 8)
				wl.PutUint32(data[0:4], 8)
				return data
			}(),
		},
		{
			name: "zero length",
			data: func() []byte {
				data := make([]byte, 4)
				wl.PutUint32(data[0:4], 0)
				return data
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, next, ok := readWlString(tt.data, 0); ok {
				t.Fatalf("readWlString(%v) = %q, next=%d, ok=true; want ok=false", tt.data, got, next)
			}
		})
	}
}

func TestWaylandOutputEventParsing(t *testing.T) { //nolint:gocyclo // covers each wl_output event payload.
	t.Parallel()

	out := &waylandOutput{
		info: Info{Name: "fallback-name", Backend: "wayland", Scale: 1, Available: true},
	}
	proxy := &wl.RawProxy{}
	out.updateProxy(proxy)

	geometry := make([]byte, 20)
	wl.PutUint32(geometry[0:4], ^uint32(9))
	wl.PutUint32(geometry[4:8], 20)
	wl.PutUint32(geometry[8:12], 600)
	wl.PutUint32(geometry[12:16], 340)
	geometry = appendWlStringData(geometry, "Acme")
	geometry = appendWlStringData(geometry, "Panel 4K")
	proxy.OnEvent(0, 0, geometry)

	mode := make([]byte, 16)
	wl.PutUint32(mode[0:4], 1)
	wl.PutUint32(mode[4:8], 3840)
	wl.PutUint32(mode[8:12], 2160)
	proxy.OnEvent(1, 0, mode)

	scale := make([]byte, 4)
	wl.PutUint32(scale, 2)
	proxy.OnEvent(3, 0, scale)

	proxy.OnEvent(4, 0, wlStringData("DP-1"))
	proxy.OnEvent(5, 0, wlStringData("Acme Panel 4K"))

	if out.info.Geometry.X != -10 || out.info.Geometry.Y != 20 {
		t.Fatalf("geometry origin = %+v, want -10,20", out.info.Geometry)
	}
	if out.info.PhysicalW != 600 || out.info.PhysicalH != 340 {
		t.Fatalf("physical size = %dx%d, want 600x340", out.info.PhysicalW, out.info.PhysicalH)
	}
	if out.info.Make != "Acme" || out.info.Model != "Panel 4K" {
		t.Fatalf("make/model = %q/%q, want Acme/Panel 4K", out.info.Make, out.info.Model)
	}
	if out.info.ResolutionW != 3840 || out.info.ResolutionH != 2160 {
		t.Fatalf("resolution = %dx%d, want 3840x2160", out.info.ResolutionW, out.info.ResolutionH)
	}
	if out.info.Scale != 2 {
		t.Fatalf("scale = %d, want 2", out.info.Scale)
	}
	if out.info.Geometry.W != 1920 || out.info.Geometry.H != 1080 {
		t.Fatalf("geometry size = %dx%d, want 1920x1080", out.info.Geometry.W, out.info.Geometry.H)
	}
	if out.info.Name != "DP-1" {
		t.Fatalf("name = %q, want DP-1", out.info.Name)
	}
	if out.info.Description != "Acme Panel 4K" {
		t.Fatalf("description = %q, want Acme Panel 4K", out.info.Description)
	}
	if !out.info.Available || out.info.ScaleNumerator != 2 || out.info.ScaleDenominator != 1 {
		t.Fatalf("availability/scale = %v/%d:%d, want true/2:1", out.info.Available, out.info.ScaleNumerator, out.info.ScaleDenominator)
	}
}

func TestCeilDivDoesNotOverflowNearMaxInt(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	want := maxInt/2 + maxInt%2
	if got := ceilDiv(maxInt, 2); got != want {
		t.Fatalf("ceilDiv(%d, 2) = %d, want %d", maxInt, got, want)
	}
}

func TestWaylandXDgOutputProvidesRotatedLogicalGeometry(t *testing.T) {
	t.Parallel()

	out := newWaylandOutput(wl.GlobalEvent{Name: 4, Version: 4})
	wlProxy := &wl.RawProxy{}
	out.updateProxy(wlProxy)
	mode := make([]byte, 16)
	wl.PutUint32(mode[0:4], 1)
	wl.PutUint32(mode[4:8], 1920)
	wl.PutUint32(mode[8:12], 1080)
	wlProxy.OnEvent(1, 0, mode)
	scale := make([]byte, 4)
	wl.PutUint32(scale, 2)
	wlProxy.OnEvent(3, 0, scale)

	xdgProxy := &wl.RawProxy{}
	out.updateXDGProxy(xdgProxy)
	logicalSize := make([]byte, 8)
	wl.PutUint32(logicalSize[0:4], 540)
	wl.PutUint32(logicalSize[4:8], 960)
	xdgProxy.OnEvent(1, 0, logicalSize)

	if out.info.Geometry != (Geometry{W: 540, H: 960}) {
		t.Fatalf("rotated geometry = %+v, want 540x960", out.info.Geometry)
	}
	if out.info.Scale != 2 {
		t.Fatalf("scale = %d, want 2", out.info.Scale)
	}
}

func TestWaylandXDgOutputProvidesFractionalScaleAndLogicalGeometry(t *testing.T) {
	t.Parallel()

	out := newWaylandOutput(wl.GlobalEvent{Name: 4, Version: 4})
	wlProxy := &wl.RawProxy{}
	out.updateProxy(wlProxy)
	mode := make([]byte, 16)
	wl.PutUint32(mode[0:4], 1)
	wl.PutUint32(mode[4:8], 2560)
	wl.PutUint32(mode[8:12], 1440)
	wlProxy.OnEvent(1, 0, mode)

	xdgProxy := &wl.RawProxy{}
	out.updateXDGProxy(xdgProxy)
	position := make([]byte, 8)
	wl.PutUint32(position[0:4], ^uint32(1535))
	wl.PutUint32(position[4:8], 10)
	xdgProxy.OnEvent(0, 0, position)
	logicalSize := make([]byte, 8)
	wl.PutUint32(logicalSize[0:4], 2048)
	wl.PutUint32(logicalSize[4:8], 1152)
	xdgProxy.OnEvent(1, 0, logicalSize)

	if out.info.Geometry != (Geometry{X: -1536, Y: 10, W: 2048, H: 1152}) {
		t.Fatalf("logical geometry = %+v, want -1536,10 2048x1152", out.info.Geometry)
	}
	if out.info.Scale != 0 || out.info.ScaleNumerator != 5 || out.info.ScaleDenominator != 4 {
		t.Fatalf("fractional scale = %d/%d:%d, want 0/5:4", out.info.Scale, out.info.ScaleNumerator, out.info.ScaleDenominator)
	}
}

func TestWaylandOutputEventParsingIgnoresMalformedEvents(t *testing.T) {
	t.Parallel()

	out := &waylandOutput{
		info: Info{
			Name:        "before",
			Description: "description before",
			Geometry:    Geometry{X: 1, Y: 2, W: 3, H: 4},
			ResolutionW: 3,
			ResolutionH: 4,
			Scale:       1,
		},
	}
	proxy := &wl.RawProxy{}
	out.updateProxy(proxy)

	proxy.OnEvent(0, 0, []byte{1, 2, 3})
	proxy.OnEvent(1, 0, []byte{1, 2, 3})
	proxy.OnEvent(3, 0, []byte{1, 2, 3})
	proxy.OnEvent(4, 0, []byte{1, 2, 3})
	proxy.OnEvent(5, 0, []byte{1, 2, 3})
	largeLength := make([]byte, 4)
	wl.PutUint32(largeLength, ^uint32(0))
	proxy.OnEvent(4, 0, largeLength)
	proxy.OnEvent(5, 0, largeLength)

	if out.info.Name != "before" {
		t.Fatalf("name changed to %q after malformed event", out.info.Name)
	}
	if out.info.Description != "description before" {
		t.Fatalf("description changed to %q after malformed event", out.info.Description)
	}
	if out.info.Geometry != (Geometry{X: 1, Y: 2, W: 3, H: 4}) {
		t.Fatalf("geometry changed to %+v after malformed event", out.info.Geometry)
	}
}

func TestOutputGlobalsPreservesSessionOrdering(t *testing.T) {
	t.Parallel()

	globals := []wl.GlobalEvent{
		{Name: 4, Interface: "wl_output", Version: 4},
		{Name: 1, Interface: "wl_seat", Version: 7},
		{Name: 12, Interface: "wl_output", Version: 3},
	}

	got := outputGlobals(globals)
	if len(got) != 2 {
		t.Fatalf("outputGlobals returned %d outputs, want 2", len(got))
	}
	if got[0].Name != 4 || got[1].Name != 12 {
		t.Fatalf("global order = %d, %d; want 4, 12", got[0].Name, got[1].Name)
	}
}

func TestWaylandListerSnapshotOutputsIsDeterministic(t *testing.T) {
	t.Parallel()

	lister := &WaylandLister{
		session: &wl.Session{},
		outputs: []*waylandOutput{
			{globalID: 12, info: Info{Name: "DP-2", Backend: "wayland"}},
			{globalID: 4, info: Info{Name: "DP-1", Backend: "wayland"}},
		},
	}

	for range 10 {
		got, err := lister.List(context.Background())
		if err != nil {
			t.Fatalf("List returned an error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("snapshot returned %d outputs, want 2", len(got))
		}
		if got[0].Name != "DP-1" || got[1].Name != "DP-2" {
			t.Fatalf("output order = %q, %q; want DP-1, DP-2", got[0].Name, got[1].Name)
		}
	}
}

func TestWaylandOutputSnapshotSynchronizesWithEvents(t *testing.T) {
	out := &waylandOutput{
		info: Info{Name: "DP-1", Backend: "wayland", Scale: 1},
	}
	proxy := &wl.RawProxy{}
	out.updateProxy(proxy)
	lister := &WaylandLister{session: &wl.Session{}, outputs: []*waylandOutput{out}}

	geometryA := make([]byte, 20)
	wl.PutUint32(geometryA[0:4], 1)
	wl.PutUint32(geometryA[4:8], 2)
	wl.PutUint32(geometryA[8:12], 600)
	wl.PutUint32(geometryA[12:16], 340)
	geometryA = appendWlStringData(geometryA, "Acme")
	geometryA = appendWlStringData(geometryA, "Panel A")
	geometryB := make([]byte, 20)
	wl.PutUint32(geometryB[0:4], 3)
	wl.PutUint32(geometryB[4:8], 4)
	wl.PutUint32(geometryB[8:12], 700)
	wl.PutUint32(geometryB[12:16], 400)
	geometryB = appendWlStringData(geometryB, "Other")
	geometryB = appendWlStringData(geometryB, "Panel B")

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			proxy.OnEvent(0, 0, geometryA)
		}()
		go func() {
			defer wg.Done()
			got, err := lister.List(context.Background())
			if err != nil {
				t.Errorf("List returned an error: %v", err)
				return
			}
			if len(got) != 1 {
				t.Errorf("snapshot returned %d outputs, want 1", len(got))
				return
			}
			info := got[0]
			if !isCompleteOutputGeometrySnapshot(info) {
				t.Errorf("snapshot contains a partial event update: %+v", info)
			}
		}()
	}
	// Exercise a second complete event shape after the concurrent snapshots;
	// this also verifies the callback remains usable after contention.
	wg.Wait()
	proxy.OnEvent(0, 0, geometryB)
	got, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	if got[0].Make != "Other" || got[0].Model != "Panel B" {
		t.Fatalf("final snapshot = %+v, want Panel B", got[0])
	}
}

func isCompleteOutputGeometrySnapshot(info Info) bool {
	validInitial := info.Geometry == (Geometry{}) &&
		info.PhysicalW == 0 && info.PhysicalH == 0 &&
		info.Make == "" && info.Model == ""
	validA := info.Geometry == (Geometry{X: 1, Y: 2}) &&
		info.PhysicalW == 600 && info.PhysicalH == 340 &&
		info.Make == "Acme" && info.Model == "Panel A"
	validB := info.Geometry == (Geometry{X: 3, Y: 4}) &&
		info.PhysicalW == 700 && info.PhysicalH == 400 &&
		info.Make == "Other" && info.Model == "Panel B"
	return validInitial || validA || validB
}

func TestWaylandTopologyReconcileSkipsDuplicateAndNilEntriesWhilePreservingSnapshots(t *testing.T) {
	l := &WaylandLister{outputs: []*waylandOutput{
		{globalID: 7, info: Info{Name: "eDP-7", Backend: "wayland", Available: true}},
		nil,
		{globalID: 2, info: Info{Name: "DP-2", Backend: "wayland", Available: true}},
	}}
	prior := l.snapshotOutputs()
	if len(prior) != 2 || prior[0].Name != "DP-2" || prior[1].Name != "eDP-7" {
		t.Fatalf("initial outputs not sorted or nil entries leaked: %+v", prior)
	}
	prior[0].Name = "mutated outside snapshot"
	if got := l.snapshotOutputs(); got[0].Name != "DP-2" {
		t.Fatalf("caller modified shared output state: %+v", got)
	}
	globals := []wl.GlobalEvent{
		{Name: 2, Interface: "wl_output", Version: 4},
		{Name: 2, Interface: "wl_output", Version: 1},
		{Name: 10, Interface: "wl_output", Version: 3},
		{Name: 10, Interface: "wl_output", Version: 2},
		{Name: 99, Interface: "wl_seat", Version: 4},
	}
	desired, added, removed := l.topologyChanges(outputGlobals(globals))
	if len(desired) != 2 || len(added) != 1 || added[0].globalID != 10 || len(removed) != 1 || removed[0].globalID != 7 {
		t.Fatalf("topology failed to reconcile announcements desired=%v added=%+v removed=%+v", desired, added, removed)
	}
	if remain := l.snapshotOutputs(); len(remain) != 1 || remain[0].Name != "DP-2" {
		t.Fatalf("reconciliation lost retained output: %+v", remain)
	}
	if err := l.releaseOutput(context.Background(), removed[0]); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseOutput(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestWaylandWireStringReaderKeepsFirstValidFieldOnTruncatedSecond(t *testing.T) {
	first := wlStringData("Acme")
	second := appendUint32(nil, 6)
	second = append(second, []byte("bad")...)
	got, secondValue, ok := readWlStrings(append(first, second...), 0)
	if ok || got != "Acme" || secondValue != "" {
		t.Fatalf("truncated second wire string accepted: first=%q second=%q ok=%v", got, secondValue, ok)
	}
	if v, _, valid := readWlString([]byte{1, 2, 3}, 0); valid || v != "" {
		t.Fatalf("truncated header accepted %q", v)
	}
	for _, invalid := range [][]byte{
		append(appendUint32(nil, 3), []byte{'a', 'b', 'c'}...),
		append(appendUint32(nil, 5), []byte{'a', 0, 'b', 0, 0, 0, 0, 0}...),
		append(appendUint32(nil, 2), []byte{0xff, 0, 0, 0}...),
	} {
		if got, _, valid := readWlString(invalid, 0); valid {
			t.Fatalf("untrusted wire string accepted: %q from %v", got, invalid)
		}
	}
	if got, _, ok := readWlString(wlStringData("DP-1"), 0); !ok || !strings.EqualFold(got, "DP-1") {
		t.Fatalf("valid wire string rejected: %q ok=%v", got, ok)
	}
}

// Wayland strings have one terminal NUL, not an interior NUL that can hide
// suffixes from C clients or poison display-name comparison/logging.
func TestWaylandOutputRejectsEmbeddedNULInAdvertisedNames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		xdg    bool
		opcode uint32
	}{
		{"wl_output.name", false, 4},
		{"wl_output.description", false, 5},
		{"xdg_output.name", true, 3},
		{"xdg_output.description", true, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := newWaylandOutput(wl.GlobalEvent{Name: 6, Interface: "wl_output", Version: 4})
			out.info.Name = "Original-Display"
			out.info.Description = "Original-Description"
			proxy := &wl.RawProxy{}
			if tc.xdg {
				out.updateXDGProxy(proxy)
			} else {
				out.updateProxy(proxy)
			}
			malformed := wlStringData("Trusted\x00Forged")
			if got, _, ok := readWlString(malformed, 0); ok {
				t.Fatalf("embedded-NUL wire string accepted as %q", got)
			}
			proxy.OnEvent(tc.opcode, 0, malformed)
			if out.info.Name != "Original-Display" || out.info.Description != "Original-Description" {
				t.Fatalf("untrusted name changed output metadata: %+v", out.info)
			}
		})
	}
}

// Compositor strings are UTF-8. Invalid byte sequences must not be exposed in
// output identifiers consumed by logs, JSON, or UI accessibility matching.
func TestWaylandOutputRejectsInvalidUTF8NameWithoutLosingLastGoodValue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		xdg    bool
		opcode uint32
	}{
		{"wl_output.name", false, 4},
		{"xdg_output.name", true, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := newWaylandOutput(wl.GlobalEvent{Name: 21, Interface: "wl_output", Version: 4})
			p := &wl.RawProxy{}
			if tc.xdg {
				out.updateXDGProxy(p)
			} else {
				out.updateProxy(p)
			}
			good := wlStringData("Display-é-日本")
			p.OnEvent(tc.opcode, 0, good)
			if out.info.Name != "Display-é-日本" {
				t.Fatalf("valid UTF-8 output name rejected: %q", out.info.Name)
			}
			bad := []byte{0xff, 0xfe, 0}
			wire := make([]byte, 4+4)
			wl.PutUint32(wire[:4], uint32(len(bad)))
			copy(wire[4:], bad)
			if _, _, ok := readWlString(wire, 0); ok {
				t.Fatal("malformed UTF-8 accepted")
			}
			p.OnEvent(tc.opcode, 0, wire)
			if out.info.Name != "Display-é-日本" {
				t.Fatalf("invalid UTF-8 replaced last safe output name: %q", out.info.Name)
			}
		})
	}
}

// Exercise the real Wayland registry and xdg-output negotiation rather than
// injecting events into a manually constructed listener. This private socket
// impersonates a compositor with one fractional-scale logical output.
func TestWaylandListerCompletesXDGManagerNegotiationAndReleasesObjects(t *testing.T) {
	sock := shortPrivateSocketPath(t)
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
		registry, outputID, managerID, xdgID := uint32(0), uint32(0), uint32(0), uint32(0)
		roundtrip := 0
		for {
			msg, err := readWaylandMessage(conn)
			if errors.Is(err, io.EOF) {
				done <- nil
				return
			}
			if err != nil {
				done <- err
				return
			}
			sender, opcode, body := wl.Uint32(msg[:4]), wl.Uint32(msg[4:8])&0xffff, msg[8:]
			switch {
			case sender == 1 && opcode == 1:
				registry = wl.Uint32(body)
			case sender == registry && opcode == 0:
				name, newID, err := parseWaylandBind(body)
				if err != nil {
					done <- err
					return
				}
				switch name {
				case 4:
					outputID = newID
				case 8:
					managerID = newID
				default:
					done <- fmt.Errorf("unexpected global %d", name)
					return
				}
			case sender == managerID && managerID != 0 && opcode == 1:
				if len(body) != 8 {
					done <- fmt.Errorf("xdg_output request malformed: %x", body)
					return
				}
				xdgID = wl.Uint32(body[:4])
				if wl.Uint32(body[4:8]) != outputID {
					done <- fmt.Errorf("xdg output bound to wrong physical output")
					return
				}
			case sender == 1 && opcode == 0:
				roundtrip++
				switch roundtrip {
				case 1:
					for _, ev := range []struct {
						id      uint32
						iface   string
						version uint32
					}{{4, "wl_output", 4}, {8, "zxdg_output_manager_v1", 3}} {
						global := appendUint32(nil, ev.id)
						global = appendWlStringData(global, ev.iface)
						global = appendUint32(global, ev.version)
						if err = writeWaylandMessage(conn, registry, 0, global); err != nil {
							done <- err
							return
						}
					}
				case 2:
					if outputID == 0 || managerID == 0 {
						done <- fmt.Errorf("missing bound output or xdg manager")
						return
					}
					mode := make([]byte, 16)
					wl.PutUint32(mode[:4], wl.OutputModeCurrent)
					wl.PutUint32(mode[4:8], 2560)
					wl.PutUint32(mode[8:12], 1440)
					if err = writeWaylandMessage(conn, outputID, 1, mode); err != nil {
						done <- err
						return
					}
					if err = writeWaylandMessage(conn, outputID, 4, wlStringData("physical-DP")); err != nil {
						done <- err
						return
					}
				case 3:
					if xdgID == 0 {
						done <- fmt.Errorf("xdg output proxy not requested")
						return
					}
					pos := make([]byte, 8)
					wl.PutUint32(pos[:4], 100)
					wl.PutUint32(pos[4:8], 200)
					size := make([]byte, 8)
					wl.PutUint32(size[:4], 2048)
					wl.PutUint32(size[4:8], 1152)
					for _, ev := range []struct {
						op   uint32
						data []byte
					}{{0, pos}, {1, size}, {3, wlStringData("DP-1")}, {4, wlStringData("synthetic logical screen")}} {
						if err = writeWaylandMessage(conn, xdgID, ev.op, ev.data); err != nil {
							done <- err
							return
						}
					}
				}
				if len(body) < 4 {
					done <- fmt.Errorf("missing callback ID")
					return
				}
				if err = writeWaylandMessage(conn, wl.Uint32(body[:4]), 0, make([]byte, 4)); err != nil {
					done <- err
					return
				}
			case sender == xdgID && xdgID != 0 && opcode == 0:
				// zxdg_output_v1.destroy
			case sender == outputID && outputID != 0 && opcode == 0:
				// wl_output.release
			case sender == managerID && managerID != 0 && opcode == 0:
				// zxdg_output_manager_v1.destroy
			default:
				done <- fmt.Errorf("unexpected Wayland client message sender=%d opcode=%d body=%x", sender, opcode, body)
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	l, err := NewWaylandListerContext(ctx, sock)
	if err != nil {
		t.Fatal(err)
	}
	got, err := l.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "DP-1" || got[0].Description != "synthetic logical screen" || got[0].Geometry.X != 100 || got[0].Geometry.Y != 200 || got[0].Geometry.W != 2048 || got[0].Geometry.H != 1152 || got[0].ScaleNumerator != 5 || got[0].ScaleDenominator != 4 {
		t.Fatalf("Wayland XDG manager negotiation returned %+v", got)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close negotiated Wayland backend: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Wayland connection not released")
	}
}

func TestWaylandXDGOutputBindSendsActualRequestAndTracksLogicalGeometry(t *testing.T) {
	path := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan *net.UnixConn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, lintShadow25 := listener.AcceptUnix()
		if lintShadow25 != nil {
			acceptErr <- lintShadow25
			return
		}
		accepted <- conn
	}()
	ctx, err := wl.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ctx.Close()
	var server *net.UnixConn
	select {
	case server = <-accepted:
		defer server.Close()
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("Wayland connection not accepted")
	}
	manager := &wl.RawProxy{}
	ctx.Register(manager)
	physical := &wl.RawProxy{}
	ctx.Register(physical)
	out := newWaylandOutput(wl.GlobalEvent{Name: 7, Interface: "wl_output", Version: 3})
	out.proxy = physical
	out.updateProxy(physical)
	lister := &WaylandLister{session: &wl.Session{Ctx: ctx}, xdgManager: manager}
	if err := lister.bindXDGOutput(context.Background(), out); err != nil {
		t.Fatalf("xdg-output bind: %v", err)
	}
	var wire [16]byte
	if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(server, wire[:]); err != nil {
		t.Fatal(err)
	}
	if wl.Uint32(wire[0:4]) != manager.ID() || wl.Uint32(wire[4:8]) != 16<<16|1 || wl.Uint32(wire[8:12]) != out.xdgProxy.ID() || wl.Uint32(wire[12:16]) != physical.ID() {
		t.Fatalf("unexpected get_xdg_output wire request: %x", wire)
	}
	// The registered callback updates the output through the same object
	// proxy used by a real compositor event dispatcher.
	mode := make([]byte, 16)
	wl.PutUint32(mode[0:4], 1)
	wl.PutUint32(mode[4:8], 2560)
	wl.PutUint32(mode[8:12], 1440)
	physical.OnEvent(1, 0, mode)
	if out.xdgProxy == nil {
		t.Fatal("bind did not install output listener")
	}
	pos := make([]byte, 8)
	wl.PutUint32(pos[0:4], uint32(120))
	wl.PutUint32(pos[4:8], uint32(30))
	size := make([]byte, 8)
	wl.PutUint32(size[0:4], uint32(2048))
	wl.PutUint32(size[4:8], uint32(1152))
	out.xdgProxy.OnEvent(0, 0, pos)
	out.xdgProxy.OnEvent(1, 0, size)
	if out.info.Geometry.X != 120 || out.info.Geometry.Y != 30 || out.info.Geometry.W != 2048 || out.info.Geometry.H != 1152 || out.info.ScaleNumerator != 5 || out.info.ScaleDenominator != 4 {
		t.Fatalf("xdg-output logical geometry not reflected: %+v", out.info)
	}
}

func TestWaylandXDGOutputBindFailsClosedWhenCanceled(t *testing.T) {
	path := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accept := make(chan *net.UnixConn, 1)
	go func() {
		conn, lintShadow99 := listener.AcceptUnix()
		if lintShadow99 == nil {
			accept <- conn
		}
	}()
	wlctx, err := wl.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer wlctx.Close()
	select {
	case conn := <-accept:
		defer conn.Close()
	case <-time.After(time.Second):
		t.Fatal("Wayland socket unavailable")
	}
	manager := &wl.RawProxy{}
	wlctx.Register(manager)
	physical := &wl.RawProxy{}
	wlctx.Register(physical)
	out := newWaylandOutput(wl.GlobalEvent{Name: 8, Interface: "wl_output", Version: 2})
	out.proxy = physical
	lister := &WaylandLister{session: &wl.Session{Ctx: wlctx}, xdgManager: manager}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lister.bindXDGOutput(canceled, out); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled XDG bind accepted: %v", err)
	}
	if out.xdgProxy != nil {
		t.Fatal("failed bind published an unusable output proxy")
	}
}

// wl_output.scale is a positive integer. A malformed zero event must not
// erase previously confirmed geometry when the compositor is reconfiguring.
func TestWaylandOutputRejectsZeroScaleWithoutLosingConfirmedGeometry(t *testing.T) {
	out := newWaylandOutput(wl.GlobalEvent{Name: 25, Interface: "wl_output", Version: 4})
	proxy := &wl.RawProxy{}
	out.updateProxy(proxy)
	mode := make([]byte, 16)
	wl.PutUint32(mode[:4], wl.OutputModeCurrent)
	wl.PutUint32(mode[4:8], 2560)
	wl.PutUint32(mode[8:12], 1440)
	proxy.OnEvent(1, 0, mode)
	scale := make([]byte, 4)
	wl.PutUint32(scale, 2)
	proxy.OnEvent(3, 0, scale)
	before := out.info
	wl.PutUint32(scale, 0)
	proxy.OnEvent(3, 0, scale)
	after := out.info
	if after.Scale != before.Scale || after.ScaleNumerator != before.ScaleNumerator ||
		after.ScaleDenominator != before.ScaleDenominator || after.Geometry != before.Geometry {
		t.Fatalf("malformed zero scale replaced authoritative output geometry: before=%+v after=%+v", before, after)
	}
}
