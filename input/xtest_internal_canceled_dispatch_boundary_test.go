package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
)

// Internal XTEST helpers must refuse canceled operations before touching an
// uninitialized X11 connection or querying the server's keyboard map.
func TestXTestInternalDispatchRejectsCanceledOperationsBeforeX11Access(t *testing.T) {
	var backend XTestBackend
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"pointer movement", func() error { return backend.mouseMove(ctx, 12, 34) }},
		{"button event", func() error { return backend.mouseButton(ctx, xproto.ButtonPress, 1) }},
		{"scroll", func() error { return backend.scroll(ctx, 4, 1) }},
		{"text preflight", func() error { return backend.validateTextKeysyms(ctx, "a") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled dispatch reached X11 transport: %v", err)
			}
		})
	}
}

func TestXTestNilBackendCloseHasNoConnectionSideEffects(t *testing.T) {
	var backend *XTestBackend
	if err := backend.Close(); err != nil {
		t.Fatalf("nil XTEST close: %v", err)
	}
}
