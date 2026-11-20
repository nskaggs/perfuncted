package output

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func countJoinedError(err, target error) int {
	if err == nil {
		return 0
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		count := 0
		for _, child := range joined.Unwrap() {
			count += countJoinedError(child, target)
		}
		return count
	}
	if errors.Is(err, target) {
		return 1
	}
	return countJoinedError(errors.Unwrap(err), target)
}

// A compositor disconnect during lister shutdown must still release every
// locally owned protocol proxy and report each failed destructor write.
func TestWaylandCloseReportsAllDestructorTransportErrorsAndUnregisters(t *testing.T) {
	socket := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan *net.UnixConn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	connection, err := wl.Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	var server *net.UnixConn
	select {
	case server = <-accepted:
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("Wayland connection was not accepted")
	}
	defer server.Close()

	outputProxy := &wl.RawProxy{}
	connection.Register(outputProxy)
	xdgProxy := &wl.RawProxy{}
	connection.Register(xdgProxy)
	manager := &wl.RawProxy{}
	connection.Register(manager)
	out := newWaylandOutput(wl.GlobalEvent{Name: 7, Interface: "wl_output", Version: 4})
	out.proxy = outputProxy
	out.xdgProxy = xdgProxy
	lister := &WaylandLister{
		session:    &wl.Session{Ctx: connection},
		outputs:    []*waylandOutput{out},
		xdgManager: manager,
	}

	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	peerClosed := make(chan error, 1)
	go func() { peerClosed <- connection.DispatchContext(context.Background()) }()
	select {
	case err := <-peerClosed:
		if err == nil {
			t.Fatal("closed compositor unexpectedly dispatched a message")
		}
	case <-time.After(time.Second):
		t.Fatal("Wayland client did not observe compositor disconnect")
	}

	closeErr := lister.Close()
	if got := countJoinedError(closeErr, syscall.EPIPE); got != 3 {
		t.Fatalf("lister close reported %d broken-pipe destructor errors, want output, XDG output, and manager: %v", got, closeErr)
	}
	for _, proxy := range []*wl.RawProxy{outputProxy, xdgProxy, manager} {
		id := proxy.ID()
		replacement := &wl.RawProxy{}
		connection.SetProxy(id, replacement)
		if replacement.ID() != id {
			t.Fatalf("failed shutdown retained protocol proxy %d", id)
		}
	}
}
