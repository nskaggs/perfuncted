package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Direct virtual-input calls are not permitted to dispatch to an uninitialized
// Wayland connection, and cancellation wins even before resource validation.
func TestWlVirtualUninitializedOperationsFailBeforeProtocolAccess(t *testing.T) {
	var backend WlVirtualBackend
	live := context.Background()
	canceled, cancel := context.WithCancel(live)
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		run  func(context.Context) error
		want string
	}{
		{"uninitialized move", live, func(ctx context.Context) error { return backend.MouseMove(ctx, 3, 5) }, "not initialised"},
		{"uninitialized button", live, func(ctx context.Context) error { return backend.button(ctx, btnLeft, 1) }, "not initialised"},
		{"uninitialized scroll", live, func(ctx context.Context) error { return backend.scroll(ctx, 0, 1) }, "not initialised"},
		{"canceled button", canceled, func(ctx context.Context) error { return backend.button(ctx, btnLeft, 1) }, ""},
		{"canceled scroll", canceled, func(ctx context.Context) error { return backend.scroll(ctx, 0, 1) }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(tc.ctx)
			if tc.want == "" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled operation reached Wayland setup: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("uninitialized operation reached Wayland transport: %v", err)
			}
		})
	}
}

func TestWlVirtualNilBackendCloseAndUninitializedCoordinateSpace(t *testing.T) {
	var nilBackend *WlVirtualBackend
	if err := nilBackend.Close(); err != nil {
		t.Fatalf("nil backend close: %v", err)
	}
	if _, err := nilBackend.PointerCoordinateSpace(context.Background()); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("nil backend coordinate space was reported: %v", err)
	}
	var empty WlVirtualBackend
	space, err := empty.PointerCoordinateSpace(context.Background())
	if err != nil || space.Kind != CoordinateSpaceLogical {
		t.Fatalf("uninitialized coordinate space: %+v %v", space, err)
	}
}
