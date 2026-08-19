package screen

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

type extCaptureTestCtx struct {
	nextID uint32
	msgs   [][]byte
}

func (c *extCaptureTestCtx) Register(p wl.Proxy) {
	c.nextID++
	p.SetID(c.nextID)
	p.SetCtx(c)
}

func (c *extCaptureTestCtx) SetProxy(id uint32, p wl.Proxy) {
	p.SetID(id)
	p.SetCtx(c)
}

func (c *extCaptureTestCtx) Unregister(p wl.Proxy) {}

func (c *extCaptureTestCtx) WriteMsg(data, _ []byte) error {
	msg := make([]byte, len(data))
	copy(msg, data)
	c.msgs = append(c.msgs, msg)
	return nil
}

func (c *extCaptureTestCtx) Dispatch() error { return nil }
func (c *extCaptureTestCtx) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.WriteMsg(data, oob)
}
func (c *extCaptureTestCtx) DispatchContext(ctx context.Context) error { return ctx.Err() }
func (c *extCaptureTestCtx) WithOperationContext(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
}
func (c *extCaptureTestCtx) Close() error { return nil }

func TestExtCaptureAvailabilityRequiresOutputSourceManager(t *testing.T) {
	t.Run("missing copy manager", func(t *testing.T) {
		ok, reason := extCaptureAvailable(map[string]bool{
			"ext_output_image_capture_source_manager_v1": true,
		})
		if ok {
			t.Fatal("extCaptureAvailable() = true, want false")
		}
		if reason != "ext_image_copy_capture_manager_v1 not advertised" {
			t.Fatalf("reason = %q", reason)
		}
	})

	t.Run("missing output source manager", func(t *testing.T) {
		ok, reason := extCaptureAvailable(map[string]bool{
			"ext_image_copy_capture_manager_v1": true,
		})
		if ok {
			t.Fatal("extCaptureAvailable() = true, want false")
		}
		if reason != "ext_output_image_capture_source_manager_v1 not advertised" {
			t.Fatalf("reason = %q", reason)
		}
	})

	t.Run("full protocol stack", func(t *testing.T) {
		ok, reason := extCaptureAvailable(map[string]bool{
			"ext_image_copy_capture_manager_v1":          true,
			"ext_output_image_capture_source_manager_v1": true,
		})
		if !ok {
			t.Fatal("extCaptureAvailable() = false, want true")
		}
		if reason == "" {
			t.Fatal("expected non-empty reason")
		}
	})
}

func TestExtCaptureProtocolOpcodes(t *testing.T) {
	ctx := &extCaptureTestCtx{}

	if err := sendExtOutputCreateSource(context.Background(), ctx, 10, 20, 30); err != nil {
		t.Fatalf("sendExtOutputCreateSource: %v", err)
	}
	if err := sendExtCreateSession(context.Background(), ctx, 11, 21, 31); err != nil {
		t.Fatalf("sendExtCreateSession: %v", err)
	}
	if err := sendExtCreateFrame(context.Background(), ctx, 12, 22); err != nil {
		t.Fatalf("sendExtCreateFrame: %v", err)
	}
	if err := sendExtAttachBuffer(context.Background(), ctx, 13, 23); err != nil {
		t.Fatalf("sendExtAttachBuffer: %v", err)
	}
	if err := sendExtDamageBuffer(context.Background(), ctx, 14, 640, 480); err != nil {
		t.Fatalf("sendExtDamageBuffer: %v", err)
	}
	if err := sendExtCapture(context.Background(), ctx, 15); err != nil {
		t.Fatalf("sendExtCapture: %v", err)
	}

	tests := []struct {
		name      string
		msg       []byte
		senderID  uint32
		opcode    uint32
		bodyWords []uint32
	}{
		{"create source", ctx.msgs[0], 10, 0, []uint32{20, 30}},
		{"create session", ctx.msgs[1], 11, 0, []uint32{21, 31, 0}},
		{"create frame", ctx.msgs[2], 12, 0, []uint32{22}},
		{"attach buffer", ctx.msgs[3], 13, 1, []uint32{23}},
		{"damage buffer", ctx.msgs[4], 14, 2, []uint32{0, 0, 640, 480}},
		{"capture", ctx.msgs[5], 15, 3, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := wl.Uint32(tc.msg[0:4]); got != tc.senderID {
				t.Fatalf("sender = %d, want %d", got, tc.senderID)
			}
			sizeOpcode := wl.Uint32(tc.msg[4:8])
			if got := sizeOpcode & 0xffff; got != tc.opcode {
				t.Fatalf("opcode = %d, want %d", got, tc.opcode)
			}
			if got := int(sizeOpcode >> 16); got != len(tc.msg) {
				t.Fatalf("size = %d, want %d", got, len(tc.msg))
			}
			for i, want := range tc.bodyWords {
				if got := wl.Uint32(tc.msg[8+i*4:]); got != want {
					t.Fatalf("word %d = %d, want %d", i, got, want)
				}
			}
		})
	}
}

