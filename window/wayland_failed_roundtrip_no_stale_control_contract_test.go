package window

import (
	"context"
	"errors"
	"net"
	"testing"
)

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
