package window

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

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
	case <-changes:
		t.Fatal("closed backend delivered spurious change")
	default:
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
