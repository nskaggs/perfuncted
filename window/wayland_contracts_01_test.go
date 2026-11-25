package window

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/internal/wl"
	"testing"
)

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
