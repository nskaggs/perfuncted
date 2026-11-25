package window

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/internal/testutil"
	"github.com/nskaggs/perfuncted/internal/wl"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// A compositor may finish a round-trip just as the caller cancels. A window
// title or handle observed in that reply must not escape a canceled read.
type lateCancellationWaylandDisplay struct {
	*mockWaylandDisplay
	cancel context.CancelFunc
	calls  int
}

func (d *lateCancellationWaylandDisplay) RoundTripContext(context.Context) error {
	d.calls++
	d.cancel()
	return nil
}

func TestWaylandActiveTitleNeverDisclosesLateCancelledCompositorReply(t *testing.T) {
	wm, wire, id := newStubWaylandManager("private window", true, true)
	wm.toplevels[id].Active = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &lateCancellationWaylandDisplay{mockWaylandDisplay: &mockWaylandDisplay{ctx: wire}, cancel: cancel}
	wm.display = probe
	title, err := wm.ActiveTitle(ctx)
	if !errors.Is(err, context.Canceled) || title != "" {
		t.Fatalf("late canceled Wayland title escaped: title=%q err=%v", title, err)
	}
	if probe.calls != 1 {
		t.Fatalf("test did not reach compositor: %d", probe.calls)
	}
}

func TestWaylandInfoByIDNeverDisclosesLateCancelledCompositorReply(t *testing.T) {
	wm, wire, id := newStubWaylandManager("private window", true, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &lateCancellationWaylandDisplay{mockWaylandDisplay: &mockWaylandDisplay{ctx: wire}, cancel: cancel}
	wm.display = probe
	info, err := wm.InfoByID(ctx, "50")
	if !errors.Is(err, context.Canceled) || info.ID != 0 || info.Title != "" {
		t.Fatalf("late canceled Wayland info escaped: %+v err=%v", info, err)
	}
	if id != 50 || probe.calls != 1 {
		t.Fatalf("test did not reach expected compositor handle: id=%d calls=%d", id, probe.calls)
	}
}

func TestWaylandSyncMustNotAcknowledgeLateCanceledRoundTrip(t *testing.T) {
	wm, wire, _ := newStubWaylandManager("private window", true, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &lateCancellationWaylandDisplay{mockWaylandDisplay: &mockWaylandDisplay{ctx: wire}, cancel: cancel}
	wm.display = probe
	if err := wm.Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("late canceled Wayland Sync reported success: %v", err)
	}
	if probe.calls != 1 {
		t.Fatalf("did not exercise round trip: calls=%d", probe.calls)
	}
}

var _ wl.Ctx = (*mockWaylandContext)(nil)

// A compositor event channel must not outlive its backend. Otherwise a waiter
// without a caller deadline can be stranded after the window manager closes.
func TestWaylandWindowChangesCloseEndsPreexistingAndLateSubscriptions(t *testing.T) {
	for _, priorSubscription := range []bool{true, false} {
		t.Run(map[bool]string{true: "subscribed before close", false: "subscribed after close"}[priorSubscription], func(t *testing.T) {
			m := &WaylandWindowManager{}
			var events <-chan struct{}
			if priorSubscription {
				events = m.WindowChanges()
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			if !priorSubscription {
				events = m.WindowChanges()
			}
			if events == nil {
				t.Fatal("manager did not provide a closable event stream")
			}
			select {
			case _, open := <-events:
				if open {
					t.Fatal("closed manager emitted a usable event")
				}
			case <-time.After(50 * time.Millisecond):
				t.Fatal("closed Wayland manager left subscribers waiting")
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			m.notifyWindowChange()
			select {
			case _, open := <-events:
				if open {
					t.Fatal("closed manager emitted a late event")
				}
			default:
				t.Fatal("close did not keep event stream terminated")
			}
		})
	}
}

// WindowChanges, Close, and foreign-toplevel event dispatch can race. Every
// subscriber must receive a terminating close without panics from late sends.
func TestWaylandWindowChangesConcurrentSubscribeNotifyAndClose(t *testing.T) {
	const attempts = 32
	for trial := 0; trial < 4; trial++ {
		m := &WaylandWindowManager{}
		start := make(chan struct{})
		var group sync.WaitGroup
		for i := 0; i < attempts; i++ {
			group.Add(3)
			go func() { defer group.Done(); <-start; m.notifyWindowChange() }()
			go func() { defer group.Done(); <-start; _ = m.WindowChanges() }()
			go func() { defer group.Done(); <-start; _ = m.Close() }()
		}
		close(start)
		group.Wait()
		changes := m.WindowChanges()
		terminated := false
		for i := 0; i < 2; i++ {
			select {
			case _, open := <-changes:
				if !open {
					terminated = true
				}
			default:
				t.Fatalf("trial %d: change stream did not terminate", trial)
			}
			if terminated {
				break
			}
		}
		if !terminated {
			t.Fatalf("trial %d: change stream remained active after Close", trial)
		}
	}
}

func TestWaylandWindowChangesDropPendingNotificationsOnShutdown(t *testing.T) {
	m := &WaylandWindowManager{}
	events := m.WindowChanges()
	m.notifyWindowChange()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, open := <-events:
		if open {
			t.Fatal("closing the compositor delivered a stale buffered window change")
		}
	default:
		t.Fatal("close left subscribed stream open")
	}
}

func wireWindowGlobal(id uint32, iface string, version uint32) []byte {
	n := len(iface) + 1
	pad := (n + 3) &^ 3
	body := make([]byte, 8+pad+4)
	wl.PutUint32(body[0:], id)
	wl.PutUint32(body[4:], uint32(n))
	copy(body[8:], iface)
	wl.PutUint32(body[8+pad:], version)
	return body
}
func wireWindowEvent(conn *net.UnixConn, sender, opcode uint32, body []byte) error {
	msg := make([]byte, 8+len(body))
	wl.PutUint32(msg[0:], sender)
	wl.PutUint32(msg[4:], uint32(len(msg))<<16|opcode)
	copy(msg[8:], body)
	_, err := conn.Write(msg)
	return err
}
func wireWindowWords(words ...uint32) []byte {
	b := make([]byte, 4*len(words))
	for i, v := range words {
		wl.PutUint32(b[4*i:], v)
	}
	return b
}
func wireWindowString(v string) []byte {
	n := len(v) + 1
	padded := (n + 3) &^ 3
	buf := make([]byte, 4+padded)
	wl.PutUint32(buf, uint32(n))
	copy(buf[4:], v)
	return buf
}
func serveWindowCompositor(conn *net.UnixConn, protocol string) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var registryID, managerID, seatID uint32
	syncs := 0
	for {
		var h [8]byte
		if _, err := io.ReadFull(conn, h[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		size := int(wl.Uint32(h[4:8]) >> 16)
		if size < 8 || size > 65535 {
			return fmt.Errorf("invalid request size %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		sender, opcode := wl.Uint32(h[:4]), wl.Uint32(h[4:8])&0xffff
		if sender == 1 && opcode == 1 {
			registryID = wl.Uint32(body)
			continue
		}
		if sender == registryID && opcode == 0 {
			name := wl.Uint32(body)
			newID := wl.Uint32(body[len(body)-4:])
			ver := wl.Uint32(body[len(body)-8 : len(body)-4])
			switch name {
			case 41:
				if protocol != "wlr" && protocol != "both" {
					return fmt.Errorf("wlr manager unexpectedly bound")
				}
				if ver != 3 {
					return fmt.Errorf("wrong wlr manager version %d", ver)
				}
				managerID = newID
			case 42:
				if protocol != "ext" {
					return fmt.Errorf("unexpected ext manager bound")
				}
				if ver != 1 {
					return fmt.Errorf("wrong ext manager version %d", ver)
				}
				managerID = newID
			case 43:
				if ver != 1 {
					return fmt.Errorf("wrong seat bind version %d", ver)
				}
				seatID = newID
			default:
				return fmt.Errorf("unexpected global %d bound", name)
			}
			continue
		}
		if sender == 1 && opcode == 0 {
			cb := wl.Uint32(body)
			syncs++
			if syncs == 1 {
				if protocol == "wlr" || protocol == "both" {
					if err := wireWindowEvent(conn, registryID, 0, wireWindowGlobal(41, "zwlr_foreign_toplevel_manager_v1", 3)); err != nil {
						return err
					}
				}
				if protocol == "ext" || protocol == "both" {
					if err := wireWindowEvent(conn, registryID, 0, wireWindowGlobal(42, "ext_foreign_toplevel_list_v1", 1)); err != nil {
						return err
					}
				}
				if protocol != "none" {
					if err := wireWindowEvent(conn, registryID, 0, wireWindowGlobal(43, "wl_seat", 9)); err != nil {
						return err
					}
				}
			}
			if syncs == 2 && managerID != 0 {
				if seatID == 0 {
					return errors.New("Wayland seat was not bound before manager sync")
				}
				if err := wireWindowEvent(conn, managerID, 0, wireWindowWords(77)); err != nil {
					return err
				}
				titleOp := uint32(0)
				if protocol == "ext" {
					titleOp = 2
				}
				if err := wireWindowEvent(conn, 77, titleOp, wireWindowString("Editor")); err != nil {
					return err
				}
				if protocol == "ext" {
					if err := wireWindowEvent(conn, 77, 4, wireWindowString("stable-editor-7")); err != nil {
						return err
					}
				}
			}
			if err := wireWindowEvent(conn, cb, 0, wireWindowWords(0)); err != nil {
				return err
			}
			continue
		}
	}
}

func TestWaylandWindowManagerRealSyntheticConstructorAndToplevelDiscovery(t *testing.T) {
	for _, proto := range []string{"wlr", "ext", "both", "none"} {
		t.Run(proto, func(t *testing.T) {
			sock := testutil.ShortSocketPath(t, "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, lintShadow166 := listener.AcceptUnix()
				if lintShadow166 != nil {
					done <- lintShadow166
					return
				}
				done <- serveWindowCompositor(conn, proto)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			wm, err := NewWaylandWindowManagerForSocketContext(ctx, sock)
			if proto == "none" {
				if wm != nil || err == nil || !strings.Contains(err.Error(), "neither") {
					t.Fatalf("unsupported compositor incorrectly initialized: %v %v", wm, err)
				}
			} else {
				if err != nil {
					t.Fatalf("valid compositor refused: %v", err)
				}
				if wm == nil || wm.seat == nil {
					t.Fatal("constructor did not bind advertised seat")
				}
				windows, listErr := wm.List(ctx)
				if listErr != nil || len(windows) != 1 || windows[0].Title != "Editor" || windows[0].ID != 77 {
					t.Fatalf("live toplevels=%+v error=%v", windows, listErr)
				}
				if proto == "ext" {
					if windows[0].NativeID != "stable-editor-7" {
						t.Fatalf("stable protocol ID lost: %+v", windows[0])
					}
				}
				if proto == "both" && wm.wlrMgrID == 0 {
					t.Fatalf("failed to prefer control-capable manager")
				}
				if err := wm.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case serverErr := <-done:
				if serverErr != nil {
					t.Fatalf("synthetic Wayland server: %v", serverErr)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("compositor connection not released")
			}
		})
	}
}

func TestWaylandWindowEnumerationStableUnderEveryMetadataTie(t *testing.T) {
	base := Info{ID: 77, NativeID: "shared", Title: "Editor", AppID: "com.example.Editor", Class: "edit", PID: 450, X: 10, Y: 20, W: 640, H: 480}
	for _, tc := range []struct {
		name   string
		change func(*Info)
	}{
		{"native handle", func(i *Info) { i.ID++ }},
		{"title", func(i *Info) { i.Title = "Terminal" }},
		{"application", func(i *Info) { i.AppID = "com.example.Terminal" }},
		{"class", func(i *Info) { i.Class = "terminal" }},
		{"process id", func(i *Info) { i.PID++ }},
		{"x coordinate", func(i *Info) { i.X++ }},
		{"y coordinate", func(i *Info) { i.Y++ }},
		{"width", func(i *Info) { i.W++ }},
		{"height", func(i *Info) { i.H++ }},
		{"active", func(i *Info) { i.Active = true }},
		{"minimized", func(i *Info) { i.Minimized = true }},
		{"maximized", func(i *Info) { i.Maximized = true }},
		{"fullscreen", func(i *Info) { i.Fullscreen = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			second := base
			tc.change(&second)
			wm, _, _ := newStubWaylandManager("", true, false)
			wm.toplevels = map[uint32]*Info{9: &second, 3: &base}
			for range 12 {
				windows, err := wm.List(context.Background())
				if err != nil || len(windows) != 2 {
					t.Fatalf("window enumeration failed: %+v %v", windows, err)
				}
				if compareWaylandWindows(windows[0], windows[1]) >= 0 || windows[0] != base || windows[1] != second {
					t.Fatalf("unstable ordering when %s differs: %+v", tc.name, windows)
				}
			}
		})
	}
}

// A Wayland window manager owns one release of its underlying transport.
// Repeated Close must not close a shared/observed transport repeatedly.
func TestWaylandWindowManagerRepeatedCloseReleasesTransportOnce(t *testing.T) {
	wm, wire, _ := newStubWaylandManager("Lifetime", true, false)
	if err := wm.Close(); err != nil {
		t.Fatal(err)
	}
	if err := wm.Close(); err != nil {
		t.Fatal(err)
	}
	if wire.closeCount != 1 {
		t.Fatalf("transport closed %d times; want exactly once", wire.closeCount)
	}
}

// Foreign-toplevel new_id must not overwrite other already-registered objects,
// including the Wayland registry and the seat used for activation.
func TestWaylandForeignToplevelRejectsOtherExistingProtocolIdentities(t *testing.T) {
	for _, wlr := range []bool{false, true} {
		protocol := "ext"
		if wlr {
			protocol = "wlroots"
		}
		for _, identity := range []string{"registry", "seat"} {
			t.Run(protocol+"/"+identity, func(t *testing.T) {
				m, wire, _ := newStubWaylandManager("", wlr, true)
				var id uint32
				if identity == "registry" {
					id = m.registry.ID()
				} else {
					id = m.seat.ID()
				}
				original := wire.objects[id]
				if original == nil {
					t.Fatalf("missing %s proxy %d", identity, id)
				}
				dispatched := false
				wire.dispatchFn = func() error {
					if dispatched {
						return nil
					}
					dispatched = true
					var manager *wl.RawProxy
					for proxyID, candidate := range wire.objects {
						if proxy, ok := candidate.(*wl.RawProxy); ok && proxyID != m.seat.ID() {
							manager = proxy
							break
						}
					}
					if manager == nil {
						t.Fatal("missing foreign-toplevel manager proxy")
					}
					var announcement [4]byte
					wl.PutUint32(announcement[:], id)
					manager.Dispatch(0, -1, announcement[:])
					return nil
				}
				if err := m.fetchToplevels(context.Background()); err != nil {
					t.Fatal(err)
				}
				if wire.objects[id] != original {
					t.Fatalf("foreign-toplevel replaced %s proxy %d", identity, id)
				}
				if _, exists := m.toplevels[id]; exists {
					t.Fatalf("foreign-toplevel published aliased %s object", identity)
				}
			})
		}
	}
}

// Discovery is transactional: a failed second subscription must not silently
// remove an existing window when the provider reuses an already known ID.
func TestWaylandFailedDiscoveryPreservesExistingCollidingHandle(t *testing.T) {
	for _, tc := range []struct {
		name string
		wlr  bool
	}{{"wlroots", true}, {"ext", false}} {
		t.Run(tc.name, func(t *testing.T) {
			manager, wire, _ := newStubWaylandManager("", tc.wlr, false)
			original := &Info{ID: 77, NativeID: "original", Title: "Editor"}
			manager.toplevels[77] = original
			priorProxy := &wl.RawProxy{}
			wire.SetProxy(77, priorProxy)
			wire.dispatchFn = func() error {
				var p *wl.RawProxy
				for _, obj := range wire.objects {
					if candidate, ok := obj.(*wl.RawProxy); ok && candidate != priorProxy {
						p = candidate
						break
					}
				}
				if p == nil {
					t.Fatal("manager proxy not registered")
				}
				var data [4]byte
				wl.PutUint32(data[:], 77)
				p.Dispatch(0, -1, data[:])
				return errors.New("synthetic setup failed after duplicate handle")
			}
			if err := manager.fetchToplevels(context.Background()); err == nil {
				t.Fatal("expected setup failure")
			}
			if got := manager.toplevels[77]; got != original {
				t.Fatalf("existing window lost after failed discovery: got=%+v", got)
			}
			if got := wire.objects[77]; got != priorProxy {
				t.Fatalf("existing handle proxy lost after failed discovery: got=%T", got)
			}
		})
	}
}

// The compositor may retain a callback reference after an aborted initial
// round trip. A rejected subscription cannot subsequently publish windows.
func TestWaylandFailedSubscriptionCannotPublishFromLateCallback(t *testing.T) {
	for _, tc := range []struct {
		name string
		wlr  bool
	}{{"wlroots", true}, {"ext", false}} {
		t.Run(tc.name, func(t *testing.T) {
			m, wire, _ := newStubWaylandManager("", tc.wlr, false)
			var stale *wl.RawProxy
			wire.dispatchFn = func() error {
				for _, p := range wire.objects {
					if proxy, ok := p.(*wl.RawProxy); ok {
						stale = proxy
						break
					}
				}
				return errors.New("synthetic compositor setup failure")
			}
			if err := m.fetchToplevels(context.Background()); err == nil {
				t.Fatal("expected setup failure")
			}
			if stale == nil {
				t.Fatal("failed discovery did not expose a manager callback")
			}
			var id [4]byte
			wl.PutUint32(id[:], 77)
			stale.Dispatch(0, -1, id[:])
			if _, ok := m.toplevels[77]; ok {
				t.Fatal("late event from failed subscription published a window")
			}
			if _, ok := wire.objects[77]; ok {
				t.Fatal("late event from failed subscription registered a proxy")
			}
			if m.changes != nil {
				select {
				case <-m.changes:
					t.Fatal("late failed subscription emitted a window notification")
				default:
				}
			}
		})
	}
}

// The Wayland object ID zero is reserved. A malformed compositor event may
// not publish an unusable window or register a proxy under that ID.
func TestWaylandForeignToplevelRejectsReservedZeroHandle(t *testing.T) {
	for _, wlr := range []bool{false, true} {
		name := "ext"
		if wlr {
			name = "wlroots"
		}
		t.Run(name, func(t *testing.T) {
			manager, wire, _ := newStubWaylandManager("", wlr, false)
			dispatched := false
			wire.dispatchFn = func() error {
				if dispatched {
					return nil
				}
				dispatched = true
				var mgr *wl.RawProxy
				for _, p := range wire.objects {
					if obj, ok := p.(*wl.RawProxy); ok {
						mgr = obj
						break
					}
				}
				if mgr == nil {
					t.Fatal("foreign-toplevel manager not registered")
				}
				mgr.Dispatch(0, -1, []byte{0, 0, 0, 0})
				return nil
			}
			if err := manager.fetchToplevels(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, ok := manager.toplevels[0]; ok {
				t.Fatal("reserved zero ID was published as a window")
			}
			if _, ok := wire.objects[0]; ok {
				t.Fatal("reserved zero ID was registered as a Wayland proxy")
			}
			if manager.changes != nil {
				select {
				case <-manager.changes:
					t.Fatal("invalid event triggered window notification")
				default:
				}
			}
		})
	}
}

// Retired Wayland callbacks may still be dispatched by a buffered reader;
// after shutdown none may revive the manager's window state.
func TestWaylandLateToplevelEventsCannotReviveClosedManager(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wlr     bool
		titleOp uint32
	}{{"wlroots", true, 0}, {"ext", false, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			m, wire, _ := newStubWaylandManager("", tc.wlr, false)
			if err := m.fetchToplevels(context.Background()); err != nil {
				t.Fatal(err)
			}
			var mgr *wl.RawProxy
			for _, obj := range wire.objects {
				if p, ok := obj.(*wl.RawProxy); ok {
					mgr = p
					break
				}
			}
			if mgr == nil {
				t.Fatal("no manager callback")
			}
			var id [4]byte
			wl.PutUint32(id[:], 77)
			mgr.Dispatch(0, -1, id[:])
			handle, ok := wire.objects[77].(*wl.RawProxy)
			if !ok {
				t.Fatal("no window callback")
			}
			old := m.toplevels[77]
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			data := []byte{5, 0, 0, 0, 'L', 'a', 't', 'e', 0}
			handle.Dispatch(tc.titleOp, -1, data)
			if old.Title != "" {
				t.Fatalf("closed manager accepted late title: %q", old.Title)
			}
			wl.PutUint32(id[:], 88)
			mgr.Dispatch(0, -1, id[:])
			if _, ok := m.toplevels[88]; ok {
				t.Fatal("closed manager published a late new window")
			}
			if _, ok := wire.objects[88]; ok {
				t.Fatal("closed manager registered a late proxy")
			}
		})
	}
}

func TestWaylandForeignToplevelIgnoresMalformedManagerEvents(t *testing.T) {
	manager, wire, _ := newStubWaylandManager("", true, false)
	managerProxyID := wire.nextID + 1
	wire.dispatchFn = func() error {
		proxy, ok := wire.objects[managerProxyID].(*wl.RawProxy)
		if !ok {
			return fmt.Errorf("foreign-toplevel manager proxy %d is unavailable", managerProxyID)
		}
		proxy.Dispatch(1, -1, []byte{77, 0, 0, 0})
		proxy.Dispatch(0, -1, []byte{77, 0, 0})
		return nil
	}
	if err := manager.fetchToplevels(context.Background()); err != nil {
		t.Fatalf("fetch empty compositor snapshot: %v", err)
	}
	windows, err := manager.List(context.Background())
	if err != nil || len(windows) != 0 {
		t.Fatalf("malformed manager events published windows: %+v, %v", windows, err)
	}
}

// A malicious compositor must not be able to reassign the foreign-toplevel
// manager's existing protocol object identity to an advertised window.
// Replacing the manager proxy would strand its event handler and break
// subsequent window discovery even when the malformed event is ignored.
func TestWaylandForeignToplevelRejectsManagerObjectIDAlias(t *testing.T) {
	for _, wlr := range []bool{false, true} {
		label := "ext"
		if wlr {
			label = "wlroots"
		}
		t.Run(label, func(t *testing.T) {
			manager, wire, _ := newStubWaylandManager("", wlr, false)
			delivered := false
			var original *wl.RawProxy
			wire.dispatchFn = func() error {
				if delivered {
					return nil
				}
				delivered = true
				for _, p := range wire.objects {
					if proxy, ok := p.(*wl.RawProxy); ok {
						original = proxy
						break
					}
				}
				if original == nil {
					t.Fatal("no manager proxy registered")
				}
				var id [4]byte
				wl.PutUint32(id[:], original.ID())
				original.Dispatch(0, -1, id[:])
				return nil
			}
			if err := manager.fetchToplevels(context.Background()); err != nil {
				t.Fatal(err)
			}
			if original == nil {
				t.Fatal("manager was never dispatched")
			}
			if current := wire.objects[original.ID()]; current != original {
				t.Fatalf("compositor replaced manager protocol proxy %d: %T", original.ID(), current)
			}
			if _, exists := manager.toplevels[original.ID()]; exists {
				t.Fatal("aliased manager ID published as toplevel")
			}
			if len(manager.toplevels) != 0 {
				t.Fatalf("unexpected windows: %v", manager.toplevels)
			}
		})
	}
}

// Late callbacks can hold a nil manager while compositor teardown is in
// progress. Reporting a notification must not panic or publish an event.
func TestWaylandNilManagerChangeNotificationFailsClosed(t *testing.T) {
	var manager *WaylandWindowManager
	manager.notifyWindowChange()
	if changes := manager.WindowChanges(); changes != nil {
		t.Fatal("nil manager advertised an event channel")
	}
}

// A compositor can advertise handles and still fail its setup round trip.
// Those provisional handles must not outlive their rejected subscription.
func TestWaylandFailedPartialDiscoveryReleasesProvisionalHandles(t *testing.T) {
	for _, tc := range []struct {
		name string
		wlr  bool
	}{{"wlroots", true}, {"ext", false}} {
		t.Run(tc.name, func(t *testing.T) {
			m, wire, _ := newStubWaylandManager("", tc.wlr, false)
			existing := &Info{ID: 55, Title: "existing"}
			m.toplevels[55] = existing
			dispatched := false
			wire.dispatchFn = func() error {
				if dispatched {
					return errors.New("synthetic setup failure")
				}
				dispatched = true
				var manager *wl.RawProxy
				for _, p := range wire.objects {
					if proxy, ok := p.(*wl.RawProxy); ok {
						manager = proxy
						break
					}
				}
				if manager == nil {
					t.Fatal("missing manager proxy")
				}
				var id [4]byte
				wl.PutUint32(id[:], 77)
				manager.Dispatch(0, -1, id[:])
				if _, ok := wire.objects[77]; !ok {
					t.Fatal("synthetic handle was not registered")
				}
				return errors.New("synthetic setup failure")
			}
			if err := m.fetchToplevels(context.Background()); err == nil {
				t.Fatal("failed round trip succeeded")
			}
			if _, ok := wire.objects[77]; ok {
				t.Fatal("failed discovery leaked provisional Wayland proxy")
			}
			if _, ok := m.toplevels[77]; ok {
				t.Fatal("failed discovery published provisional window")
			}
			if got := m.toplevels[55]; got != existing {
				t.Fatalf("unrelated window corrupted: %+v", got)
			}
		})
	}
}

// Revoked Wayland requests must fail before touching a display or a foreign
// toplevel handle. Keep the transport absent so an accidental call cannot be
// mistaken for an ordinary compositor rejection.
func TestWaylandWindowOperationsRejectPreCanceledRequestsBeforeTransport(t *testing.T) {
	var backend WaylandWindowManager
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := []struct {
		name string
		run  func() error
	}{
		{"activate", func() error { return backend.ActivateByID(ctx, "17") }},
		{"close", func() error { return backend.CloseWindowByID(ctx, "17") }},
		{"minimize", func() error { return backend.MinimizeByID(ctx, "17") }},
		{"maximize", func() error { return backend.MaximizeByID(ctx, "17") }},
		{"fullscreen", func() error { return backend.FullscreenByID(ctx, "17") }},
		{"unfullscreen", func() error { return backend.UnfullscreenByID(ctx, "17") }},
		{"restore", func() error { return backend.RestoreByID(ctx, "17") }},
		{"info", func() error { _, err := backend.InfoByID(ctx, "17"); return err }},
	}
	for _, tc := range operations {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled Wayland request proceeded: %v", err)
			}
		})
	}
}

func TestWaylandReadOnlyRestoreAndMissingInfoRemainFailClosed(t *testing.T) {
	t.Run("read-only ext protocol rejects restore before handle request", func(t *testing.T) {
		manager, wire, handleID := newStubWaylandManager("Viewer", false, false)
		if err := manager.RestoreByID(context.Background(), "50"); !errors.Is(err, ErrNotSupported) {
			t.Fatalf("RestoreByID error = %v, want ErrNotSupported", err)
		}
		if sawAnyHandleRequest(wire, handleID) {
			t.Fatal("read-only backend sent a handle control request")
		}
	})

	t.Run("missing info returns not found without handle request", func(t *testing.T) {
		manager, wire, handleID := newStubWaylandManager("Viewer", true, true)
		info, err := manager.InfoByID(context.Background(), "999")
		if !errors.Is(err, ErrWindowNotFound) || info != (Info{}) {
			t.Fatalf("InfoByID returned info=%+v err=%v, want empty info and ErrWindowNotFound", info, err)
		}
		if sawAnyHandleRequest(wire, handleID) {
			t.Fatal("missing lookup sent a handle request")
		}
	})
}

// Foreign-toplevel object IDs are reusable after the old Wayland proxy is
// retired. A delayed callback still references the original Info pointer and
// must not delete the replacement when its old close notification arrives.
func TestWaylandStaleHandleCloseDoesNotRemoveReplacement(t *testing.T) {
	for _, tc := range []struct {
		name        string
		closeOpcode uint32
		titleOpcode uint32
		deliver     func(*WaylandWindowManager, uint32, *Info, uint32, []byte)
	}{
		{"wlroots", 6, 0, (*WaylandWindowManager).handleWLRToplevelEvent},
		{"ext", 0, 2, (*WaylandWindowManager).handleExtToplevelEvent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := &WaylandWindowManager{toplevels: make(map[uint32]*Info)}
			old := &Info{ID: 42, NativeID: "old", Title: "Old"}
			replacement := &Info{ID: 42, NativeID: "new", Title: "New"}
			manager.toplevels[42] = old
			// Represent a second manager creation event after the old object
			// was retired. The old callback can still be delivered late.
			manager.toplevels[42] = replacement
			// Stale metadata must also be ignored rather than waking discovery
			// observers or publishing attributes from a retired object.
			staleTitle := []byte{6, 0, 0, 0, 'S', 't', 'a', 'l', 'e', 0}
			tc.deliver(manager, 42, old, tc.titleOpcode, staleTitle)
			if old.Title != "Old" || replacement.Title != "New" {
				t.Fatalf("stale event mutated toplevel metadata: old=%+v new=%+v", old, replacement)
			}
			if manager.changes != nil {
				select {
				case <-manager.changes:
					t.Fatal("stale event falsely signaled a window change")
				default:
				}
			}
			tc.deliver(manager, 42, old, tc.closeOpcode, nil)
			got, ok := manager.toplevels[42]
			if !ok || got != replacement || got.Title != "New" {
				t.Fatalf("stale close retired replacement handle: present=%v got=%+v", ok, got)
			}
			tc.deliver(manager, 42, replacement, tc.closeOpcode, nil)
			if len(manager.toplevels) != 0 {
				t.Fatalf("current handle close was ignored: %+v", manager.toplevels)
			}
		})
	}
}

// A manager with no compositor connection must not advertise window mutation
// capabilities. Its sync is a no-op, not a request to an absent display.
func TestWaylandUninitializedManagerHasReadOnlyCapabilities(t *testing.T) {
	var manager WaylandWindowManager
	want := []string{"discover", "info", "active-title"}
	if got := manager.SupportedOperations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("unconnected manager advertised control capabilities: %v want %v", got, want)
	}
	if err := manager.Sync(context.Background()); err != nil {
		t.Fatalf("unconnected manager sync failed: %v", err)
	}
	var absent *WaylandWindowManager
	if got := absent.SupportedOperations(); len(got) != 0 {
		t.Fatalf("nil manager advertised capabilities: %v", got)
	}
	if err := absent.Sync(context.Background()); err == nil {
		t.Fatal("nil manager was accepted as connected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled no-op sync ignored revocation: %v", err)
	}
}

// Window titles and app IDs originate with untrusted clients. Reject embedded
// NUL and invalid UTF-8 rather than allowing spoofed or ambiguous metadata.
func TestWaylandForeignToplevelRejectsMalformedStringsWithoutLosingValidMetadata(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"embedded NUL", encodeWaylandTestString("Trusted\x00Forged")},
		{"invalid UTF8", []byte{3, 0, 0, 0, 0xff, 0xfe, 0, 0}},
		{"missing terminator", []byte{3, 0, 0, 0, 'b', 'a', 'd', 0xff}},
		{"short packet", []byte{1, 2}},
	}
	for _, proto := range []struct {
		name                   string
		ext                    bool
		titleOpcode, appOpcode uint32
	}{{"wlr", false, 0, 1}, {"ext", true, 2, 3}} {
		for _, tc := range cases {
			t.Run(proto.name+"/"+tc.name, func(t *testing.T) {
				m := &WaylandWindowManager{toplevels: map[uint32]*Info{}}
				info := &Info{Title: "Existing title", AppID: "existing.app"}
				m.toplevels[77] = info
				deliver := func(op uint32) {
					if proto.ext {
						m.handleExtToplevelEvent(77, info, op, tc.payload)
					} else {
						m.handleWLRToplevelEvent(77, info, op, tc.payload)
					}
				}
				deliver(proto.titleOpcode)
				deliver(proto.appOpcode)
				if info.Title != "Existing title" || info.AppID != "existing.app" {
					t.Fatalf("invalid metadata changed to title=%q app=%q", info.Title, info.AppID)
				}
			})
		}
	}
}

