//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

type malformedBridgeReply struct{}

func (malformedBridgeReply) CallWithContext(_ context.Context, _ string, _ dbus.Flags, _ ...any) *dbus.Call {
	return &dbus.Call{Body: []any{"not the numeric response field"}}
}

// Cancellation causes must remain distinguishable from a generic canceled
// operation; a closed client or transport without descriptor passing must
// never dispatch input or capture operations to a stale GNOME bridge object.
func TestBridgeCancellationAndDisconnectedTransportFailClosed(t *testing.T) {
	cause := errors.New("caller revoked bridge authority")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := bridgeContextError(ctx); !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("bridge cancellation dropped its cause: %v", err)
	}
	if err := bridgeContextError(context.Background()); err != nil {
		t.Fatalf("live bridge context rejected: %v", err)
	}
	closed := &Client{closed: true, obj: malformedBridgeReply{}}
	if err := closed.Key(context.Background(), 37, true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed client accepted synthetic key injection: %v", err)
	}
	if _, err := closed.CaptureFull(context.Background(), 0); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed client accepted screenshot request: %v", err)
	}
	disconnected := &Client{obj: malformedBridgeReply{}}
	if _, err := disconnected.CaptureFull(context.Background(), 0); !errors.Is(err, ErrUnixFDUnsupported) {
		t.Fatalf("transport lacking file descriptor passing accepted capture: %v", err)
	}
	if _, err := disconnected.CaptureRegion(context.Background(), 0, image.Rect(0, 0, 0, 1)); err == nil || !strings.Contains(err.Error(), "empty capture region") {
		t.Fatalf("empty capture region unexpectedly accepted: %v", err)
	}
	malformed := &Client{obj: malformedBridgeReply{}, supportsFDs: true}
	var number uint32
	if err := malformed.callWithArgsOut(context.Background(), InputInterface, "PointerLocation", nil, &number); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("malformed input D-Bus reply authorized data: %v", err)
	}
	if err := malformed.capture(context.Background(), "CaptureFull", 0, nil, &number); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("malformed screenshot D-Bus reply authorized data: %v", err)
	}
}
