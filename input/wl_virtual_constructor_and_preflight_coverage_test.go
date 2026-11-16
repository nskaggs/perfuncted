//go:build linux

package input

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWlVirtualConstructorHonorsPreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend, err := NewWlVirtualBackendContext(ctx, filepath.Join(t.TempDir(), "unused.sock"))
	if backend != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("constructor with canceled context = backend %v, error %v; want context.Canceled", backend, err)
	}
}

func TestWlVirtualTypeRejectsMalformedSequenceBeforeWireOutput(t *testing.T) {
	backend, peer := newCapturedWlVirtualBackend(t)
	err := backend.Type(context.Background(), "prefix{unclosed")
	if err == nil {
		t.Fatal("malformed key sequence was accepted")
	}
	if closeErr := backend.Close(); closeErr != nil {
		t.Fatalf("close after rejected type sequence: %v", closeErr)
	}
	if messages := readCapturedWlWrites(t, peer); len(messages) != 0 {
		t.Fatalf("malformed type sequence emitted %d Wayland messages", len(messages))
	}
}

func TestWlVirtualPointerFrameRejectsUnavailablePointerBeforeWireOutput(t *testing.T) {
	backend := &WlVirtualBackend{ptr: &wl.RawProxy{}}
	wire := &recordingCtx{}
	err := backend.ptrFrame(context.Background(), wire)
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("pointer frame with unavailable pointer = %v, want unsupported error", err)
	}
	if len(wire.msgs) != 0 {
		t.Fatalf("unavailable pointer emitted %d Wayland messages", len(wire.msgs))
	}
}