func TestWaylandForeignToplevelAcceptsEmptyStringAndUnicodeNames(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"", ""}, {"Café 日本", "Café 日本"}} {
		payload := encodeWaylandTestString(tc.input)
		value, valid := parseWaylandString(payload)
		if !valid || value != tc.want {
			t.Errorf("parse valid title %q: %q valid=%v", tc.input, value, valid)
		}
	}
}

type waylandTransportFailureDisplay struct {
	*mockWaylandDisplay
	failure error
	calls   int
}

func (d *waylandTransportFailureDisplay) RoundTripContext(ctx context.Context) error {
	d.calls++
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.failure
}

// On a lost compositor transport, cached window identity is not authorization
// to mutate a stale handle. Discovery must also refuse stale window details.
func TestWaylandTransportLossPreventsEveryWindowCommandAndStaleRead(t *testing.T) {
	failure := errors.New("synthetic compositor transport failure")
	for _, tc := range []struct {
		name string
		run  func(*WaylandWindowManager) error
	}{
		{"activate", func(m *WaylandWindowManager) error { return m.ActivateByID(context.Background(), "50") }},
		{"close", func(m *WaylandWindowManager) error { return m.CloseWindowByID(context.Background(), "50") }},
		{"minimize", func(m *WaylandWindowManager) error { return m.MinimizeByID(context.Background(), "50") }},
		{"maximize", func(m *WaylandWindowManager) error { return m.MaximizeByID(context.Background(), "50") }},
		{"fullscreen", func(m *WaylandWindowManager) error { return m.FullscreenByID(context.Background(), "50") }},
		{"unfullscreen", func(m *WaylandWindowManager) error { return m.UnfullscreenByID(context.Background(), "50") }},
		{"restore", func(m *WaylandWindowManager) error { return m.RestoreByID(context.Background(), "50") }},
		{"window info", func(m *WaylandWindowManager) error {
			info, err := m.InfoByID(context.Background(), "50")
			if info.Title != "" {
				return errors.New("stale window metadata exposed")
			}
			return err
		}},
		{"active title", func(m *WaylandWindowManager) error {
			title, err := m.ActiveTitle(context.Background())
			if title != "" {
				return errors.New("stale active title exposed")
			}
			return err
		}},
		{"list windows", func(m *WaylandWindowManager) error {
			wins, err := m.List(context.Background())
			if len(wins) > 0 {
				return errors.New("stale window listing exposed")
			}
			return err
		}},
		{"sync", func(m *WaylandWindowManager) error { return m.Sync(context.Background()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, wire, handle := newStubWaylandManager("private-title", true, true)
			m.toplevels[handle].Active = true
			d := &waylandTransportFailureDisplay{mockWaylandDisplay: &mockWaylandDisplay{ctx: wire}, failure: failure}
			m.display = d
			err := tc.run(m)
			if !errors.Is(err, failure) {
				t.Fatalf("lost compositor was not reported: %v", err)
			}
			if d.calls != 1 {
				t.Fatalf("transport queried %d times, want exactly once", d.calls)
			}
			if sawAnyHandleRequest(wire, handle) {
				t.Fatalf("lost transport emitted control request: %+v", wire.sentMsgs)
			}
			if m.toplevels[handle].Title != "private-title" {
				t.Fatalf("failed query mutated cached state: %+v", m.toplevels[handle])
			}
		})
	}
}

func TestWaylandCanceledOperationsNeverQueryCompositorOrUseCachedHandles(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*WaylandWindowManager, context.Context) error
	}{
		{"activate", func(m *WaylandWindowManager, ctx context.Context) error { return m.ActivateByID(ctx, "50") }},
		{"close", func(m *WaylandWindowManager, ctx context.Context) error { return m.CloseWindowByID(ctx, "50") }},
		{"minimize", func(m *WaylandWindowManager, ctx context.Context) error { return m.MinimizeByID(ctx, "50") }},
		{"maximize", func(m *WaylandWindowManager, ctx context.Context) error { return m.MaximizeByID(ctx, "50") }},
		{"fullscreen", func(m *WaylandWindowManager, ctx context.Context) error { return m.FullscreenByID(ctx, "50") }},
		{"unfullscreen", func(m *WaylandWindowManager, ctx context.Context) error { return m.UnfullscreenByID(ctx, "50") }},
		{"restore", func(m *WaylandWindowManager, ctx context.Context) error { return m.RestoreByID(ctx, "50") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, wire, handle := newStubWaylandManager("private", true, true)
			d := &waylandTransportFailureDisplay{mockWaylandDisplay: &mockWaylandDisplay{ctx: wire}, failure: errors.New("should not round-trip")}
			m.display = d
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := tc.run(m, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled call returned: %v", err)
			}
			if d.calls != 0 || sawAnyHandleRequest(wire, handle) {
				t.Fatalf("canceled call reached compositor: roundtrips=%d writes=%d", d.calls, len(wire.sentMsgs))
			}
		})
	}
}

