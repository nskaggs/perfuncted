package input

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"sync"
	"testing"
	"time"
)

// observedEISWaitContext reports when the EIS reader has subscribed to
// cancellation. This makes a blocked-wait test independent of scheduler timing.
type observedEISWaitContext struct { //nolint:containedctx // Test wrapper observes when Done is subscribed without altering cancellation semantics.
	parent  context.Context //nolint:containedctx // Observes the real context, never stored in production.
	started chan struct{}
	once    sync.Once
}

func (c *observedEISWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.started) })
	return c.parent.Done()
}

func (c *observedEISWaitContext) Deadline() (time.Time, bool) { return c.parent.Deadline() }
func (c *observedEISWaitContext) Err() error                  { return c.parent.Err() }
func (c *observedEISWaitContext) Value(key any) any           { return c.parent.Value(key) }

func TestEIClientBlockedNextHonorsCancellationWithoutConsumingSerial(t *testing.T) {
	clientConn, peer := unixConnectionPair(t)
	defer peer.Close()
	client := newEIClient(clientConn)
	defer client.close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wait := &observedEISWaitContext{parent: ctx, started: make(chan struct{})}
	result := make(chan error, 1)
	client.serial.Store(71)
	go func() { _, err := client.next(wait); result <- err }()
	select {
	case <-wait.started:
	case <-time.After(time.Second):
		t.Fatal("EIS next did not enter cancelable wait")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled EIS wait returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("EIS wait ignored caller cancellation")
	}
	if got := client.serial.Load(); got != 71 {
		t.Fatalf("cancelled wait advanced event serial to %d", got)
	}
}

func TestEIClientBlockedNextReportsRemoteTransportFailure(t *testing.T) {
	clientConn, peer := unixConnectionPair(t)
	client := newEIClient(clientConn)
	defer client.close()
	defer peer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	wait := &observedEISWaitContext{parent: ctx, started: make(chan struct{})}
	result := make(chan error, 1)
	go func() { _, err := client.next(wait); result <- err }()
	select {
	case <-wait.started:
	case <-ctx.Done():
		t.Fatal("EIS reader never entered event wait")
	}
	if err := peer.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil || errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, io.EOF) {
			t.Fatalf("EIS wait lost peer EOF error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("EIS reader did not report peer half-close")
	}
}

func TestEIClientWriteBatchStopsAtClosedTransport(t *testing.T) {
	clientConn, peer := unixConnectionPair(t)
	defer peer.Close()
	defer clientConn.Close()
	if err := clientConn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	c := &eiClient{conn: clientConn}
	err := c.writeMessages([]byte{1, 2, 3}, []byte{4, 5, 6})
	if !errors.Is(err, unix.EPIPE) {
		t.Fatalf("EIS writes accepted a closed output stream: %v", err)
	}
}
