package output

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// A canceled discovery request cannot touch a Wayland socket or disclose a
// partial output snapshot. Its cancellation takes priority over malformed
// runtime settings, including a missing WAYLAND_DISPLAY.
func TestWaylandOutputDiscoveryRejectsCanceledRequestsBeforeDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, socket := range []string{"", filepath.Join(t.TempDir(), "missing.sock")} {
		l, err := NewWaylandListerContext(ctx, socket)
		if l != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled discovery of %q: lister=%v err=%v", socket, l, err)
		}
	}
}

// Without a compositor connection, discovery must report absence, not return
// an initialized empty lister that callers could mistake for no monitors.
func TestWaylandOutputDiscoveryFailsClosedWithoutCompositor(t *testing.T) {
	for _, tc := range []struct {
		name, socket string
		contains     string
	}{
		{"missing WAYLAND_DISPLAY", "", "WAYLAND_DISPLAY"},
		{"unreachable socket", filepath.Join(t.TempDir(), "not-running.sock"), "connect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, err := NewWaylandListerContext(context.Background(), tc.socket)
			if l != nil || err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("unavailable output accepted: lister=%v err=%v", l, err)
			}
			if errors.Is(err, net.ErrClosed) {
				t.Fatalf("no compositor was reported as an existing closed lister: %v", err)
			}
		})
	}
}