// Context/capability preflights do not assume a live transport, and a nil
// backend must remain safe to probe and release during teardown.
func TestWaylandUnavailableManagerAndCanceledDiscoveryFailClosed(t *testing.T) { //nolint:gocyclo // One teardown contract checks independent nil, canceled, and closed entrypoints.
	var missing *WaylandWindowManager
	if err := missing.sendHandleRequest(context.Background(), 50, 5, nil); err == nil {
		t.Fatal("nil backend sent protocol frame")
	}
	if err := missing.withOperation(context.Background(), func() error { return nil }); err == nil {
		t.Fatal("nil backend admitted protocol operation")
	}
	if err := missing.Close(); err != nil {
		t.Fatalf("nil close: %v", err)
	}
	if operations := missing.SupportedOperations(); len(operations) != 0 {
		t.Fatalf("nil manager advertised control: %v", operations)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if wm, err := NewWaylandWindowManagerForSocketContext(ctx, "/nonexistent/wayland"); wm != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Wayland startup: %v, %v", wm, err)
	}
	m, _, _ := newStubWaylandManager("private", true, true)
	if wins, err := m.List(ctx); len(wins) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled discovery exposed windows: %v %v", wins, err)
	}
	if title, err := m.ActiveTitle(ctx); title != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled title lookup exposed data: %q %v", title, err)
	}
	if info, err := m.InfoByID(ctx, "50"); info.Title != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled info exposed state: %+v %v", info, err)
	}
	if err := m.Sync(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled sync: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.withOperation(context.Background(), func() error { return nil }); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed manager admitted work: %v", err)
	}
}

