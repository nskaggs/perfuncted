//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Both methods with typed replies and screenshot capture must reject malformed
// transport data. Capture must also retain the caller's cancellation cause
// when a D-Bus call is interrupted after it has reached the provider.
func TestBridgeCaptureCancellationCauseAndTypedReplyRejection(t *testing.T) {
	malformed := &Client{obj: malformedBridgeReply{}}
	var number uint32
	if err := malformed.call(context.Background(), CoreInterface, "GetProtocolVersion", &number); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("invalid typed GNOME bridge reply authorized data: %v", err)
	}
	object := &blockingObject{seen: make(chan context.Context, 1)}
	client := &Client{obj: object, supportsFDs: true}
	cause := errors.New("screenshot authorization was revoked")
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.capture(ctx, "CaptureFull", 0, nil) }()
	select {
	case <-object.seen:
	case <-time.After(time.Second):
		cancel(cause)
		t.Fatal("capture never entered bounded D-Bus call")
	}
	cancel(cause)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
			t.Fatalf("canceled capture lost operation or revocation cause: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("capture did not terminate when revoked")
	}
	preCanceled, preCancel := context.WithCancel(context.Background())
	preCancel()
	if err := client.capture(preCanceled, "CaptureFull", 0, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled capture bypassed cancellation: %v", err)
	}
}
