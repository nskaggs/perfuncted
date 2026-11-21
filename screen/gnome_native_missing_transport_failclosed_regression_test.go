//go:build linux

package screen

import (
	"context"
	"image"
	"path/filepath"
	"strings"
	"testing"
)

// An out-of-contract GNOME service can unlink the temporary transport file
// while retaining its already-open descriptor. The caller must fail closed:
// even a valid decoded PNG is not an authorized capture when cleanup fails.
func TestGnomeNativeUnlinkedTransportFailsClosedWithoutReturningPixels(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	backend, provider := newPrivateGnomeNativeCapture(t)
	provider.mu.Lock()
	provider.removeTransport = true
	provider.mu.Unlock()
	img, err := backend.Grab(context.Background(), image.Rectangle{})
	if err == nil || !strings.Contains(err.Error(), "remove transport") || img != nil {
		t.Fatalf("deleted transport disclosed image=%v, error=%v", img, err)
	}
	matches, err := filepath.Glob(filepath.Join(root, "perfuncted-gnome-capture-*.png"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("capture left private files behind: %v (%v)", matches, err)
	}
}
