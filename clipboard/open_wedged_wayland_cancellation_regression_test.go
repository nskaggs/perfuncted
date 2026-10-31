package clipboard

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

// An active Unix listener is not evidence that a compositor will ever
// answer wl_display.get_registry. A caller's deadline must bound detection
// before the clipboard implementation is chosen or any owner is launched.
func TestClipboardOpenAbortsBlockedWaylandProbeAtCallerDeadline(t *testing.T) {
	dir := t.TempDir()
	listener, err := net.Listen("unix", filepath.Join(dir, "wayland-hung"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-hung", "XDG_RUNTIME_DIR=" + dir, "XDG_SESSION_TYPE=wayland", "XDG_CURRENT_DESKTOP=sway"})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	clipboard, err := OpenRuntimeContext(ctx, rt)
	if clipboard != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wedged Wayland detection returned backend=%T err=%v, want caller timeout", clipboard, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("deadline ignored during compositor detection: %v", elapsed)
	}
}
