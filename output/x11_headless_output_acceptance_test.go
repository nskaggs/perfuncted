//go:build linux

package output

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/x11test"
)

func TestX11HeadlessRandROutputTopologyAndLifecycle(t *testing.T) {
	auth := filepath.Join(t.TempDir(), "no-host-auth")
	if err := os.WriteFile(auth, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", auth)
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		t.Skipf("isolated Xvfb unavailable: %v", err)
	}
	t.Cleanup(stop)
	lister, err := NewX11Lister(display)
	if err != nil {
		t.Fatalf("connect headless X11: %v", err)
	}
	defer lister.Close()
	outs, err := lister.List(context.Background())
	if err != nil || len(outs) == 0 {
		t.Fatalf("missing RandR headless output: %+v %v", outs, err)
	}
	for _, out := range outs {
		if out.Backend != "x11" || !out.Available || out.Geometry.W < 1 || out.Geometry.H < 1 || out.ScaleNumerator != 1 || out.ScaleDenominator != 1 {
			t.Fatalf("invalid RandR output %+v", out)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := lister.List(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query: %v", err)
	}
	if err := lister.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := lister.List(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed lister accepted: %v", err)
	}
}