// A compositor may return multiple windows while the caller cancels after
// processing one. The remaining snapshot must not be exposed as live state.
func TestWaylandIteratorCancellationAfterFirstWindowFailsClosed(t *testing.T) {
	mgr, _, _ := newStubWaylandManager("first", true, false)
	mgr.toplevels[51] = &Info{ID: 51, Title: "second"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := 0
	gotCancellation := false
	for win, err := range mgr.IterateWindows(ctx) {
		if errors.Is(err, context.Canceled) {
			if win.ID != 0 {
				t.Fatalf("canceled iteration exposed window %#v", win)
			}
			gotCancellation = true
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		seen++
		if seen > 1 {
			t.Fatalf("iterator leaked %d windows after cancellation", seen)
		}
		cancel()
	}
	if seen != 1 || !gotCancellation {
		t.Fatalf("got %d windows and cancellation=%v, want one then cancellation", seen, gotCancellation)
	}
}

// Cancellation during the sync boundary must be checked before publishing
// compositor data, not only before issuing the round-trip.
func TestWaylandIteratorCancellationDuringRoundTripDoesNotPublishWindows(t *testing.T) {
	mgr, _, _ := newStubWaylandManager("private-window", true, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// RoundTripContext returns nil after cancel to simulate a transport that
	// completes at the same instant the request is revoked.
	mgr.display = &cancelAfterSuccessfulWindowRoundTrip{display: mgr.display, cancel: cancel}
	seen := 0
	for win, err := range mgr.IterateWindows(ctx) {
		seen++
		if !errors.Is(err, context.Canceled) || win.ID != 0 {
			t.Fatalf("canceled round-trip exposed %#v, %v", win, err)
		}
	}
	if seen != 1 {
		t.Fatalf("got %d results, want one cancellation error", seen)
	}
}

type cancelAfterSuccessfulWindowRoundTrip struct {
	display interface {
		Context() wl.Ctx
		GetRegistry() (*wl.Registry, error)
		RoundTripContext(context.Context) error
	}
	cancel context.CancelFunc
}

func (d *cancelAfterSuccessfulWindowRoundTrip) Context() wl.Ctx { return d.display.Context() }
func (d *cancelAfterSuccessfulWindowRoundTrip) GetRegistry() (*wl.Registry, error) {
	return d.display.GetRegistry()
}
func (d *cancelAfterSuccessfulWindowRoundTrip) RoundTripContext(ctx context.Context) error {
	if err := d.display.RoundTripContext(ctx); err != nil {
		return err
	}
	d.cancel()
	return nil
}

// A rejected foreign-toplevel setup is not an active subscription. It must
// not leave a manager proxy in the Wayland object registry after failure.
func TestWaylandFailedForeignToplevelSetupUnregistersManagerProxy(t *testing.T) {
	bindFailure := errors.New("synthetic manager bind failure")
	roundTripFailure := errors.New("synthetic transport failure")
	for _, tc := range []struct {
		name      string
		configure func(*WaylandWindowManager, *mockWaylandContext)
		wantErr   error
	}{
		{"no protocol", func(m *WaylandWindowManager, _ *mockWaylandContext) { m.wlrMgrID = 0; m.extMgrID = 0 }, nil},
		{"manager bind failed", func(m *WaylandWindowManager, _ *mockWaylandContext) {
			registry, ok := m.registry.(*mockRegistry)
			if !ok {
				t.Fatalf("registry fixture type = %T", m.registry)
			}
			m.registry = &failingWaylandRegistry{mockRegistry: registry, err: bindFailure}
		}, bindFailure},
		{"round trip failed", func(_ *WaylandWindowManager, c *mockWaylandContext) {
			c.dispatchFn = func() error { return roundTripFailure }
		}, roundTripFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, wire, _ := newStubWaylandManager("", true, false)
			tc.configure(m, wire)
			managerProxyID := wire.nextID + 1
			previous := make(map[uint32]wl.Proxy, len(wire.objects))
			for id, proxy := range wire.objects {
				previous[id] = proxy
			}
			if err := m.fetchToplevels(context.Background()); err == nil {
				t.Fatal("broken setup unexpectedly succeeded")
			} else if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("setup error = %v, want wrapped %v", err, tc.wantErr)
			}
			if _, leaked := wire.objects[managerProxyID]; leaked {
				t.Fatalf("failed setup leaked foreign-toplevel manager proxy %d", managerProxyID)
			}
			for id, proxy := range previous {
				if wire.objects[id] != proxy {
					t.Fatalf("failed setup replaced preexisting Wayland object %d", id)
				}
			}
		})
	}
}

type failingWaylandRegistry struct {
	*mockRegistry
	err error
}

func (r *failingWaylandRegistry) BindContext(context.Context, uint32, string, uint32, uint32) error {
	return r.err
}

type cancelAfterRoundTripWaylandDisplay struct {
	*mockWaylandDisplay
	cancel context.CancelFunc
}

func (d *cancelAfterRoundTripWaylandDisplay) RoundTripContext(ctx context.Context) error {
	if err := d.mockWaylandDisplay.RoundTripContext(ctx); err != nil {
		return err
	}
	d.cancel()
	return nil
}

type cancelAfterHandleRequestWaylandContext struct {
	*mockWaylandContext
	cancel context.CancelFunc
	handle uint32
	opcode uint32
}

func (c *cancelAfterHandleRequestWaylandContext) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := c.mockWaylandContext.WriteMsgContext(ctx, data, oob); err != nil {
		return err
	}
	if len(data) >= 8 && wl.Uint32(data[:4]) == c.handle && wl.Uint32(data[4:8])&0xffff == c.opcode {
		c.cancel()
	}
	return nil
}

