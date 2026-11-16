//go:build linux

package input

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// cancelAtErrCallContext controls a context transition at a known observation
// boundary without depending on goroutine scheduling or elapsed time.
type cancelAtErrCallContext struct {
	context.Context
	cancel          context.CancelFunc
	cancelOnErrCall uint32
	errCalls        atomic.Uint32
}

func (c *cancelAtErrCallContext) Err() error {
	if c.errCalls.Add(1) == c.cancelOnErrCall {
		c.cancel()
	}
	return c.Context.Err()
}

func TestRemoteDesktopSubmitObservesCancellationAtSuccessfulWriterReceipt(t *testing.T) {
	backend := syntheticQueuedPortal()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, cancelOnErrCall: 2}
	result := make(chan error, 1)
	go func() {
		result <- backend.submit(ctx, func() error { return nil })
	}()

	var command remoteDesktopCommand
	select {
	case command = <-backend.commands:
	case <-time.After(time.Second):
		t.Fatal("submit did not admit its command")
	}
	if err := command.run(); err != nil {
		t.Fatalf("synthetic writer operation failed: %v", err)
	}
	command.done <- nil

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("successful writer receipt hid cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("submit did not return after the writer receipt")
	}
}

func TestRemoteDesktopTypeLiteralCanceledBeforeWriterDispatchSendsNoFrames(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	backend := newRemoteDesktopBackend(&eiSender{client: client, text: 701, textDev: 702}, &portalSession{})
	t.Cleanup(func() { _ = backend.Close() })

	base, cancel := context.WithCancel(context.Background())
	// The operation checks cancellation before admission, the submitter checks
	// again before queueing, and the writer checks before running the command.
	// Cancel at the command's own check so no EIS frame can be dispatched.
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, cancelOnErrCall: 4}
	result := make(chan error, 1)
	go func() { result <- backend.TypeLiteral(ctx, "must not be sent") }()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("TypeLiteral after writer cancellation = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("TypeLiteral did not return after writer cancellation")
	}

	// A later serialized no-op confirms that the canceled command finished
	// before checking the socket for an unauthorized text frame.
	checkCtx, checkCancel := context.WithTimeout(context.Background(), time.Second)
	defer checkCancel()
	if err := backend.submit(checkCtx, func() error { return nil }); err != nil {
		t.Fatalf("writer did not continue after rejecting canceled text: %v", err)
	}
	if err := backend.stateError(); err != nil {
		t.Fatalf("local cancellation revoked the authorized session: %v", err)
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("set EIS peer read deadline: %v", err)
	}
	buf := make([]byte, 256)
	n, err := serverConn.Read(buf)
	if n != 0 {
		t.Fatalf("canceled text emitted %d EIS bytes: %x", n, buf[:n])
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("EIS peer read error = %v, want no-frame deadline", err)
	}
}
