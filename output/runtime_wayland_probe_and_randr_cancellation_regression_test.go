package output

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/randr"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestOutputRuntimeDoesNotFallBackAfterReachableWaylandRejectsDiscovery(t *testing.T) {
	socket := shortPrivateSocketPath(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() { done <- serveUnusableWaylandCompositor(listener, nil) }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=" + socket, "DISPLAY=invalid-display"})
	lister, err := OpenRuntimeContext(ctx, rt)
	if lister != nil || err == nil || !strings.Contains(err.Error(), "no wl_output globals advertised") {
		if lister != nil {
			_ = lister.Close()
		}
		t.Fatalf("Wayland discovery failure returned lister=%v err=%v", lister, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("failed Wayland discovery did not close its connection")
	}
}

func TestOutputRuntimeReturnsUsableWaylandListerAfterDiscovery(t *testing.T) {
	socket := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() { done <- serveWaylandOutputs(listener) }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=" + socket, "DISPLAY=invalid-display"})
	lister, err := OpenRuntimeContext(ctx, rt)
	if err != nil || lister == nil {
		t.Fatalf("Wayland discovery returned lister=%v err=%v", lister, err)
	}
	t.Cleanup(func() { _ = lister.Close() })
	outputs, err := lister.List(ctx)
	if err != nil || len(outputs) != 2 || outputs[0].Name != "DP-1" || outputs[1].Name != "DP-2" {
		t.Fatalf("Wayland outputs=%+v err=%v", outputs, err)
	}
	if err := lister.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Wayland lister did not release its connection")
	}
}

func TestX11OutputListRejectsCancellationAfterEmptyRandRQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	primaryQueried := false
	lister := &X11Lister{randrAvailable: true, conn: &x11.MockConnection{
		DefaultScreenFunc: func() *xproto.ScreenInfo {
			return &xproto.ScreenInfo{Root: 42, WidthInPixels: 1920, HeightInPixels: 1080}
		},
		GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
			return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{}, nil)
		},
		GetOutputPrimaryFunc: func(xproto.Window) x11.RandROutputPrimaryCookie {
			primaryQueried = true
			cancel()
			return x11.NewMockRandROutputPrimaryCookie(&randr.GetOutputPrimaryReply{}, nil)
		},
	}}
	outputs, err := lister.List(ctx)
	if !primaryQueried || outputs != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled empty RandR query returned outputs=%v err=%v primary queried=%v", outputs, err, primaryQueried)
	}
}