type failWaylandHandleRequestContext struct {
	*mockWaylandContext
	failure error
	handle  uint32
	opcode  uint32
}

func (c *failWaylandHandleRequestContext) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if len(data) >= 8 && wl.Uint32(data[:4]) == c.handle && wl.Uint32(data[4:8])&0xffff == c.opcode {
		return c.failure
	}
	return c.mockWaylandContext.WriteMsgContext(ctx, data, oob)
}

type waylandCancellationContextDisplay struct {
	*mockWaylandDisplay
	ctx wl.Ctx
}

func (d *waylandCancellationContextDisplay) Context() wl.Ctx { return d.ctx }

type rejectingWaylandSeatRegistry struct {
	*mockRegistry
	failure     error
	seatProxyID uint32
}

func (r *rejectingWaylandSeatRegistry) BindContext(ctx context.Context, name uint32, iface string, version, proxyID uint32) error {
	if iface == "wl_seat" {
		r.seatProxyID = proxyID
		return r.failure
	}
	return r.mockRegistry.BindContext(ctx, name, iface, version, proxyID)
}

func TestWaylandMutationsStopWhenCallerCancelsAfterRoundTrip(t *testing.T) {
	mutations := []struct {
		name string
		run  func(*WaylandWindowManager, context.Context) error
	}{
		{"activate", func(m *WaylandWindowManager, ctx context.Context) error { return m.ActivateByID(ctx, "50") }},
		{"close", func(m *WaylandWindowManager, ctx context.Context) error { return m.CloseWindowByID(ctx, "50") }},
		{"minimize", func(m *WaylandWindowManager, ctx context.Context) error { return m.MinimizeByID(ctx, "50") }},
		{"maximize", func(m *WaylandWindowManager, ctx context.Context) error { return m.MaximizeByID(ctx, "50") }},
		{"fullscreen", func(m *WaylandWindowManager, ctx context.Context) error { return m.FullscreenByID(ctx, "50") }},
		{"unfullscreen", func(m *WaylandWindowManager, ctx context.Context) error { return m.UnfullscreenByID(ctx, "50") }},
		{"restore", func(m *WaylandWindowManager, ctx context.Context) error { return m.RestoreByID(ctx, "50") }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			manager, wire, handle := newStubWaylandManager("private", true, true)
			t.Cleanup(func() { _ = manager.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			display, ok := manager.display.(*mockWaylandDisplay)
			if !ok {
				t.Fatalf("unexpected test display %T", manager.display)
			}
			manager.display = &cancelAfterRoundTripWaylandDisplay{
				mockWaylandDisplay: display,
				cancel:             cancel,
			}

			if err := mutation.run(manager, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("mutation error = %v, want context.Canceled", err)
			}
			if sawAnyHandleRequest(wire, handle) {
				t.Fatalf("mutation sent a handle request after caller cancellation: %v", wire.sentMsgs)
			}
		})
	}
}

func TestWaylandRestoreStopsBeforeSecondRequestAfterCancellation(t *testing.T) {
	manager, wire, handle := newStubWaylandManager("private", true, true)
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	display, ok := manager.display.(*mockWaylandDisplay)
	if !ok {
		t.Fatalf("unexpected test display %T", manager.display)
	}
	manager.display = &waylandCancellationContextDisplay{
		mockWaylandDisplay: display,
		ctx: &cancelAfterHandleRequestWaylandContext{
			mockWaylandContext: wire,
			cancel:             cancel,
			handle:             handle,
			opcode:             1,
		},
	}

	err := manager.RestoreByID(ctx, "50")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RestoreByID error = %v, want context.Canceled", err)
	}
	if !sawHandleRequest(wire, handle, 1) {
		t.Fatal("RestoreByID did not send its first request before cancellation")
	}
	if sawHandleRequest(wire, handle, 3) {
		t.Fatal("RestoreByID sent its second request after cancellation")
	}
}

func TestWaylandRestoreStopsAfterFirstRequestTransportFailure(t *testing.T) {
	manager, wire, handle := newStubWaylandManager("private", true, true)
	t.Cleanup(func() { _ = manager.Close() })
	failure := errors.New("synthetic restore transport failure")
	display, ok := manager.display.(*mockWaylandDisplay)
	if !ok {
		t.Fatalf("unexpected test display %T", manager.display)
	}
	manager.display = &waylandCancellationContextDisplay{
		mockWaylandDisplay: display,
		ctx: &failWaylandHandleRequestContext{
			mockWaylandContext: wire,
			failure:            failure,
			handle:             handle,
			opcode:             1,
		},
	}

	err := manager.RestoreByID(context.Background(), "50")
	if !errors.Is(err, failure) {
		t.Fatalf("RestoreByID error = %v, want first-request transport failure", err)
	}
	if sawAnyHandleRequest(wire, handle) {
		t.Fatalf("RestoreByID sent a request after its first request failed: %v", wire.sentMsgs)
	}
}

func TestWaylandActivationSeatBindFailureDoesNotLeakProxy(t *testing.T) {
	manager, wire, handle := newStubWaylandManager("private", true, false)
	t.Cleanup(func() { _ = manager.Close() })
	manager.seatID = 7
	failure := errors.New("synthetic seat bind failure")
	originalRegistry, ok := manager.registry.(*mockRegistry)
	if !ok {
		t.Fatalf("unexpected test registry %T", manager.registry)
	}
	registry := &rejectingWaylandSeatRegistry{
		mockRegistry: originalRegistry,
		failure:      failure,
	}
	manager.registry = registry

	err := manager.ActivateByID(context.Background(), "50")
	if !errors.Is(err, failure) {
		t.Fatalf("ActivateByID error = %v, want seat bind failure", err)
	}
	if registry.seatProxyID == 0 || wire.objects[registry.seatProxyID] != nil {
		t.Fatalf("failed seat bind leaked proxy %d into the Wayland object registry", registry.seatProxyID)
	}
	if sawAnyHandleRequest(wire, handle) {
		t.Fatalf("ActivateByID sent a handle request after seat binding failed: %v", wire.sentMsgs)
	}
}

func TestWaylandV1ManagerRejectsFullscreenMutations(t *testing.T) {
	mutations := []struct {
		name string
		run  func(*WaylandWindowManager) error
	}{
		{"fullscreen", func(m *WaylandWindowManager) error { return m.FullscreenByID(context.Background(), "50") }},
		{"unfullscreen", func(m *WaylandWindowManager) error { return m.UnfullscreenByID(context.Background(), "50") }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			manager, wire, handle := newStubWaylandManager("private", true, true)
			t.Cleanup(func() { _ = manager.Close() })
			manager.wlrMgrVersion = 1

			if err := mutation.run(manager); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("v1 %s error = %v, want ErrNotSupported", mutation.name, err)
			}
			if sawAnyHandleRequest(wire, handle) {
				t.Fatalf("v1 manager sent unsupported %s request: %v", mutation.name, wire.sentMsgs)
			}
		})
	}
}

func TestWaylandProtocolSyntheticLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name             string
		wlr              bool
		titleOp, closeOp uint32
	}{
		{"wlr", true, 0, 6}, {"ext", false, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wm, wire, _ := newStubWaylandManager("", tc.wlr, false)
			if tc.wlr {
				wm.wlrMgrVersion = 3
			}
			created := false
			wire.dispatchFn = func() error {
				if created {
					return nil
				}
				created = true
				var manager *wl.RawProxy
				for _, proxy := range wire.objects {
					if p, ok := proxy.(*wl.RawProxy); ok {
						manager = p
						break
					}
				}
				if manager == nil {
					t.Fatal("manager proxy not registered")
				}
				var id [4]byte
				wl.PutUint32(id[:], 77)
				manager.Dispatch(0, -1, id[:])
				proxy, ok := wire.objects[77].(*wl.RawProxy)
				if !ok {
					t.Fatalf("missing toplevel handle: %v", wire.objects)
				}
				data := append(make([]byte, 4), []byte("Editor\x00")...)
				wl.PutUint32(data[:4], 7)
				proxy.Dispatch(tc.titleOp, -1, data)
				return nil
			}
			if err := wm.fetchToplevels(context.Background()); err != nil {
				t.Fatal(err)
			}
			wins, err := wm.List(context.Background())
			if err != nil || len(wins) != 1 || wins[0].Title != "Editor" || wins[0].ID != 77 {
				t.Fatalf("window lifecycle: %+v err=%v", wins, err)
			}
			p, ok := wire.objects[77].(*wl.RawProxy)
			if !ok {
				t.Fatalf("unexpected Wayland proxy %T", wire.objects[77])
			}
			p.Dispatch(tc.closeOp, -1, nil)
			wins, err = wm.List(context.Background())
			if err != nil || len(wins) != 0 {
				t.Fatalf("close failed %+v err=%v", wins, err)
			}
		})
	}
}

func TestWaylandSyntheticControlAndErrorBoundaries(t *testing.T) {
	wm, wire, id := newStubWaylandManager("Editor", true, true)
	key := "50"
	for _, tc := range []struct {
		name   string
		op     func(context.Context, string) error
		opcode uint32
	}{
		{"restore", wm.RestoreByID, 1}, {"close", wm.CloseWindowByID, 5}, {"minimize", wm.MinimizeByID, 2},
		{"maximize", wm.MaximizeByID, 0}, {"fullscreen", wm.FullscreenByID, 7}, {"unfullscreen", wm.UnfullscreenByID, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(wire.sentMsgs)
			if err := tc.op(context.Background(), key); err != nil {
				t.Fatal(err)
			}
			if !sawHandleRequest(wire, id, tc.opcode) || len(wire.sentMsgs) <= before {
				t.Fatalf("no correct protocol request for %s", tc.name)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			before = len(wire.sentMsgs)
			if err := tc.op(ctx, key); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel got %v", err)
			}
			if len(wire.sentMsgs) != before {
				t.Fatal("canceled operation wrote to compositor")
			}
			if err := tc.op(context.Background(), "missing"); !errors.Is(err, ErrWindowNotFound) {
				t.Fatalf("unknown handle got %v", err)
			}
		})
	}
	if info, err := wm.InfoByID(context.Background(), key); err != nil || info.Title != "Editor" {
		t.Fatalf("InfoByID %+v %v", info, err)
	}
	if err := wm.MoveByID(context.Background(), key, 1, 1); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("MoveByID=%v", err)
	}
	if err := wm.ResizeByID(context.Background(), key, 1, 1); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("ResizeByID=%v", err)
	}
	if title, err := wm.ActiveTitle(context.Background()); !errors.Is(err, ErrNotSupported) || title != "" {
		t.Fatalf("no active title %q, %v", title, err)
	}
	wm.toplevels[id].Active = true
	if title, err := wm.ActiveTitle(context.Background()); err != nil || title != "Editor" {
		t.Fatalf("active title %q %v", title, err)
	}
	if ops := strings.Join(wm.SupportedOperations(), ","); !strings.Contains(ops, "fullscreen") || !strings.Contains(ops, "activate") {
		t.Fatalf("control caps %s", ops)
	}
}

