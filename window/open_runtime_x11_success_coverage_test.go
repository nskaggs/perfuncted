//go:build linux

package window

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/x11test"
)

func TestOpenRuntimeContextSelectsLiveX11WindowManager(t *testing.T) {
	authority := filepath.Join(t.TempDir(), "empty-xauthority")
	if err := os.WriteFile(authority, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", authority)
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		t.Skipf("isolated Xvfb unavailable: %v", err)
	}
	t.Cleanup(stop)

	manager, err := OpenRuntimeContext(context.Background(), env.FromEnviron([]string{"DISPLAY=" + display}))
	if err != nil {
		t.Fatalf("open window runtime on Xvfb: %v", err)
	}
	x11Manager, ok := manager.(*X11Backend)
	if !ok || x11Manager == nil {
		t.Fatalf("window runtime returned %T, want live *X11Backend", manager)
	}
	t.Cleanup(func() { _ = manager.Close() })

	windows, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("list Xvfb windows: %v", err)
	}
	if len(windows) != 0 {
		t.Fatalf("fresh Xvfb unexpectedly contains windows: %+v", windows)
	}
}
