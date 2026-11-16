//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type observedWlVirtualDoneContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (c *observedWlVirtualDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestWlVirtualCanceledOperationLeavesSerializedAdmission(t *testing.T) {
	backend, peer := newCapturedWlVirtualBackend(t)
	gate := backend.operationGateChannel()
	<-gate
	defer func() { gate <- struct{}{} }()

	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &observedWlVirtualDoneContext{Context: base, observed: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- backend.MouseMove(ctx, 20, 30) }()
	select {
	case <-ctx.observed:
	case <-time.After(time.Second):
		t.Fatal("operation did not enter cancellation-aware admission")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked operation returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled operation did not leave admission")
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close after canceled admission: %v", err)
	}
	if messages := readCapturedWlWrites(t, peer); len(messages) != 0 {
		t.Fatalf("canceled admission emitted %d Wayland messages", len(messages))
	}
}

func TestWlVirtualPointerCoordinateQueryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var backend *WlVirtualBackend
	if _, err := backend.PointerCoordinateSpace(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("coordinate query after cancellation = %v, want context.Canceled", err)
	}
}

func TestWlVirtualClosedBackendRejectsCoordinateQueryAndMouseUp(t *testing.T) {
	backend, peer := newCapturedWlVirtualBackend(t)
	if err := backend.Close(); err != nil {
		t.Fatalf("close backend: %v", err)
	}
	if _, err := backend.PointerCoordinateSpace(context.Background()); err == nil || !strings.Contains(err.Error(), "backend is closed") {
		t.Fatalf("closed backend coordinate query = %v, want closed error", err)
	}
	if err := backend.MouseUp(context.Background(), 1); err == nil || !strings.Contains(err.Error(), "backend is closed") {
		t.Fatalf("mouse up after close = %v, want closed error", err)
	}
	if messages := readCapturedWlWrites(t, peer); len(messages) != 0 {
		t.Fatalf("closed backend emitted %d Wayland messages", len(messages))
	}
}

func TestWlVirtualKeyOperationsPropagateKeyboardTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		call  func(*WlVirtualBackend) error
		setUp func(*wlKeyboard)
	}{
		{
			name: "key down",
			call: func(b *WlVirtualBackend) error { return b.KeyDown(context.Background(), "a") },
		},
		{
			name:  "key up",
			setUp: func(k *wlKeyboard) { k.held["a"] = kcDynBase },
			call:  func(b *WlVirtualBackend) error { return b.KeyUp(context.Background(), "a") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, peer := newCapturedWlVirtualBackend(t)
			keyboard, wire := newFailingKeyboard(0)
			if tc.setUp != nil {
				tc.setUp(keyboard)
			}
			backend.kbd = keyboard
			err := tc.call(backend)
			if err == nil || !strings.Contains(err.Error(), "injected write failure") {
				t.Fatalf("%s transport failure = %v, want injected write error", tc.name, err)
			}
			if wire.writes != 0 {
				t.Fatalf("keyboard recorded %d successful writes after injected failure", wire.writes)
			}
			if closeErr := backend.Close(); closeErr != nil {
				t.Fatalf("close after %s failure: %v", tc.name, closeErr)
			}
			if messages := readCapturedWlWrites(t, peer); len(messages) != 0 {
				t.Fatalf("failed %s emitted %d display messages", tc.name, len(messages))
			}
		})
	}
}

func TestWlVirtualScrollPropagatesDisconnectedTransportFailure(t *testing.T) {
	backend, peer := newCapturedWlVirtualBackend(t)
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := backend.ScrollUp(context.Background(), 1); err == nil {
		t.Fatal("scroll succeeded after its Wayland peer disconnected")
	}
}