func TestWaylandForeignToplevelStateAndMalformedEventBoundaries(t *testing.T) {
	wm, _, _ := newStubWaylandManager("", true, false)
	info := &Info{ID: 77, Title: "original", AppID: "old"}
	wm.toplevels[77] = info
	state := make([]byte, 20)
	wl.PutUint32(state, 16)
	for i, v := range []uint32{0, 1, 2, 3} {
		wl.PutUint32(state[4+i*4:], v)
	}
	wm.handleWLRToplevelEvent(77, info, 4, state)
	if !info.Maximized || !info.Minimized || !info.Active || !info.Fullscreen {
		t.Fatalf("all four state bits expected: %+v", info)
	}
	malformed := []byte{0xff, 0xff, 0xff, 0xff}
	wm.handleWLRToplevelEvent(77, info, 4, malformed)
	if !info.Maximized || !info.Minimized || !info.Active || !info.Fullscreen {
		t.Fatalf("malformed state changed previous state: %+v", info)
	}
	wm.handleWLRToplevelEvent(77, info, 4, []byte{0, 0, 0, 0})
	if info.Maximized || info.Minimized || info.Active || info.Fullscreen {
		t.Fatalf("zero state did not clear flags: %+v", info)
	}
	wm.handleWLRToplevelEvent(77, info, 0, []byte{8, 0, 0, 0, 'b', 'a', 'd'})
	if info.Title != "original" {
		t.Fatalf("malformed title accepted: %q", info.Title)
	}
	data := append([]byte{4, 0, 0, 0}, []byte{'n', 'e', 'w', 0}...)
	wm.handleWLRToplevelEvent(77, info, 1, data)
	if info.AppID != "new" {
		t.Fatalf("app id not updated: %q", info.AppID)
	}
	wm.handleWLRToplevelEvent(77, info, 6, nil)
	if len(wm.toplevels) != 0 {
		t.Fatal("closed window remained accessible")
	}
	changes := wm.WindowChanges()
	if changes == nil {
		t.Fatal("window change notification channel missing")
	}
	select {
	case <-changes:
	default:
		t.Fatal("change not signaled")
	}
	select {
	case <-changes:
		t.Fatal("coalesced notification delivered twice")
	default:
	}
	wm.notifyWindowChange()
	select {
	case <-changes:
	default:
		t.Fatal("new change was not signaled")
	}
	if err := wm.Close(); err != nil {
		t.Fatal(err)
	}
	wm.notifyWindowChange()
	select {
	case _, open := <-changes:
		if open {
			t.Fatal("closed backend delivered spurious change")
		}
	default:
		t.Fatal("closed backend left a live change stream")
	}
}

func TestWaylandExtToplevelStableIdentifierAndMalformedUpdates(t *testing.T) {
	wm, _, _ := newStubWaylandManager("", false, false)
	info := &Info{ID: 55, NativeID: "old"}
	wm.toplevels[55] = info
	wm.handleExtToplevelEvent(55, info, 4, []byte{4, 0, 0, 0, 'n', 'e', 'w', 0})
	if info.NativeID != "new" {
		t.Fatalf("stable identifier %q", info.NativeID)
	}
	wm.handleExtToplevelEvent(55, info, 4, []byte{10, 0, 0, 0, 'b', 'a', 'd'})
	if info.NativeID != "new" {
		t.Fatalf("malformed ID overwrote good identifier: %q", info.NativeID)
	}
	wm.handleExtToplevelEvent(55, info, 3, []byte{4, 0, 0, 0, 'a', 'p', 'p', 0})
	if info.AppID != "app" {
		t.Fatalf("application ID %q", info.AppID)
	}
	wm.handleExtToplevelEvent(55, info, 0, nil)
	if len(wm.toplevels) != 0 {
		t.Fatal("ext closed window remained discoverable")
	}
}

// Mock Wayland Context and Display for testing.
type mockWaylandContext struct {
	closeCount int
	objects    map[uint32]wl.Proxy
	nextID     uint32
	sentMsgs   [][]byte
	sentOOBs   [][]byte
	dispatchFn func() error
}

func (m *mockWaylandContext) Register(p wl.Proxy) {
	m.nextID++
	p.SetID(m.nextID)
	p.SetCtx(m)
	m.objects[m.nextID] = p
}

func (m *mockWaylandContext) SetProxy(id uint32, p wl.Proxy) {
	p.SetID(id)
	p.SetCtx(m)
	m.objects[id] = p
}

func (m *mockWaylandContext) Unregister(p wl.Proxy) {
	delete(m.objects, p.ID())
}

func (m *mockWaylandContext) WriteMsg(data, oob []byte) error {
	m.sentMsgs = append(m.sentMsgs, data)
	m.sentOOBs = append(m.sentOOBs, oob)
	return nil
}

func (m *mockWaylandContext) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.WriteMsg(data, oob)
}

func (m *mockWaylandContext) Dispatch() error {
	if m.dispatchFn != nil {
		return m.dispatchFn()
	}
	return nil // No-op by default
}

func (m *mockWaylandContext) DispatchContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.Dispatch()
}

func (m *mockWaylandContext) WithOperationContext(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}

func (m *mockWaylandContext) Close() error { m.closeCount++; return nil }
func (m *mockWaylandContext) ID() uint32   { return 1 } // Mock display ID

type mockWaylandDisplay struct{ ctx *mockWaylandContext }

func (d *mockWaylandDisplay) Context() wl.Ctx { return d.ctx }
func (d *mockWaylandDisplay) GetRegistry() (*wl.Registry, error) {
	reg := &wl.Registry{}
	d.ctx.Register(reg)
	// Mock the wl_display.get_registry call message
	// (sender ID, size/opcode, newID)
	var buf []byte
	buf = put32(buf, 1)          // wl_display ID
	buf = put32(buf, (12<<16)|1) // size=12, opcode=1 (get_registry)
	buf = put32(buf, reg.ID())
	if err := d.ctx.WriteMsg(buf, nil); err != nil {
		return nil, err
	}
	return reg, nil
}
func (d *mockWaylandDisplay) Sync() (*wl.Callback, error) {
	cb := &wl.Callback{}
	d.ctx.Register(cb)
	// Mock the wl_display.sync call message
	var buf []byte
	buf = put32(buf, 1)          // wl_display ID
	buf = put32(buf, (12 << 16)) // size=12, opcode=0 (sync)
	buf = put32(buf, cb.ID())
	if err := d.ctx.WriteMsg(buf, nil); err != nil {
		return nil, err
	}
	return cb, nil
}
func (d *mockWaylandDisplay) RoundTripContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Mock RoundTrip: simulate a done event immediately
	cb, err := d.Sync()
	if err != nil {
		return err
	}
	cb.SetDoneHandler(func() {
		// Simulate dispatching a done event for the callback
		cb.Dispatch(0, -1, nil) // opcode 0 for done event
	})
	return d.ctx.Dispatch() // Process the sync and done
}

type blockingWaylandDisplay struct {
	*mockWaylandDisplay
	started chan struct{}
}

func (d *blockingWaylandDisplay) RoundTripContext(ctx context.Context) error {
	close(d.started)
	<-ctx.Done()
	return ctx.Err()
}

type sequencedWaylandDisplay struct {
	*mockWaylandDisplay
	started chan struct{}
	calls   int
}

func (d *sequencedWaylandDisplay) RoundTripContext(ctx context.Context) error {
	d.calls++
	if d.calls == 1 || d.calls >= 3 {
		return nil
	}
	close(d.started)
	<-ctx.Done()
	return ctx.Err()
}

func put32(buf []byte, v uint32) []byte {
	var tmp [4]byte
	wl.PutUint32(tmp[:], v)
	return append(buf, tmp[:]...)
}

// Mock for wl.RawProxy and related structs for testing window manager methods.
type mockRawProxy struct {
	wl.BaseProxy
	OnEventMock func(opcode uint32, fd int, data []byte)
}

func (p *mockRawProxy) Dispatch(opcode uint32, fd int, data []byte) {
	if p.OnEventMock != nil {
		p.OnEventMock(opcode, fd, data)
	}
}

func (p *mockRawProxy) SetCtx(c wl.Ctx) { p.BaseProxy.SetCtx(c) }
func (p *mockRawProxy) ID() uint32      { return p.BaseProxy.ID() }
func (p *mockRawProxy) SetID(id uint32) { p.BaseProxy.SetID(id) }

// Mock for the wl.Registry SetGlobalHandler
type mockRegistry struct {
	wl.BaseProxy
	globalHandler func(wl.GlobalEvent)
}

func (r *mockRegistry) SetGlobalHandler(f func(wl.GlobalEvent)) { r.globalHandler = f }
func (r *mockRegistry) Bind(name uint32, iface string, ver, newID uint32) error {
	// No-op for mock: tests simulate compositor responses via ctx.dispatchFn.
	return nil
}
func (r *mockRegistry) BindContext(ctx context.Context, name uint32, iface string, ver, newID uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.Bind(name, iface, ver, newID)
}
func (r *mockRegistry) Dispatch(opcode uint32, fd int, data []byte) {
	if opcode != 0 || r.globalHandler == nil || len(data) < 8 {
		return
	}
	ev := wl.GlobalEvent{Name: wl.Uint32(data[0:4])}
	slen := int(wl.Uint32(data[4:8]))
	if slen > 0 && 8+slen <= len(data) {
		ev.Interface = string(data[8 : 8+slen-1])
	}
	padded := (slen + 3) &^ 3
	if off := 8 + padded; off+4 <= len(data) {
		ev.Version = wl.Uint32(data[off : off+4])
	}
	r.globalHandler(ev)
}

func findWindowHandleByTitle(wm *WaylandWindowManager, title string) (*Info, error) {
	titleLower := strings.ToLower(title)
	for _, v := range wm.toplevels {
		if strings.Contains(strings.ToLower(v.Title), titleLower) {
			return v, nil
		}
	}
	return nil, fmt.Errorf("window matching %q not found", title)
}

func TestWaylandWindowManager_New(t *testing.T) {
	originalSocketPath := os.Getenv("WAYLAND_DISPLAY")
	os.Unsetenv("WAYLAND_DISPLAY")
	defer os.Setenv("WAYLAND_DISPLAY", originalSocketPath)

	wm, err := NewWaylandWindowManagerForSocket("")
	if err == nil || !strings.Contains(err.Error(), "window/wayland: WAYLAND_DISPLAY not set") {
		t.Fatalf("NewWaylandWindowManagerForSocket() error = %v", err)
	}
	if wm != nil {
		t.Fatal("expected nil manager when socket is empty")
	}
}

