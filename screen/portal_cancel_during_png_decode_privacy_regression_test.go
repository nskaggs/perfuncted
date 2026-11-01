//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A portal provider can hand back a pipe-like file which remains readable
// after the caller cancels. The screenshot must not be published after that
// cancel, even when a syntactically valid PNG arrives later.
func TestScreenshotPortalNeverPublishesImageAfterCancellationDuringDecode(t *testing.T) {
	backend, portal := newSyntheticScreenshotPortal(t)
	fifo := filepath.Join(t.TempDir(), "private-capture.fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	portal.mu.Lock()
	portal.uri = "file://" + fifo
	portal.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	type result struct {
		img image.Image
		err error
	}
	done := make(chan result, 1)
	go func() { img, err := backend.Grab(ctx, image.Rectangle{}); done <- result{img, err} }()
	writer := make(chan *os.File, 1)
	writerErr := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			writerErr <- err
			return
		}
		writer <- f
	}()
	var f *os.File
	select {
	case f = <-writer:
	case err := <-writerErr:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatalf("portal did not reach screenshot file decode: %v", ctx.Err())
	}
	// Both reader and writer have opened the FIFO. The reader is blocked on
	// PNG bytes, not on a portal reply, and cancellation is now deterministic.
	cancel()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(1, 1, color.RGBA{R: 255, A: 255})
	if err := png.Encode(f, img); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-done:
		if !errors.Is(value.err, context.Canceled) || value.img != nil {
			t.Fatalf("canceled screenshot leaked private pixels: img=%v err=%v", value.img, value.err)
		}
	case <-time.After(time.Second):
		t.Fatal("PNG decoder never released canceled screenshot request")
	}
}
