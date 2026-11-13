package screen

import (
	"context"
	"errors"
	"testing"
)

// An already canceled caller cannot initiate a compositor connection, even
// if its socket path is missing or invalid. Cancellation wins before display
// discovery and must not be converted into a configuration error.
func TestExtCaptureConstructorPreCanceledBeforeSocketDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, sock := range []string{"", "/nonexistent-perfuncted-wayland-socket"} {
		b, err := NewExtCaptureBackendForSocketContext(ctx, sock)
		if b != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled constructor socket=%q backend=%v err=%v", sock, b, err)
		}
	}
}
