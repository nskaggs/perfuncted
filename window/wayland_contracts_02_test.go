package window

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/wl"
	"reflect"
	"testing"
)

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
