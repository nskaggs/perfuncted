package window

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

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
