//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A portal-controlled screenshot file might be a FIFO that never sends PNG
// bytes. Cancellation must interrupt the in-flight decoder, not merely be
// checked after the pipe unexpectedly reaches EOF.
func TestScreenshotPortalCancellationInterruptsBlockedPNGTransport(t *testing.T) {
	b, p := newSyntheticScreenshotPortal(t)
	fifo := filepath.Join(t.TempDir(), "withheld-pixels.fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Skipf("FIFO unsupported: %v", err)
	}
	p.mu.Lock()
	p.uri = "file://" + fifo
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	type captureResult struct {
		image image.Image
		err   error
	}
	result := make(chan captureResult, 1)
	go func() { img, err := b.Grab(ctx, image.Rectangle{}); result <- captureResult{img, err} }()
	opened := make(chan *os.File, 1)
	go func() {
		f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err == nil {
			opened <- f
		}
	}()
	var writer *os.File
	select {
	case writer = <-opened:
	case <-ctx.Done():
		t.Fatal("capture transport never opened")
	}
	defer writer.Close()
	cancel()
	select {
	case value := <-result:
		if !errors.Is(value.err, context.Canceled) || value.image != nil {
			t.Fatalf("canceling stalled capture returned pixels=%v error=%v", value.image, value.err)
		}
	case <-time.After(150 * time.Millisecond):
		// Unblock a broken decoder before failing so this regression test never
		// leaves an unbounded provider-read goroutine in the suite.
		_ = writer.Close()
		select {
		case <-result:
		case <-time.After(time.Second):
		}
		t.Fatal("cancellation failed to interrupt stalled PNG transport")
	}
}
