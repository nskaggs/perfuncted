//go:build linux

package compositor

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestCompositorDetectionCanceledAndUnreachableSocketDoNotSelectDesktopAuthority(t *testing.T) {
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=unreachable", "XDG_CURRENT_DESKTOP=KDE"})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if kind, err := DetectRuntimeContext(canceled, rt); kind != Unknown || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled detection selected %s err=%v", kind, err)
	}
	if kind, ok := probeGlobalsContext(context.Background(), env.FromEnviron([]string{"WAYLAND_DISPLAY=", "XDG_RUNTIME_DIR="})); ok || kind != X11 {
		t.Fatalf("empty compositor socket detected: kind=%s ok=%v", kind, ok)
	}
	missing := env.FromEnviron([]string{"WAYLAND_DISPLAY=" + filepath.Join(t.TempDir(), "no-socket")})
	if kind, ok := probeGlobalsContext(context.Background(), missing); ok || kind != X11 {
		t.Fatalf("unreachable compositor accepted: kind=%s ok=%v", kind, ok)
	}
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if kind, err := DetectRuntimeContext(deadline, missing); kind != Unknown || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired detection selected %s err=%v", kind, err)
	}
}