func TestWaylandWindowManagerRejectsOperationsAfterClose(t *testing.T) {
	wm, _, _ := newStubWaylandManager("Closed", true, true)
	if err := wm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := wm.Sync(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Sync error = %v, want net.ErrClosed", err)
	}
	if _, err := wm.List(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("List error = %v, want net.ErrClosed", err)
	}
}

func TestWaylandWindowManagerRejectsInvalidHandleRequests(t *testing.T) {
	wm, ctx, _ := newStubWaylandManager("Request", true, true)
	initialWrites := len(ctx.sentMsgs)

	if err := wm.sendHandleRequest(context.Background(), 50, 0x10000, nil); err == nil {
		t.Fatal("sendHandleRequest accepted an oversized opcode")
	}
	if err := wm.sendHandleRequest(context.Background(), 50, 0, make([]byte, 1<<16-7)); err == nil {
		t.Fatal("sendHandleRequest accepted an oversized payload")
	}
	if got := len(ctx.sentMsgs); got != initialWrites {
		t.Fatalf("invalid requests wrote %d messages, want no additional messages", got-initialWrites)
	}
}

func newStubWaylandManager(title string, controlProtocol bool, withSeat bool) (*WaylandWindowManager, *mockWaylandContext, uint32) {
	ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy), nextID: 0}
	display := &mockWaylandDisplay{ctx: ctx}
	registry := &mockRegistry{}
	ctx.Register(registry)
	ctx.dispatchFn = func() error { return nil }

	handleID := uint32(50)
	wm := &WaylandWindowManager{
		display:   display,
		registry:  registry,
		toplevels: make(map[uint32]*Info),
	}
	if controlProtocol {
		wm.wlrMgrID = 1
	} else {
		wm.extMgrID = 1
	}
	if title != "" {
		wm.toplevels[handleID] = &Info{ID: uint64(handleID), Title: title}
	}
	if withSeat {
		seat := &wl.RawProxy{}
		ctx.SetProxy(60, seat)
		wm.seat = seat
		wm.seatID = 1
	}
	return wm, ctx, handleID
}

func sawHandleRequest(ctx *mockWaylandContext, handleID uint32, opcode uint32) bool {
	for _, msg := range ctx.sentMsgs {
		if len(msg) < 8 {
			continue
		}
		sender := wl.Uint32(msg[0:4])
		sizeOpcode := wl.Uint32(msg[4:8])
		if sender == handleID && sizeOpcode&0xffff == opcode {
			return true
		}
	}
	return false
}

func sawAnyHandleRequest(ctx *mockWaylandContext, handleID uint32) bool {
	for _, msg := range ctx.sentMsgs {
		if len(msg) < 8 {
			continue
		}
		if wl.Uint32(msg[0:4]) == handleID {
			return true
		}
	}
	return false
}

func TestWaylandWindowManager_ActivateByID(t *testing.T) {
	testCases := []struct {
		name          string
		windowID      string
		windowExists  bool
		expectedCmd   string
		expectedError string
	}{
		{
			name:         "Window exists",
			windowID:     "50",
			windowExists: true,
			expectedCmd:  "activate", // Check for the presence of the activate request
		},
		{
			name:          "Window does not exist",
			windowID:      "999",
			windowExists:  false,
			expectedError: ErrWindowNotFound.Error(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			title := ""
			if tc.windowExists {
				title = "Test Window"
			}
			wm, ctx, handleID := newStubWaylandManager(title, true, tc.windowExists)

			err := wm.ActivateByID(context.Background(), tc.windowID)

			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("ActivateByID(%q) error = %v, expected error containing %q", tc.windowID, err, tc.expectedError)
				}
			} else if err != nil {
				t.Errorf("ActivateByID(%q) unexpected error: %v", tc.windowID, err)
			}

			// Check if the correct message was sent
			if tc.expectedCmd != "" && tc.expectedError == "" {
				if !sawHandleRequest(ctx, handleID, 4) {
					t.Errorf("ActivateByID(%q) did not send the expected activate request", tc.windowID)
				}
			}
		})
	}
}

func runWindowActionTest(
	t *testing.T,
	actionName string,
	existingTitle string,
	expectedOpcode uint32,
	action func(*WaylandWindowManager, context.Context, string) error,
) {
	t.Helper()
	testCases := []struct {
		name          string
		windowID      string
		windowExists  bool
		expectedError string
	}{
		{
			name:         "Window exists",
			windowID:     "50",
			windowExists: true,
		},
		{
			name:          "Window does not exist",
			windowID:      "999",
			windowExists:  false,
			expectedError: ErrWindowNotFound.Error(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			title := ""
			if tc.windowExists {
				title = existingTitle
			}
			wm, ctx, handleID := newStubWaylandManager(title, true, false)

			err := action(wm, context.Background(), tc.windowID)

			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("%s(%q) error = %v, expected error containing %q", actionName, tc.windowID, err, tc.expectedError)
				}
			} else if err != nil {
				t.Errorf("%s(%q) unexpected error: %v", actionName, tc.windowID, err)
			}

			if tc.expectedError == "" {
				if !sawHandleRequest(ctx, handleID, expectedOpcode) {
					t.Errorf("%s(%q) did not send the expected request", actionName, tc.windowID)
				}
			}
		})
	}
}

// TestWaylandWindowManager_CloseWindow tests the CloseWindow method.
func TestWaylandWindowManager_CloseWindowByID(t *testing.T) {
	runWindowActionTest(
		t,
		"CloseWindow",
		"CloseMe",
		5,
		func(wm *WaylandWindowManager, ctx context.Context, id string) error {
			return wm.CloseWindowByID(ctx, id)
		},
	)
}

// TestWaylandWindowManager_Minimize tests the Minimize method.
func TestWaylandWindowManager_MinimizeByID(t *testing.T) {
	runWindowActionTest(
		t,
		"Minimize",
		"MinimizeMe",
		2,
		func(wm *WaylandWindowManager, ctx context.Context, id string) error {
			return wm.MinimizeByID(ctx, id)
		},
	)
}

// TestWaylandWindowManager_Maximize tests the Maximize method.
func TestWaylandWindowManager_MaximizeByID(t *testing.T) {
	runWindowActionTest(
		t,
		"Maximize",
		"MaximizeMe",
		0,
		func(wm *WaylandWindowManager, ctx context.Context, id string) error {
			return wm.MaximizeByID(ctx, id)
		},
	)
}

func TestWaylandWindowManager_FullscreenByID(t *testing.T) {
	runWindowActionTest(
		t,
		"Fullscreen",
		"FullscreenMe",
		7,
		func(wm *WaylandWindowManager, ctx context.Context, id string) error {
			return wm.FullscreenByID(ctx, id)
		},
	)
}

func TestWaylandWindowManager_UnfullscreenByID(t *testing.T) {
	runWindowActionTest(
		t,
		"Unfullscreen",
		"UnfullscreenMe",
		8,
		func(wm *WaylandWindowManager, ctx context.Context, id string) error {
			return wm.UnfullscreenByID(ctx, id)
		},
	)
}

func TestWaylandWindowManagerPrefersWlrootsProtocolForControl(t *testing.T) {
	wm := &WaylandWindowManager{extMgrID: 2, wlrMgrID: 3}
	iface, id, version := wm.foreignToplevelProtocol()
	if iface != "zwlr_foreign_toplevel_manager_v1" || id != 3 || version != 3 {
		t.Fatalf("foreign protocol = (%q, %d, %d), want wlroots id 3 version 3", iface, id, version)
	}

	wm = &WaylandWindowManager{extMgrID: 2}
	iface, id, version = wm.foreignToplevelProtocol()
	if iface != "ext_foreign_toplevel_list_v1" || id != 2 || version != 1 {
		t.Fatalf("list protocol = (%q, %d, %d), want ext id 2 version 1", iface, id, version)
	}
}

func TestWaylandWindowManagerHonorsWlrootsProtocolVersion(t *testing.T) {
	wm := &WaylandWindowManager{wlrMgrID: 7, wlrMgrVersion: 1}
	iface, id, version := wm.foreignToplevelProtocol()
	if iface != "zwlr_foreign_toplevel_manager_v1" || id != 7 || version != 1 {
		t.Fatalf("foreign protocol = (%q, %d, %d), want wlroots id 7 version 1", iface, id, version)
	}
	for _, operation := range wm.SupportedOperations() {
		if operation == "fullscreen" {
			t.Fatal("SupportedOperations advertised fullscreen for wlroots protocol version 1")
		}
	}
}

func TestWaylandWindowManagerRejectsFullscreenOnWlrootsVersionOne(t *testing.T) {
	wm, ctx, _ := newStubWaylandManager("Version one", true, false)
	wm.wlrMgrVersion = 1

	if err := wm.FullscreenByID(context.Background(), "50"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("FullscreenByID error = %v, want ErrNotSupported", err)
	}
	if sawAnyHandleRequest(ctx, 50) {
		t.Fatal("FullscreenByID sent a request to a version-one wlroots handle")
	}
}

// TestWaylandWindowManager_List tests the List method.
func TestWaylandWindowManager_List(t *testing.T) {
	wm, _, _ := newStubWaylandManager("", true, false)
	handleID1 := uint32(50)
	handleID2 := uint32(51)
	wm.toplevels[handleID1] = &Info{ID: uint64(handleID1), Title: "Window One"}
	wm.toplevels[handleID2] = &Info{ID: uint64(handleID2), Title: "Window Two"}

	windows, err := wm.List(context.Background())

	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}

	if len(windows) != 2 {
		t.Errorf("List() expected 2 windows, got %d", len(windows))
	}

	if windows[0].ID != uint64(handleID1) || windows[1].ID != uint64(handleID2) {
		t.Fatalf("List() order = %d, %d; want %d, %d", windows[0].ID, windows[1].ID, handleID1, handleID2)
	}

	// Check contents as well as order so a stable sort cannot hide a missing item.
	found1 := false
	found2 := false
	for _, w := range windows {
		if w.Title == "Window One" && w.ID == uint64(handleID1) {
			found1 = true
		}
		if w.Title == "Window Two" && w.ID == uint64(handleID2) {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Errorf("List() results did not contain expected windows. Found: %+v", windows)
	}
}

func TestDecodeWaylandStringRejectsOversizedLength(t *testing.T) {
	data := []byte{0xff, 0xff, 0xff, 0xff}
	if got := decodeWaylandString(data); got != "" {
		t.Fatalf("decodeWaylandString oversized length = %q, want empty", got)
	}
}

func TestDecodeWaylandStringRejectsMissingTerminator(t *testing.T) {
	data := []byte{3, 0, 0, 0, 'a', 'b', 'c'}
	if got := decodeWaylandString(data); got != "" {
		t.Fatalf("decodeWaylandString unterminated value = %q, want empty", got)
	}

	if got := decodeWaylandString(encodeWaylandTestString("title")); got != "title" {
		t.Fatalf("decodeWaylandString valid value = %q, want title", got)
	}
}

