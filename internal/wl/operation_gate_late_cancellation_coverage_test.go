package wl

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// cancelAfterAdmissionContext lets a test cancel exactly at the post-gate check.
type cancelAfterAdmissionContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
	calls   int
}

func (c *cancelAfterAdmissionContext) Err() error {
	c.calls++
	if c.calls == 2 {
		c.once.Do(func() { close(c.checked) })
		<-c.Context.Done()
	}
	return c.Context.Err()
}

func TestContextOperationsObserveCancellationAfterAdmissionBeforeTransport(t *testing.T) {
	for _, operation := range []struct {
		name string
		run  func(*Context, context.Context) error
	}{
		{name: "write", run: func(ctx *Context, cancel context.Context) error {
			return ctx.WriteMsgContext(cancel, []byte{1}, nil)
		}},
		{name: "dispatch", run: func(ctx *Context, cancel context.Context) error {
			return ctx.DispatchContext(cancel)
		}},
		{name: "round trip", run: func(ctx *Context, cancel context.Context) error {
			return ctx.RoundTripContext(cancel)
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			ctx, peerRead := newWaylandAdmissionTestContext(t)
			baseCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observedCtx := &cancelAfterAdmissionContext{Context: baseCtx, checked: make(chan struct{})}
			operationDone := make(chan error, 1)
			go func() { operationDone <- operation.run(ctx, observedCtx) }()

			select {
			case <-observedCtx.checked:
			case <-time.After(time.Second):
				t.Fatal("operation did not reach its post-admission cancellation check")
			}
			cancel()
			select {
			case err := <-operationDone:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("operation error = %v, want context.Canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("operation did not return after cancellation")
			}

			if err := ctx.Close(); err != nil {
				t.Fatalf("close Wayland connection: %v", err)
			}
			select {
			case data := <-peerRead:
				if len(data) != 0 {
					t.Fatalf("canceled operation wrote %d transport bytes", len(data))
				}
			case <-time.After(time.Second):
				t.Fatal("peer read did not finish after connection close")
			}
		})
	}
}

func newWaylandAdmissionTestContext(t *testing.T) (*Context, <-chan []byte) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wlgate-")
	if err != nil {
		t.Fatalf("create short Wayland socket directory: %v", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "wayland.sock"), Net: "unix"})
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("listen for Wayland connection: %v", err)
	}
	accepted := make(chan struct{})
	peerRead := make(chan []byte, 1)
	go func() {
		peer, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			peerRead <- nil
			return
		}
		defer peer.Close()
		close(accepted)
		buf := make([]byte, 32)
		n, _ := peer.Read(buf)
		peerRead <- append([]byte(nil), buf[:n]...)
	}()
	ctx, err := Connect(filepath.Join(dir, "wayland.sock"))
	if err != nil {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
		t.Fatalf("connect to Wayland test peer: %v", err)
	}
	t.Cleanup(func() {
		_ = ctx.Close()
		_ = listener.Close()
		_ = os.RemoveAll(dir)
	})
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("Wayland peer did not accept the connection")
	}
	return ctx, peerRead
}
