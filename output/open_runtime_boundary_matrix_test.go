package output

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// A canceled caller must never discover or attach to an output backend,
// regardless of whether an X11 fallback or a Wayland socket is configured.
func TestOpenRuntimeContextCanceledBeforeBackendSelection(t *testing.T) {
	for _, tc := range []struct {
		name string
		vars []string
	}{
		{"headless", nil},
		{"x11", []string{"DISPLAY=:999999"}},
		{"wayland", []string{"WAYLAND_DISPLAY=wayland-0", "XDG_RUNTIME_DIR=/nonexistent/perfuncted"}},
		{"wayland with x11 fallback", []string{"DISPLAY=:999999", "WAYLAND_DISPLAY=wayland-0", "XDG_RUNTIME_DIR=/nonexistent/perfuncted"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			lister, err := OpenRuntimeContext(ctx, env.FromEnviron(tc.vars))
			if lister != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled selection = %T, %v", lister, err)
			}
		})
	}
}

func TestOpenRuntimeContextMissingWaylandSocketDoesNotReturnPartialLister(t *testing.T) {
	runtimeDir := t.TempDir()
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-unavailable", "XDG_RUNTIME_DIR=" + runtimeDir})
	lister, err := OpenRuntimeContext(context.Background(), rt)
	if lister != nil || err == nil || !strings.Contains(err.Error(), "socket unreachable") {
		t.Fatalf("missing Wayland socket admitted: lister=%T err=%v", lister, err)
	}
}

func TestOpenRuntimeContextX11OnlyConnectionFailureDoesNotReturnPartialLister(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=invalid-display-name"})
	lister, err := OpenRuntimeContext(context.Background(), rt)
	if lister != nil || err == nil || !strings.Contains(err.Error(), "output/x11: connect") {
		t.Fatalf("invalid X11 display admitted: lister=%T err=%v", lister, err)
	}
}

func TestOutputProbeAndOpenDistinguishRegularFileFromWaylandSocket(t *testing.T) {
	dir := t.TempDir()
	// A regular file with a Wayland socket's filename is not a compositor.
	path := dir + "/wayland-0"
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-0", "XDG_RUNTIME_DIR=" + dir})
	results := ProbeRuntime(rt)
	if len(results) != 1 || results[0].Available || results[0].Selected || results[0].Name != "wayland" {
		t.Fatalf("regular file falsely probed as a compositor: %+v", results)
	}
	lister, err := OpenRuntimeContext(context.Background(), rt)
	if lister != nil || err == nil || !strings.Contains(err.Error(), "socket unreachable") {
		t.Fatalf("regular file falsely opened as a compositor: %T %v", lister, err)
	}
}

func TestOpenRuntimeContextUnreachableWaylandX11FallbackFailureHasNilLister(t *testing.T) {
	rt := env.FromEnviron([]string{
		"DISPLAY=invalid-display-name",
		"WAYLAND_DISPLAY=wayland-unavailable",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})
	lister, err := OpenRuntimeContext(context.Background(), rt)
	if lister != nil || err == nil || !strings.Contains(err.Error(), "output/x11: connect") {
		t.Fatalf("failed fallback leaked typed-nil lister: %T, %v", lister, err)
	}
}