func TestExtCaptureCleanupRequestUsesIndependentContext(t *testing.T) {
	ctx := &extCaptureTestCtx{}
	captureCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sendWaylandRequest(captureCtx, ctx, 42, 0, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture request error = %v, want context.Canceled", err)
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
	defer cleanupCancel()
	if err := sendWaylandRequest(cleanupCtx, ctx, 42, 0, nil); err != nil {
		t.Fatalf("independent cleanup request: %v", err)
	}
	if len(ctx.msgs) != 1 {
		t.Fatalf("cleanup writes = %d, want 1", len(ctx.msgs))
	}
}

func TestExtCaptureCloseCancelsBlockedCapture(t *testing.T) { //nolint:gocyclo // exercises the complete blocked-capture shutdown sequence.
	sock := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()

	serverReady := make(chan struct{})
	captureSent := make(chan struct{})
	releaseServer := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		close(serverReady)

		// The first request is the session round-trip. Reply with valid
		// constraints and the callback completion so capture can proceed to
		// its genuinely blocking frame dispatch.
		var request [12]byte
		if _, readErr := io.ReadFull(conn, request[:]); readErr != nil {
			return
		}
		callbackID := wl.Uint32(request[8:12])
		if writeErr := writeExtTestEvent(conn, 3, 0, wlUint32Payload(1, 1)); writeErr != nil {
			return
		}
		if writeErr := writeExtTestEvent(conn, 3, 1, wlUint32Payload(0)); writeErr != nil {
			return
		}
		if writeErr := writeExtTestEvent(conn, callbackID, 0, nil); writeErr != nil {
			return
		}

		for {
			var header [8]byte
			if _, readErr := io.ReadFull(conn, header[:]); readErr != nil {
				return
			}
			size := int(wl.Uint32(header[4:8])>>16) - 8
			if size < 0 {
				return
			}
			body := make([]byte, size)
			if _, readErr := io.ReadFull(conn, body); readErr != nil {
				return
			}
			if wl.Uint32(header[4:8])&0xffff == 3 {
				close(captureSent)
				<-releaseServer
				return
			}
		}
	}()

	ctx, err := wl.Connect(sock)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	<-serverReady
	sessProxy := &wlRawProxy{}
	shm := &wl.Shm{}
	ctx.Register(shm)
	ctx.Register(sessProxy)
	b := &ExtCaptureBackend{
		session:     &wl.Session{Sock: sock, Ctx: ctx, Display: wl.NewDisplay(ctx)},
		shm:         shm,
		sessProxy:   sessProxy,
		outputScale: 1,
	}

	grabDone := make(chan error, 1)
	go func() {
		_, grabErr := b.GrabFullHash(context.Background())
		grabDone <- grabErr
	}()
	<-captureSent

	closeDone := make(chan error, 2)
	go func() { closeDone <- b.Close() }()
	go func() { closeDone <- b.Close() }()
	for i := 0; i < 2; i++ {
		select {
		case closeErr := <-closeDone:
			if closeErr != nil {
				t.Fatalf("Close: %v", closeErr)
			}
		case <-time.After(time.Second):
			t.Fatal("Close did not cancel the blocked capture")
		}
	}

	select {
	case grabErr := <-grabDone:
		if !errors.Is(grabErr, context.Canceled) {
			t.Fatalf("GrabFullHash error = %v, want context.Canceled", grabErr)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked capture did not exit after Close")
	}
	close(releaseServer)
	<-serverDone

	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := b.GrabFullHash(context.Background()); err == nil {
		t.Fatal("GrabFullHash succeeded after Close")
	}
}

func writeExtTestEvent(w io.Writer, sender, opcode uint32, body []byte) error {
	msg := make([]byte, 8+len(body))
	wl.PutUint32(msg[0:4], sender)
	wl.PutUint32(msg[4:8], uint32(len(msg))<<16|opcode)
	copy(msg[8:], body)
	_, err := w.Write(msg)
	return err
}