func TestWaylandWindowManager_ListIsDeterministicWithStableIDTies(t *testing.T) {
	wm, _, _ := newStubWaylandManager("", true, false)
	wm.toplevels = map[uint32]*Info{
		2: {ID: 2, NativeID: "shared", Title: "second"},
		1: {ID: 1, NativeID: "shared", Title: "first"},
		3: {ID: 7, NativeID: "shared", Title: "z"},
		4: {ID: 7, NativeID: "shared", Title: "a"},
	}

	for range 20 {
		windows, err := wm.List(context.Background())
		if err != nil {
			t.Fatalf("List() unexpected error: %v", err)
		}
		if len(windows) != 4 {
			t.Fatalf("List() returned %d windows, want 4", len(windows))
		}
		want := []struct {
			id    uint64
			title string
		}{
			{id: 1, title: "first"},
			{id: 2, title: "second"},
			{id: 7, title: "a"},
			{id: 7, title: "z"},
		}
		for i, expected := range want {
			if windows[i].ID != expected.id || windows[i].Title != expected.title {
				t.Fatalf("List() order at index %d = (%d, %q), want (%d, %q)",
					i, windows[i].ID, windows[i].Title, expected.id, expected.title)
			}
		}
	}
}

// TestWaylandWindowManager_FindWindowHandleByTitle tests the findWindowHandleByTitle helper.
func TestWaylandWindowManager_FindWindowHandleByTitle(t *testing.T) {
	wm, _, _ := newStubWaylandManager("", true, false)
	handleID1 := uint32(50)
	handleID2 := uint32(51)
	wm.toplevels[handleID1] = &Info{ID: uint64(handleID1), Title: "My Test Window"}
	wm.toplevels[handleID2] = &Info{ID: uint64(handleID2), Title: "Another Window"}

	// Test case 1: Exact match
	handle, err := findWindowHandleByTitle(wm, "My Test Window")
	if err != nil {
		t.Errorf("findWindowHandleByTitle(exact) unexpected error: %v", err)
	}
	if handle == nil || handle.Title != "My Test Window" {
		t.Errorf("findWindowHandleByTitle(exact) returned wrong handle: %+v", handle)
	}

	// Test case 2: Partial match (case-insensitive)
	handle, err = findWindowHandleByTitle(wm, "test")
	if err != nil {
		t.Errorf("findWindowHandleByTitle(partial, case-insensitive) unexpected error: %v", err)
	}
	if handle == nil || handle.Title != "My Test Window" {
		t.Errorf("findWindowHandleByTitle(partial, case-insensitive) returned wrong handle: %+v", handle)
	}

	// Test case 3: No match
	_, err = findWindowHandleByTitle(wm, "Nonexistent")
	if err == nil || !strings.Contains(err.Error(), `window matching "Nonexistent" not found`) {
		t.Errorf("findWindowHandleByTitle(no match) error = %v, expected error containing %q", err, `window matching "Nonexistent" not found`)
	}
}

// TestWaylandWindowManager_List_RoundTripError tests that List returns an error if RoundTrip fails.
func TestWaylandWindowManager_List_RoundTripError(t *testing.T) {
	ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy), nextID: 0}
	display := &mockWaylandDisplay{ctx: ctx}
	_ = display
	registry := &mockRegistry{}
	ctx.Register(registry)

	managerProxyID := uint32(101)
	mockManagerProxy := &mockRawProxy{}
	ctx.SetProxy(managerProxyID, mockManagerProxy)
	mockManagerProxy.SetCtx(ctx)

	wm := &WaylandWindowManager{
		display:   display,
		registry:  registry,
		wlrMgrID:  1,
		toplevels: make(map[uint32]*Info),
	}
	ctx.objects[managerProxyID] = mockManagerProxy

	// Force RoundTrip to return an error
	ctx.dispatchFn = func() error {
		return errors.New("simulated roundtrip error")
	}

	_, err := wm.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "window/wayland: round-trip: simulated roundtrip error") {
		t.Errorf("List() error = %v, expected error containing %q", err, "window/wayland: round-trip: simulated roundtrip error")
	}
}

func TestWaylandWindowManager_ListHonorsCancellationDuringRoundTrip(t *testing.T) {
	ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy)}
	display := &blockingWaylandDisplay{
		mockWaylandDisplay: &mockWaylandDisplay{ctx: ctx},
		started:            make(chan struct{}),
	}
	wm := &WaylandWindowManager{
		display:   display,
		toplevels: make(map[uint32]*Info),
	}

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := wm.List(requestCtx)
		done <- err
	}()

	select {
	case <-display.started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("List did not enter the cancellable round trip")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("List error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("List did not return after cancellation")
	}
}

func TestWaylandWindowManager_ActionHonorsCancellationDuringRoundTrip(t *testing.T) {
	ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy)}
	display := &blockingWaylandDisplay{
		mockWaylandDisplay: &mockWaylandDisplay{ctx: ctx},
		started:            make(chan struct{}),
	}
	wm := &WaylandWindowManager{
		display:   display,
		toplevels: map[uint32]*Info{50: {ID: 50, NativeID: "50"}},
		wlrMgrID:  1,
	}

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- wm.CloseWindowByID(requestCtx, "50")
	}()

	select {
	case <-display.started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("CloseWindowByID did not enter the cancellable round trip")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CloseWindowByID error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseWindowByID did not return after cancellation")
	}
}

func TestWaylandWindowManager_ActivateRetainsSeatAfterCanceledBindRoundTrip(t *testing.T) {
	ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy)}
	display := &sequencedWaylandDisplay{
		mockWaylandDisplay: &mockWaylandDisplay{ctx: ctx},
		started:            make(chan struct{}),
	}
	registry := &mockRegistry{}
	ctx.Register(registry)
	wm := &WaylandWindowManager{
		display:   display,
		registry:  registry,
		seatID:    7,
		wlrMgrID:  1,
		toplevels: map[uint32]*Info{50: {ID: 50, NativeID: "50"}},
	}

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- wm.ActivateByID(requestCtx, "50") }()
	select {
	case <-display.started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("ActivateByID did not reach lazy seat round trip")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ActivateByID error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ActivateByID did not return after cancellation")
	}
	if wm.seat == nil {
		t.Fatal("ActivateByID discarded a successfully bound seat")
	}
	if len(ctx.objects) != 2 {
		t.Fatalf("registered objects after canceled round trip = %d, want registry and seat", len(ctx.objects))
	}
	if err := wm.ActivateByID(context.Background(), "50"); err != nil {
		t.Fatalf("ActivateByID retry: %v", err)
	}
	if display.calls != 3 {
		t.Fatalf("round trips = %d, want 3 including successful retry", display.calls)
	}
	if len(ctx.objects) != 2 {
		t.Fatalf("registered objects after retry = %d, want registry and seat", len(ctx.objects))
	}
}

func TestWaylandWindowManager_IterateWindowsAllowsReentrantCallbacks(t *testing.T) {
	wm, _, _ := newStubWaylandManager("Reentrant", false, false)

	done := make(chan error, 1)
	go func() {
		for info, err := range wm.IterateWindows(context.Background()) {
			if err != nil {
				done <- err
				return
			}
			_, _, err = wm.lookupByID(info.StableID())
			done <- err
			return
		}
		done <- errors.New("iterator yielded no windows")
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reentrant callback: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("reentrant callback deadlocked")
	}
}

func TestWaylandWindowManager_ExtForeignToplevelIsListOnly(t *testing.T) {
	wm, ctx, _ := newStubWaylandManager("Ext Window", false, false)

	for name, fn := range map[string]func() error{
		"activate": func() error { return wm.ActivateByID(context.Background(), "50") },
		"close":    func() error { return wm.CloseWindowByID(context.Background(), "50") },
		"minimize": func() error { return wm.MinimizeByID(context.Background(), "50") },
		"maximize": func() error { return wm.MaximizeByID(context.Background(), "50") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := fn(); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("%s error = %v, want ErrNotSupported", name, err)
			}
		})
	}

	if sawAnyHandleRequest(ctx, 50) {
		t.Fatalf("ext list-only backend sent a control request")
	}
}

func TestWaylandWindowManager_ActivateRequiresSeat(t *testing.T) {
	wm, ctx, _ := newStubWaylandManager("Seatless", true, false)

	err := wm.ActivateByID(context.Background(), "50")
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("Activate() error = %v, want ErrNotSupported", err)
	}
	if sawAnyHandleRequest(ctx, 50) {
		t.Fatalf("Activate() sent a request without a seat")
	}
}

func TestWaylandWindowManager_SupportedOperationsRequireSeatForActivation(t *testing.T) {
	wm, _, _ := newStubWaylandManager("Seatless", true, false)
	for _, operation := range wm.SupportedOperations() {
		if operation == "activate" {
			t.Fatal("SupportedOperations advertised activate without a wl_seat")
		}
	}

	wm, _, _ = newStubWaylandManager("With seat", true, true)
	found := false
	for _, operation := range wm.SupportedOperations() {
		if operation == "activate" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("SupportedOperations omitted activate when a wl_seat is available")
	}
}

func TestWaylandNilManagerLifecycleQueriesAreSafe(t *testing.T) {
	var manager *WaylandWindowManager
	if got := manager.WindowChanges(); got != nil {
		t.Fatalf("WindowChanges() = %v, want nil", got)
	}
	if got := manager.SupportedOperations(); got != nil {
		t.Fatalf("SupportedOperations() = %v, want nil", got)
	}
	if err := manager.Sync(context.Background()); err == nil {
		t.Fatal("Sync() returned nil error for nil manager")
	}
}

func TestWaylandExtEventsPreserveStableIdentifierAndNotify(t *testing.T) {
	info := &Info{ID: 41, NativeID: "41"}
	manager := &WaylandWindowManager{
		toplevels: map[uint32]*Info{41: info},
	}
	changes := manager.WindowChanges()

	manager.handleExtToplevelEvent(
		41,
		info,
		4,
		encodeWaylandTestString("opaque-window-id"),
	)
	if info.NativeID != "opaque-window-id" {
		t.Fatalf("NativeID = %q, want opaque-window-id", info.NativeID)
	}
	if _, _, err := manager.lookupByID("opaque-window-id"); err != nil {
		t.Fatalf("lookupByID: %v", err)
	}
	select {
	case <-changes:
	default:
		t.Fatal("stable identifier event did not notify")
	}

	manager.handleExtToplevelEvent(41, info, 0, nil)
	if _, _, err := manager.lookupByID("opaque-window-id"); !errors.Is(
		err,
		ErrWindowNotFound,
	) {
		t.Fatalf("lookup after closed event = %v", err)
	}
}

func encodeWaylandTestString(value string) []byte {
	length := len(value) + 1
	data := make([]byte, 4+length)
	wl.PutUint32(data[:4], uint32(length))
	copy(data[4:], value)
	return data
}
