package wl

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveWaylandSocketPathRejectsUnresolvedRelativeNames(t *testing.T) {
	for _, tc := range []struct{ display, runtime, want string }{
		{"", "/tmp/safe", ""},
		{"wl-0", "", ""},
		{"/tmp/explicit-wayland", "/run/ignored", "/tmp/explicit-wayland"},
		{"wayland-4", "/tmp/private-runtime", "/tmp/private-runtime/wayland-4"},
	} {
		if got := ResolveSocketPath(tc.display, tc.runtime); got != tc.want {
			t.Errorf("display %q runtime %q resolved to %q, wanted %q", tc.display, tc.runtime, got, tc.want)
		}
	}
}

// connectSyntheticDefaultDisplay owns a private Unix socket and compositor;
// the caller explicitly closes the client and checks peer shutdown.
func connectSyntheticDefaultDisplay(t *testing.T) (*Context, <-chan error) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	peerDone := make(chan error, 1)
	go func() {
		peer, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			peerDone <- acceptErr
			return
		}
		peerDone <- privateSessionWaylandPeer(peer)
	}()
	ctx, err := Connect(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctx.Close() })
	return ctx, peerDone
}

func assertPrivateCompositorStopped(t *testing.T, peerDone <-chan error, message string) {
	t.Helper()
	select {
	case err := <-peerDone:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("synthetic compositor shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal(message)
	}
}

func TestDefaultDisplayProtocolMethodsUsePrivateSyntheticWaylandServer(t *testing.T) {
	ctx, peerDone := connectSyntheticDefaultDisplay(t)
	display := NewDisplay(ctx)
	if display.Context() != ctx {
		t.Fatal("display lost original transport")
	}
	reg, err := display.GetRegistry()
	if err != nil || reg == nil || reg.ID() <= 1 {
		t.Fatalf("GetRegistry returned %v %v", reg, err)
	}
	globals := make(map[string]bool)
	reg.SetGlobalHandler(func(ev GlobalEvent) { globals[ev.Interface] = true })
	cb, err := display.Sync()
	if err != nil || cb == nil || cb.ID() <= reg.ID() {
		t.Fatalf("Sync returned %v %v", cb, err)
	}
	if err := ctx.RoundTrip(); err != nil {
		t.Fatalf("context convenience round trip: %v", err)
	}
	if err := display.RoundTrip(); err != nil {
		t.Fatalf("display convenience round trip: %v", err)
	}
	if !globals["wl_output"] || !globals["wl_seat"] {
		t.Fatalf("default registry wrapper did not dispatch authoritative globals: %+v", globals)
	}
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	assertPrivateCompositorStopped(t, peerDone, "compositor did not terminate after transport closure")
}

func TestCallbackDispatchAndWaylandObjectDispatchBoundaries(t *testing.T) {
	fired := 0
	cb := &Callback{}
	cb.SetDoneHandler(func() { fired++ })
	cb.Dispatch(3, -1, nil)
	if fired != 0 {
		t.Fatal("unknown callback opcode fired Done")
	}
	cb.Dispatch(0, -1, nil)
	if fired != 1 {
		t.Fatalf("Done dispatched %d times", fired)
	}
	(&Shm{}).Dispatch(0, -1, nil)
	(&ShmPool{}).Dispatch(0, -1, nil)
	(&Buffer{}).Dispatch(0, -1, nil)
}

func TestListGlobalsAndSocketReachabilityUseOnlySyntheticCompositor(t *testing.T) {
	if got := ListGlobals(""); got != nil {
		t.Fatalf("empty socket unexpectedly enumerated globals: %+v", got)
	}
	notSocket := filepath.Join(t.TempDir(), "not-a-socket")
	if err := os.WriteFile(notSocket, []byte("not a compositor"), 0600); err != nil {
		t.Fatal(err)
	}
	if SocketReachable(notSocket) || ListGlobals(notSocket) != nil {
		t.Fatal("regular file accepted as Wayland socket")
	}
	socket := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peerDone := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			peerDone <- err
			return
		}
		peerDone <- privateSessionWaylandPeer(conn)
	}()
	if !SocketReachable(socket) {
		t.Fatal("private compositor socket not recognized")
	}
	names := ListGlobals(socket)
	if !names["wl_output"] || !names["wl_seat"] || len(names) != 2 {
		t.Fatalf("unexpected compositor globals %+v", names)
	}
	select {
	case err := <-peerDone:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("compositor shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ListGlobals leaked session transport")
	}
}
