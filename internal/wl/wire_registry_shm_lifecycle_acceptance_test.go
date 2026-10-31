package wl

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWaylandRegistryAndSHMExchangeRealWireAndFileDescriptors(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connected := make(chan *net.UnixConn, 1)
	go func() {
		peer, lintShadow24 := listener.AcceptUnix()
		if lintShadow24 == nil {
			connected <- peer
		}
	}()
	ctx, err := Connect(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ctx.Close()
	var peer *net.UnixConn
	select {
	case peer = <-connected:
		defer peer.Close()
	case <-time.After(time.Second):
		t.Fatal("synthetic compositor not reachable")
	}
	readMessage := func() ([]byte, []byte) {
		t.Helper()
		if lintShadow43 := peer.SetReadDeadline(time.Now().Add(time.Second)); lintShadow43 != nil {
			t.Fatal(lintShadow43)
		}
		raw := make([]byte, 4096)
		oob := make([]byte, 256)
		n, oobn, _, _, lintShadow48 := peer.ReadMsgUnix(raw, oob)
		if lintShadow48 != nil {
			t.Fatal(lintShadow48)
		}
		if n < 8 || int(Uint32(raw[4:8])>>16) != n {
			t.Fatalf("bad Wayland wire length: %x", raw[:n])
		}
		return raw[:n], oob[:oobn]
	}
	reg := &Registry{}
	ctx.Register(reg)
	target := &RawProxy{}
	ctx.Register(target)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if lintShadow63 := reg.BindContext(canceled, 7, "wl_output", 3, target.ID()); !errors.Is(lintShadow63, context.Canceled) {
		t.Fatalf("canceled registry bind wrote protocol: %v", lintShadow63)
	}
	if lintShadow66 := reg.Bind(7, "wl_output", 3, target.ID()); lintShadow66 != nil {
		t.Fatal(lintShadow66)
	}
	msg, oob := readMessage()
	if Uint32(msg[:4]) != reg.ID() || Uint32(msg[4:8])&0xffff != 0 || Uint32(msg[8:12]) != 7 || Uint32(msg[12:16]) != 10 || string(msg[16:25]) != "wl_output" || msg[25] != 0 || Uint32(msg[28:32]) != 3 || Uint32(msg[32:36]) != target.ID() || len(oob) != 0 {
		t.Fatalf("registry bind does not preserve object ID, name, interface, version, or new ID: %x oob=%x", msg, oob)
	}
	shm := &Shm{}
	ctx.Register(shm)
	input, err := os.CreateTemp(t.TempDir(), "shared-memory-*")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	content := []byte("pixels for synthetic compositor")
	if _, lintShadow81 := input.Write(content); lintShadow81 != nil {
		t.Fatal(lintShadow81)
	}
	before := len(ctx.objects)
	if pool, lintShadow85 := shm.CreatePoolContext(canceled, int(input.Fd()), 4096); !errors.Is(lintShadow85, context.Canceled) || pool != nil || len(ctx.objects) != before {
		t.Fatalf("canceled shared memory bind mutated registry: %v, %p", lintShadow85, pool)
	}
	pool, err := shm.CreatePool(int(input.Fd()), 4096)
	if err != nil {
		t.Fatal(err)
	}
	msg, oob = readMessage()
	if len(msg) != 16 || Uint32(msg[:4]) != shm.ID() || Uint32(msg[8:12]) != pool.ID() || Uint32(msg[12:16]) != 4096 {
		t.Fatalf("invalid shm create_pool wire: %x", msg)
	}
	cms, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		t.Fatal(err)
	}
	if len(cms) != 1 {
		t.Fatalf("expected exactly one shared memory fd, got %d", len(cms))
	}
	fds, err := syscall.ParseUnixRights(&cms[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(fds) != 1 {
		t.Fatalf("shared memory missing fd: %v", fds)
	}
	func() {
		fd := os.NewFile(uintptr(fds[0]), "synthetic compositor mapped memory")
		defer fd.Close()
		got := make([]byte, len(content))
		if _, lintShadow114 := fd.ReadAt(got, 0); lintShadow114 != nil || !bytes.Equal(got, content) {
			t.Fatalf("passed FD does not own source pixel bytes: %q %v", got, lintShadow114)
		}
	}()
	before = len(ctx.objects)
	if buf, lintShadow119 := pool.CreateBufferContext(canceled, 8, 64, 32, 256, 875713112); !errors.Is(lintShadow119, context.Canceled) || buf != nil || len(ctx.objects) != before {
		t.Fatalf("canceled SHM buffer bind mutated registry: %v, %p", lintShadow119, buf)
	}
	buffer, err := pool.CreateBuffer(8, 64, 32, 256, 875713112)
	if err != nil {
		t.Fatal(err)
	}
	msg, oob = readMessage()
	if len(msg) != 32 || Uint32(msg[:4]) != pool.ID() || Uint32(msg[8:12]) != buffer.ID() || Uint32(msg[12:16]) != 8 || Uint32(msg[16:20]) != 64 || Uint32(msg[20:24]) != 32 || Uint32(msg[24:28]) != 256 || Uint32(msg[28:32]) != 875713112 || len(oob) != 0 {
		t.Fatalf("invalid SHM buffer parameters: %x", msg)
	}
	if err := buffer.DestroyContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled buffer destroy: %v", err)
	}
	if _, ok := ctx.objects[buffer.ID()]; !ok {
		t.Fatal("canceled destruction incorrectly dropped live buffer")
	}
	if err := buffer.Destroy(); err != nil {
		t.Fatal(err)
	}
	msg, _ = readMessage()
	if len(msg) != 8 || Uint32(msg[:4]) != buffer.ID() {
		t.Fatalf("buffer destroy wrong object: %x", msg)
	}
	if _, ok := ctx.objects[buffer.ID()]; ok {
		t.Fatal("destroyed buffer not unregistered")
	}
	if err := pool.DestroyContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled pool destruction: %v", err)
	}
	if _, ok := ctx.objects[pool.ID()]; !ok {
		t.Fatal("canceled pool destruction dropped active pool")
	}
	if err := pool.Destroy(); err != nil {
		t.Fatal(err)
	}
	msg, _ = readMessage()
	if len(msg) != 8 || Uint32(msg[:4]) != pool.ID() || Uint32(msg[4:8])&0xffff != 1 {
		t.Fatalf("pool destroy wrong wire request: %x", msg)
	}
	if _, ok := ctx.objects[pool.ID()]; ok {
		t.Fatal("destroyed pool not unregistered")
	}
	// Direct dispatch wrapper drives an actual callback over the socket.
	cb := &Callback{}
	ctx.Register(cb)
	fired := false
	cb.SetDoneHandler(func() { fired = true })
	callbackWire := make([]byte, 12)
	PutUint32(callbackWire[:4], cb.ID())
	PutUint32(callbackWire[4:8], 12<<16)
	PutUint32(callbackWire[8:12], 888)
	if _, err := peer.Write(callbackWire); err != nil {
		t.Fatal(err)
	}
	if err := DispatchContext(context.Background(), ctx); err != nil || !fired {
		t.Fatalf("wayland callback not dispatched from real wire: fired=%v err=%v", fired, err)
	}
	// The underlying socket must not have inherited a cancelled I/O deadline.
	if err := WriteMsg(ctx, []byte{1, 0, 0, 0, 0, 0, 8, 0}, nil); err != nil {
		t.Fatalf("prior cancellation leaked write deadline: %v", err)
	}
	msg, _ = readMessage()
	if len(msg) != 8 || Uint32(msg[:4]) != 1 {
		t.Fatalf("wire wrapper lost message: %x", msg)
	}
}
