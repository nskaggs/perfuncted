//go:build linux

package screen

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGnomeNativeCaptureDoesNotDispatchWithoutPrivateTransport(t *testing.T) {
	backend, provider := newPrivateGnomeNativeCapture(t)
	missing := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missing)
	for _, rect := range []image.Rectangle{{}, image.Rect(1, 2, 3, 4)} {
		img, err := backend.Grab(context.Background(), rect)
		if img != nil || err == nil || !strings.Contains(err.Error(), "create transport") {
			t.Fatalf("capture %v = (%v, %v), want transport error", rect, img, err)
		}
	}
	provider.mu.Lock()
	captures := len(provider.captures)
	provider.mu.Unlock()
	if captures != 0 {
		t.Fatalf("capture service received %d requests without transport", captures)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("unexpected temporary directory state: %v", err)
	}
}
