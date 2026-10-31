package output

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWaylandXDGOutputBindSendsActualRequestAndTracksLogicalGeometry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan *net.UnixConn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, lintShadow25 := listener.AcceptUnix()
		if lintShadow25 != nil {
			acceptErr <- lintShadow25
			return
		}
		accepted <- conn
	}()
	ctx, err := wl.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ctx.Close()
	var server *net.UnixConn
	select {
	case server = <-accepted:
		defer server.Close()
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("Wayland connection not accepted")
	}
	manager := &wl.RawProxy{}
	ctx.Register(manager)
	physical := &wl.RawProxy{}
	ctx.Register(physical)
	out := newWaylandOutput(wl.GlobalEvent{Name: 7, Interface: "wl_output", Version: 3})
	out.proxy = physical
	out.updateProxy(physical)
	lister := &WaylandLister{session: &wl.Session{Ctx: ctx}, xdgManager: manager}
	if err := lister.bindXDGOutput(context.Background(), out); err != nil {
		t.Fatalf("xdg-output bind: %v", err)
	}
	var wire [16]byte
	if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(server, wire[:]); err != nil {
		t.Fatal(err)
	}
	if wl.Uint32(wire[0:4]) != manager.ID() || wl.Uint32(wire[4:8]) != 16<<16|1 || wl.Uint32(wire[8:12]) != out.xdgProxy.ID() || wl.Uint32(wire[12:16]) != physical.ID() {
		t.Fatalf("unexpected get_xdg_output wire request: %x", wire)
	}
	// The registered callback updates the output through the same object
	// proxy used by a real compositor event dispatcher.
	mode := make([]byte, 16)
	wl.PutUint32(mode[0:4], 1)
	wl.PutUint32(mode[4:8], 2560)
	wl.PutUint32(mode[8:12], 1440)
	physical.OnEvent(1, 0, mode)
	if out.xdgProxy == nil {
		t.Fatal("bind did not install output listener")
	}
	pos := make([]byte, 8)
	wl.PutUint32(pos[0:4], uint32(120))
	wl.PutUint32(pos[4:8], uint32(30))
	size := make([]byte, 8)
	wl.PutUint32(size[0:4], uint32(2048))
	wl.PutUint32(size[4:8], uint32(1152))
	out.xdgProxy.OnEvent(0, 0, pos)
	out.xdgProxy.OnEvent(1, 0, size)
	if out.info.Geometry.X != 120 || out.info.Geometry.Y != 30 || out.info.Geometry.W != 2048 || out.info.Geometry.H != 1152 || out.info.ScaleNumerator != 5 || out.info.ScaleDenominator != 4 {
		t.Fatalf("xdg-output logical geometry not reflected: %+v", out.info)
	}
}

func TestWaylandXDGOutputBindFailsClosedWhenCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accept := make(chan *net.UnixConn, 1)
	go func() {
		conn, lintShadow99 := listener.AcceptUnix()
		if lintShadow99 == nil {
			accept <- conn
		}
	}()
	wlctx, err := wl.Connect(path)
	if err != nil {
		t.Fatal(err)
	}
	defer wlctx.Close()
	select {
	case conn := <-accept:
		defer conn.Close()
	case <-time.After(time.Second):
		t.Fatal("Wayland socket unavailable")
	}
	manager := &wl.RawProxy{}
	wlctx.Register(manager)
	physical := &wl.RawProxy{}
	wlctx.Register(physical)
	out := newWaylandOutput(wl.GlobalEvent{Name: 8, Interface: "wl_output", Version: 2})
	out.proxy = physical
	lister := &WaylandLister{session: &wl.Session{Ctx: wlctx}, xdgManager: manager}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lister.bindXDGOutput(canceled, out); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled XDG bind accepted: %v", err)
	}
	if out.xdgProxy != nil {
		t.Fatal("failed bind published an unusable output proxy")
	}
}
