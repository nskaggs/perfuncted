package screen

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/find"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"github.com/nskaggs/perfuncted/v2/internal/wl"
	"image"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestExtCaptureReusesAndReplacesCachedBufferByGeometry(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		sizes := [][2]uint32{{2, 2}, {2, 2}, {3, 2}}
		syncs, captures := 0, 0
		var frameID uint32
		for {
			var header [8]byte
			if _, err := io.ReadFull(conn, header[:]); err != nil {
				if errors.Is(err, io.EOF) && syncs == len(sizes) && captures == len(sizes) {
					serverDone <- nil
				} else {
					serverDone <- err
				}
				return
			}
			size := int(wl.Uint32(header[4:]) >> 16)
			if size < 8 || size > 4096 {
				serverDone <- io.ErrUnexpectedEOF
				return
			}
			body := make([]byte, size-8)
			if _, err := io.ReadFull(conn, body); err != nil {
				serverDone <- err
				return
			}
			sender, opcode := wl.Uint32(header[:4]), wl.Uint32(header[4:])&0xffff
			if sender == 1 && opcode == 0 {
				if len(body) != 4 || syncs >= len(sizes) {
					serverDone <- io.ErrUnexpectedEOF
					return
				}
				callback := wl.Uint32(body)
				constraints := []struct {
					sender, opcode uint32
					body           []byte
				}{
					{3, 0, wlUint32Payload(sizes[syncs][0], sizes[syncs][1])},
					{3, 1, wlUint32Payload(0)},
					{callback, 0, nil},
				}
				for _, event := range constraints {
					if err := writeExtTestEvent(conn, event.sender, event.opcode, event.body); err != nil {
						serverDone <- err
						return
					}
				}
				syncs++
				continue
			}

			if sender == 3 && opcode == 0 {
				if len(body) != 4 {
					serverDone <- io.ErrUnexpectedEOF
					return
				}
				frameID = wl.Uint32(body)
				continue
			}
			if frameID != 0 && sender == frameID && opcode == 3 {
				if err := writeExtTestEvent(conn, sender, 3, nil); err != nil {
					serverDone <- err
					return
				}
				captures++
				frameID = 0
			}
		}
	}()

	wlctx, err := wl.Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	shm := &wl.Shm{}
	session := &wlRawProxy{}
	wlctx.Register(shm)
	wlctx.Register(session)
	if shm.ID() != 2 || session.ID() != 3 {
		t.Fatalf("unexpected fixture objects: shm=%d session=%d", shm.ID(), session.ID())
	}
	backend := &ExtCaptureBackend{
		session:     &wl.Session{Sock: socket, Ctx: wlctx, Display: wl.NewDisplay(wlctx)},
		shm:         shm,
		sessProxy:   session,
		outputScale: 1,
	}
	defer func() {
		if err := backend.Close(); err != nil {
			t.Errorf("close capture backend: %v", err)
		}
	}()

	capture := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := backend.GrabFullHash(ctx); err != nil {
			t.Fatalf("capture failed: %v", err)
		}
	}

	capture()
	firstFile := backend.cachedFd
	if firstFile == nil || len(backend.cachedBuf) == 0 {
		t.Fatal("first capture did not retain its shared-memory mapping")
	}
	firstBuffer := &backend.cachedBuf[0]

	capture()
	if backend.cachedFd != firstFile || &backend.cachedBuf[0] != firstBuffer {
		t.Fatal("same-size capture replaced its cached shared-memory mapping")
	}

	capture()
	if backend.cachedFd == firstFile || backend.cachedBI.width != 3 || backend.cachedBI.height != 2 {
		t.Fatalf("changed-size capture retained stale mapping: file=%p geometry=%dx%d", backend.cachedFd, backend.cachedBI.width, backend.cachedBI.height)
	}
	if _, err := firstFile.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("replaced mapping descriptor remained open: %v", err)
	}

	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("synthetic compositor did not observe session teardown")
	}
}

// A canceled capture may not consult an uninitialized compositor session,
// publish an image, or return a misleading backend-configuration error.
func TestExtCaptureCancelledRequestsFailBeforeBackendOrPixelAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, operation := range []struct {
		name string
		run  func(*ExtCaptureBackend) error
	}{
		{"full screen", func(b *ExtCaptureBackend) error {
			hash, err := b.GrabFullHash(ctx)
			if hash != 0 {
				t.Fatalf("canceled full frame disclosed hash %x", hash)
			}
			return err
		}},
		{"region", func(b *ExtCaptureBackend) error {
			hash, err := b.GrabRegionHash(ctx, image.Rect(0, 0, 32, 32))
			if hash != 0 {
				t.Fatalf("canceled region disclosed hash %x", hash)
			}
			return err
		}},
		{"image", func(b *ExtCaptureBackend) error {
			img, err := b.Grab(ctx, image.Rect(0, 0, 32, 32))
			if img != nil {
				t.Fatalf("canceled capture disclosed image %v", img)
			}
			return err
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(&ExtCaptureBackend{}); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled capture returned %v", err)
			}
		})
	}
}

// A shutdown backend must not reopen a screenshot session or accidentally
// substitute an invalid-session error for its authoritative closed state.
func TestExtCaptureClosedBackendRejectsSubsequentReads(t *testing.T) {
	backend := &ExtCaptureBackend{}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Grab(context.Background(), image.Rect(0, 0, 1, 1)); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed capture returned %v", err)
	}
	if _, err := backend.GrabFullHash(context.Background()); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed hash returned %v", err)
	}
	if _, err := backend.GrabRegionHash(context.Background(), image.Rect(0, 0, 1, 1)); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed region hash returned %v", err)
	}
}

// An invalid output scale is not authority to reject a legitimate request or
// remap it to a different pixel region; the documented fallback is scale one.
func TestExtCaptureInvalidHashScaleKeepsLogicalRegion(t *testing.T) {
	request := image.Rect(4, 5, 7, 9)
	exact, err := croppedHashRegion(request, 1, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, scale := range []int{0, -1, -999} {
		got, err := croppedHashRegion(request, scale, 20, 20)
		if err != nil || got != exact {
			t.Fatalf("scale=%d redirected region %v: %v", scale, got, err)
		}
	}
	if _, err := croppedHashRegion(image.Rect(30, 30, 33, 33), 0, 20, 20); !errors.Is(err, find.ErrEmptyRegion) {
		t.Fatalf("out-of-frame region accepted: %v", err)
	}
}

func TestExtCaptureRejectsUntrustedWaylandConstraintsBeforeSharedMemoryAllocation(t *testing.T) {
	for _, tc := range []struct {
		name          string
		sessionEvents []struct {
			opcode uint32
			data   []byte
		}
		want string
	}{
		{"zero size", []struct {
			opcode uint32
			data   []byte
		}{{1, wlUint32Payload(0)}}, "buffer size"},
		{"empty size payload", []struct {
			opcode uint32
			data   []byte
		}{{0, []byte{2, 0, 0, 0}}}, "malformed"},
		{"empty format payload", []struct {
			opcode uint32
			data   []byte
		}{{0, wlUint32Payload(2, 2)}, {1, []byte{}}}, "malformed"},
		{"session stopped", []struct {
			opcode uint32
			data   []byte
		}{{0, wlUint32Payload(2, 2)}, {1, wlUint32Payload(0)}, {5, nil}}, "stopped"},
		{"oversized width", []struct {
			opcode uint32
			data   []byte
		}{{0, wlUint32Payload(0x40000000, 2)}, {1, wlUint32Payload(0)}}, "width is too large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := testutil.ShortSocketPath(t, "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, lintShadow56 := listener.AcceptUnix()
				if lintShadow56 != nil {
					done <- lintShadow56
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
				first := make([]byte, 12)
				if _, lintShadow64 := io.ReadFull(conn, first); lintShadow64 != nil {
					done <- lintShadow64
					return
				}
				callback := wl.Uint32(first[8:])
				for _, event := range tc.sessionEvents {
					if lintShadow70 := writeExtTestEvent(conn, 3, event.opcode, event.data); lintShadow70 != nil {
						done <- lintShadow70
						return
					}
				}
				if lintShadow75 := writeExtTestEvent(conn, callback, 0, nil); lintShadow75 != nil {
					done <- lintShadow75
					return
				}
				for {
					var h [8]byte
					if _, lintShadow81 := io.ReadFull(conn, h[:]); lintShadow81 != nil {
						if errors.Is(lintShadow81, io.EOF) {
							done <- nil
						} else {
							done <- lintShadow81
						}
						return
					}
					size := int(wl.Uint32(h[4:]) >> 16)
					if size < 8 || size > 2048 {
						done <- fmt.Errorf("untrusted wire size %d", size)
						return
					}
					body := make([]byte, size-8)
					if _, lintShadow95 := io.ReadFull(conn, body); lintShadow95 != nil {
						done <- lintShadow95
						return
					}
					// A malformed or impossible constraint must never create a buffer.
					if sender := wl.Uint32(h[:4]); sender == 2 {
						done <- fmt.Errorf("invalid capture allocated shm buffer after constraints failure")
						return
					}
				}
			}()
			wlctx, err := wl.Connect(socket)
			if err != nil {
				t.Fatal(err)
			}
			shm := &wl.Shm{}
			sess := &wlRawProxy{}
			wlctx.Register(shm)
			wlctx.Register(sess)
			if shm.ID() != 2 || sess.ID() != 3 {
				t.Fatalf("unexpected fixture IDs %d,%d", shm.ID(), sess.ID())
			}
			b := &ExtCaptureBackend{session: &wl.Session{Sock: socket, Ctx: wlctx, Display: wl.NewDisplay(wlctx)}, shm: shm, sessProxy: sess}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, grabErr := b.GrabFullHash(ctx)
			cancel()
			if grabErr == nil || !strings.Contains(grabErr.Error(), tc.want) {
				t.Fatalf("untrusted constraints %s returned %v", tc.name, grabErr)
			}
			if b.cachedBuf != nil || b.cachedFd != nil {
				t.Fatal("bad constraints allocated shared memory")
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case serverErr := <-done:
				if serverErr != nil {
					t.Fatalf("synthetic compositor: %v", serverErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("backend failed to close rejected session")
			}
		})
	}
}

// An already canceled caller cannot initiate a compositor connection, even
// if its socket path is missing or invalid. Cancellation wins before display
// discovery and must not be converted into a configuration error.
func TestExtCaptureConstructorPreCanceledBeforeSocketDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, sock := range []string{"", "/nonexistent-perfuncted-wayland-socket"} {
		b, err := NewExtCaptureBackendForSocketContext(ctx, sock)
		if b != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled constructor socket=%q backend=%v err=%v", sock, b, err)
		}
	}
}

func extConstructorGlobal(name uint32, iface string, version uint32) []byte {
	n := len(iface) + 1
	pad := (n + 3) &^ 3
	data := make([]byte, 8+pad+4)
	wl.PutUint32(data, name)
	wl.PutUint32(data[4:], uint32(n))
	copy(data[8:], iface)
	wl.PutUint32(data[8+pad:], version)
	return data
}

type extConstructorAdvertisedGlobal struct {
	name    uint32
	iface   string
	version uint32
}

func serveExtConstructorPeer(conn *net.UnixConn, skip string) error {
	return serveExtConstructorPeerWithGlobals(conn, skip, nil)
}

func serveExtConstructorPeerWithGlobals(conn *net.UnixConn, skip string, additional []extConstructorAdvertisedGlobal) error {
	return serveExtConstructorPeerWithOutputScale(conn, skip, additional, 0)
}

func serveExtConstructorPeerWithOutputScale(conn *net.UnixConn, skip string, additional []extConstructorAdvertisedGlobal, outputScale uint32) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	var registryID uint32
	gotBinds := map[uint32]uint32{}
	syncs := 0
	globals := append([]extConstructorAdvertisedGlobal{
		{21, "ext_image_copy_capture_manager_v1", 1}, {22, "ext_output_image_capture_source_manager_v1", 1},
		{23, "wl_output", 4}, {24, "wl_shm", 1},
	}, additional...)
	for {
		var h [8]byte
		if _, err := io.ReadFull(conn, h[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("peer read: %w", err)
		}
		size := int(wl.Uint32(h[4:]) >> 16)
		if size < 8 || size > 16384 {
			return fmt.Errorf("invalid request length %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		sender, opcode := wl.Uint32(h[:4]), wl.Uint32(h[4:])&0xffff
		if sender == 1 && opcode == 1 {
			registryID = wl.Uint32(body)
			continue
		}
		if sender == 1 && opcode == 0 {
			callback := wl.Uint32(body)
			syncs++
			if syncs == 1 {
				for _, global := range globals {
					if global.iface == skip {
						continue
					}
					if err := writeExtTestEvent(conn, registryID, 0, extConstructorGlobal(global.name, global.iface, global.version)); err != nil {
						return err
					}
				}
			}
			if err := writeExtTestEvent(conn, callback, 0, wlUint32Payload(0)); err != nil {
				return err
			}
			continue
		}
		if sender == registryID && opcode == 0 {
			if size < 20 {
				return fmt.Errorf("short registry.bind %x", body)
			}
			name, version, newID := wl.Uint32(body), wl.Uint32(body[len(body)-8:]), wl.Uint32(body[len(body)-4:])
			if (name == 23 && version > 2) || (name == 21 && version > 1) || (name == 22 && version > 1) {
				return fmt.Errorf("invalid bound version %d for global %d", version, name)
			}
			gotBinds[name] = newID
			if name == 23 && outputScale > 0 {
				if err := writeExtTestEvent(conn, newID, 3, wlUint32Payload(outputScale)); err != nil {
					return err
				}
			}
			continue
		}
		if gotBinds[22] != 0 && sender == gotBinds[22] && opcode == 0 {
			if len(body) < 8 {
				return fmt.Errorf("invalid source creation %x", body)
			}
			continue
		}
		if gotBinds[21] != 0 && sender == gotBinds[21] && opcode == 0 {
			if len(body) < 12 {
				return fmt.Errorf("invalid copy session creation %x", body)
			}
			continue
		}
	}
}
func TestExtCaptureRealSyntheticConstructorBindsOnlyAdvertisedProtocols(t *testing.T) {
	cases := []struct{ name, skip, wantErr string }{
		{"full capture protocol stack", "", ""},
		{"copy manager absent", "ext_image_copy_capture_manager_v1", "ext_image_copy_capture_manager_v1"},
		{"output source manager absent", "ext_output_image_capture_source_manager_v1", "ext_output_image_capture_source_manager_v1"},
		{"output absent", "wl_output", "wl_output"},
		{"shared memory absent", "wl_shm", "wl_shm"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			socket := testutil.ShortSocketPath(t, "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			server := make(chan error, 1)
			go func() {
				conn, lintShadow123 := listener.AcceptUnix()
				if lintShadow123 != nil {
					server <- lintShadow123
					return
				}
				server <- serveExtConstructorPeer(conn, tc.skip)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			b, err := NewExtCaptureBackendForSocketContext(ctx, socket)
			if tc.wantErr != "" {
				if b != nil || err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("missing %s accepted: backend=%v err=%v", tc.skip, b, err)
				}
			} else {
				if err != nil {
					t.Fatalf("fully capable compositor failed: %v", err)
				}
				if b.session == nil || b.shm == nil || b.outputProxy == nil || b.mgrProxy == nil || b.sourceMgrProxy == nil || b.sourceProxy == nil || b.sessProxy == nil {
					t.Fatal("successful constructor returned incomplete capture backend")
				}
				if !b.CanonicalHashing() {
					t.Fatal("external capture did not advertise canonical hashes")
				}
				if err := b.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case result := <-server:
				if result != nil {
					t.Fatalf("synthetic compositor rejected wire requests: %v", result)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("backend failed to release compositor connection")
			}
		})
	}
}

func TestExtCaptureRealWaylandFailedFrameNeverReturnsImageOrHash(t *testing.T) {
	for _, op := range []struct {
		name string
		run  func(context.Context, *ExtCaptureBackend) error
	}{
		{"image", func(ctx context.Context, b *ExtCaptureBackend) error {
			img, err := b.Grab(ctx, image.Rectangle{})
			if img != nil {
				return fmt.Errorf("frame failure returned image %v", img)
			}
			return err
		}},
		{"full hash", func(ctx context.Context, b *ExtCaptureBackend) error {
			h, err := b.GrabFullHash(ctx)
			if h != 0 {
				return fmt.Errorf("frame failure returned hash %d", h)
			}
			return err
		}},
		{"region hash", func(ctx context.Context, b *ExtCaptureBackend) error {
			h, err := b.GrabRegionHash(ctx, image.Rect(0, 0, 1, 1))
			if h != 0 {
				return fmt.Errorf("frame failure returned region hash %d", h)
			}
			return err
		}},
	} {
		t.Run(op.name, func(t *testing.T) {
			sock := testutil.ShortSocketPath(t, "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() {
				conn, lintShadow54 := listener.AcceptUnix()
				if lintShadow54 != nil {
					serverDone <- lintShadow54
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				first := make([]byte, 12)
				if _, lintShadow62 := io.ReadFull(conn, first); lintShadow62 != nil {
					serverDone <- lintShadow62
					return
				}
				callback := wl.Uint32(first[8:])
				for _, event := range []struct {
					sender, opcode uint32
					body           []byte
				}{
					{3, 0, wlUint32Payload(2, 2)}, {3, 1, wlUint32Payload(0)}, {callback, 0, nil},
				} {
					if lintShadow73 := writeExtTestEvent(conn, event.sender, event.opcode, event.body); lintShadow73 != nil {
						serverDone <- lintShadow73
						return
					}
				}
				var frameID uint32
				sawCapture := false
				for {
					var h [8]byte
					if _, lintShadow82 := io.ReadFull(conn, h[:]); lintShadow82 != nil {
						if errors.Is(lintShadow82, io.EOF) && sawCapture {
							serverDone <- nil
						} else {
							serverDone <- fmt.Errorf("post-capture EOF=%w captured=%v", lintShadow82, sawCapture)
						}
						return
					}
					size := int(wl.Uint32(h[4:]) >> 16)
					if size < 8 || size > 8192 {
						serverDone <- fmt.Errorf("malformed frame request size %d", size)
						return
					}
					body := make([]byte, size-8)
					if _, lintShadow96 := io.ReadFull(conn, body); lintShadow96 != nil {
						serverDone <- lintShadow96
						return
					}
					sender, opcode := wl.Uint32(h[:4]), wl.Uint32(h[4:])&0xffff
					if sender == 3 && opcode == 0 && len(body) >= 4 {
						frameID = wl.Uint32(body)
					}
					if frameID != 0 && sender == frameID && opcode == 3 {
						sawCapture = true
						if lintShadow106 := writeExtTestEvent(conn, frameID, 4, nil); lintShadow106 != nil {
							serverDone <- lintShadow106
							return
						}
					}
				}
			}()
			ctx, err := wl.Connect(sock)
			if err != nil {
				t.Fatal(err)
			}
			shm := &wl.Shm{}
			sess := &wlRawProxy{}
			ctx.Register(shm)
			ctx.Register(sess)
			backend := &ExtCaptureBackend{session: &wl.Session{Sock: sock, Ctx: ctx, Display: wl.NewDisplay(ctx)}, shm: shm, sessProxy: sess, outputScale: 1}
			captureCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err = op.run(captureCtx, backend)
			cancel()
			if err == nil || !strings.Contains(err.Error(), "frame failed") {
				t.Fatalf("failed frame not recognized: %v", err)
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case serverErr := <-serverDone:
				if serverErr != nil {
					t.Fatalf("synthetic compositor: %v", serverErr)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("frame capture did not close socket")
			}
		})
	}
}

// A capture canceled while consuming the compositor's completed pixel buffer
// cannot report success: the caller no longer owns a publishable result.
func TestExtCaptureFrameProcessingCancellationCannotPublishSuccess(t *testing.T) {
	runtime := testutil.ShortSocketDir(t)
	sock := filepath.Join(runtime, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		first := make([]byte, 12)
		if _, err := io.ReadFull(conn, first); err != nil {
			serverDone <- err
			return
		}
		callback := wl.Uint32(first[8:12])
		for _, ev := range []struct {
			sender, opcode uint32
			body           []byte
		}{
			{3, 0, wlUint32Payload(2, 2)},
			{3, 1, wlUint32Payload(0)},
			{callback, 0, nil},
		} {
			if err := writeExtTestEvent(conn, ev.sender, ev.opcode, ev.body); err != nil {
				serverDone <- err
				return
			}
		}
		var frame uint32
		for {
			var header [8]byte
			if _, err := io.ReadFull(conn, header[:]); err != nil {
				serverDone <- err
				return
			}
			length := int(wl.Uint32(header[4:8])>>16) - 8
			if length < 0 || length > 4096 {
				serverDone <- io.ErrUnexpectedEOF
				return
			}
			body := make([]byte, length)
			if _, err := io.ReadFull(conn, body); err != nil {
				serverDone <- err
				return
			}
			sender, opcode := wl.Uint32(header[:4]), wl.Uint32(header[4:8])&0xffff
			if sender == 3 && opcode == 0 && len(body) >= 4 {
				frame = wl.Uint32(body[:4])
			}
			if frame != 0 && sender == frame && opcode == 3 {
				serverDone <- writeExtTestEvent(conn, frame, 3, nil)
				return
			}
		}
	}()
	ctx, err := wl.Connect(sock)
	if err != nil {
		t.Fatal(err)
	}
	shm := &wl.Shm{}
	sess := &wlRawProxy{}
	ctx.Register(shm)
	ctx.Register(sess)
	backend := &ExtCaptureBackend{session: &wl.Session{Sock: sock, Ctx: ctx, Display: wl.NewDisplay(ctx)}, shm: shm, sessProxy: sess, outputScale: 1}
	captureCtx, cancel := context.WithCancel(context.Background())
	processed := false
	err = backend.grabInternal(captureCtx, func(pixels []byte, w, h, stride int) error { processed = true; cancel(); return nil })
	if !processed {
		t.Fatal("capture never processed a completed frame")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled frame processing was acknowledged: %v", err)
	}
	if closeErr := backend.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatal(serverErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("compositor did not finish")
	}
}

// Only the zero rectangle means "capture the entire output". An empty
// nonzero region must be rejected before any capture IPC is attempted,
// otherwise a malformed caller region silently turns into a full capture.
func TestExtCaptureRejectsNonzeroEmptyRegionsBeforeCapture(t *testing.T) {
	for _, rect := range []image.Rectangle{
		image.Rect(1, 1, 1, 1),
		image.Rectangle{Min: image.Pt(8, 8), Max: image.Pt(2, 2)},
		image.Rect(-3, 4, 7, 4),
	} {
		t.Run(rect.String(), func(t *testing.T) {
			b := &ExtCaptureBackend{}
			img, err := b.Grab(context.Background(), rect)
			if img != nil || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("Grab(%v) returned image=%v err=%v; want invalid region", rect, img, err)
			}
			hash, err := b.GrabRegionHash(context.Background(), rect)
			if hash != 0 || err == nil || !strings.Contains(err.Error(), "invalid empty capture region") {
				t.Errorf("GrabRegionHash(%v) returned hash=%08x err=%v; want invalid region", rect, hash, err)
			}
		})
	}
}

// Capture converts logical regions to physical pixels using wl_output.scale.
// Malformed zero or signed-negative compositor events must never replace a
// previously established scale and silently redirect the capture crop.
func TestExtCapturePreservesAuthorizedScaleAfterMalformedOutputEvent(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value uint32
	}{
		{"zero", 0},
		{"negative one", ^uint32(0)},
		{"minimum signed", uint32(1) << 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &ExtCaptureBackend{}
			initial := make([]byte, 4)
			wl.PutUint32(initial, 2)
			b.applyOutputEvent(wlOutputScaleOpcode, initial)
			if b.outputScale != 2 {
				t.Fatalf("initial scale=%d", b.outputScale)
			}
			malformed := make([]byte, 4)
			wl.PutUint32(malformed, tc.value)
			b.applyOutputEvent(wlOutputScaleOpcode, malformed)
			if b.outputScale != 2 {
				t.Fatalf("invalid %08x scale replaced confirmed value: %d", tc.value, b.outputScale)
			}
		})
	}
}

func TestExtCaptureConstructorAppliesAdvertisedOutputScaleEvent(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			server <- err
			return
		}
		server <- serveExtConstructorPeerWithOutputScale(conn, "", nil, 2)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	backend, err := NewExtCaptureBackendForSocketContext(ctx, socket)
	if err != nil {
		t.Fatalf("construct ext capture backend: %v", err)
	}
	if backend.outputScale != 0 {
		t.Fatalf("output scale updated before dispatch: %d", backend.outputScale)
	}
	if err := backend.session.Display.RoundTripContext(ctx); err != nil {
		_ = backend.Close()
		t.Fatalf("dispatch compositor output scale: %v", err)
	}
	if backend.outputScale != 2 {
		t.Fatalf("compositor output scale = %d, want 2", backend.outputScale)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close backend: %v", err)
	}
	select {
	case err := <-server:
		if err != nil {
			t.Fatalf("synthetic compositor rejected wire requests: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("backend failed to release compositor connection")
	}
}

func TestExtCaptureFailsClosedOnSessionRoundTripAndBufferGeometryErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		disconnect bool
		width      uint32
		height     uint32
		want       string
	}{
		{name: "session round-trip disconnect", disconnect: true, want: "session round-trip"},
		{name: "buffer allocation overflow", width: 1, height: ^uint32(0), want: "invalid buffer geometry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := testutil.ShortSocketPath(t, "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()

			serverDone := make(chan error, 1)
			go func() {
				conn, err := listener.AcceptUnix()
				if err != nil {
					serverDone <- err
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
				request := make([]byte, 12)
				if _, err := io.ReadFull(conn, request); err != nil {
					serverDone <- err
					return
				}
				if tc.disconnect {
					serverDone <- nil
					return
				}
				callback := wl.Uint32(request[8:])
				for _, event := range []struct {
					sender, opcode uint32
					body           []byte
				}{
					{3, 0, wlUint32Payload(tc.width, tc.height)},
					{3, 1, wlUint32Payload(0)},
					{callback, 0, nil},
				} {
					if err := writeExtTestEvent(conn, event.sender, event.opcode, event.body); err != nil {
						serverDone <- err
						return
					}
				}
				var header [8]byte
				if _, err := io.ReadFull(conn, header[:]); err != nil {
					if err == io.EOF {
						serverDone <- nil
					} else {
						serverDone <- err
					}
					return
				}
				serverDone <- fmt.Errorf("invalid geometry sent unexpected Wayland request from object %d", wl.Uint32(header[:4]))
			}()

			wlctx, err := wl.Connect(socket)
			if err != nil {
				t.Fatal(err)
			}
			shm := &wl.Shm{}
			session := &wlRawProxy{}
			wlctx.Register(shm)
			wlctx.Register(session)
			backend := &ExtCaptureBackend{
				session:   &wl.Session{Sock: socket, Ctx: wlctx, Display: wl.NewDisplay(wlctx)},
				shm:       shm,
				sessProxy: session,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			hash, captureErr := backend.GrabFullHash(ctx)
			cancel()
			if hash != 0 || captureErr == nil || !strings.Contains(captureErr.Error(), tc.want) {
				t.Fatalf("invalid capture returned hash=%08x err=%v, want error containing %q", hash, captureErr, tc.want)
			}
			if backend.cachedBuf != nil || backend.cachedFd != nil {
				t.Fatal("failed capture retained a shared-memory mapping")
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-serverDone:
				if err != nil {
					t.Fatalf("synthetic compositor: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("synthetic compositor did not finish the failed capture")
			}
		})
	}
}

func TestExtCaptureDoesNotPublishHashWhenSharedMemoryPoolTransportFails(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		request := make([]byte, 12)
		if _, readErr := io.ReadFull(conn, request); readErr != nil {
			_ = conn.Close()
			serverDone <- fmt.Errorf("read initial sync request: %w", readErr)
			return
		}
		callback := wl.Uint32(request[8:])
		for _, event := range []struct {
			sender, opcode uint32
			body           []byte
		}{
			{3, 0, wlUint32Payload(2, 2)},
			{3, 1, wlUint32Payload(0)},
			{callback, 0, nil},
		} {
			if writeErr := writeExtTestEvent(conn, event.sender, event.opcode, event.body); writeErr != nil {
				_ = conn.Close()
				serverDone <- fmt.Errorf("write session constraints: %w", writeErr)
				return
			}
		}
		if closeErr := conn.Close(); closeErr != nil {
			serverDone <- fmt.Errorf("close after session constraints: %w", closeErr)
			return
		}
		serverDone <- nil
	}()

	wlctx, err := wl.Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	shm := &wl.Shm{}
	session := &wlRawProxy{}
	wlctx.Register(shm)
	wlctx.Register(session)
	if shm.ID() != 2 || session.ID() != 3 {
		t.Fatalf("unexpected fixture object IDs: shm=%d session=%d", shm.ID(), session.ID())
	}
	backend := &ExtCaptureBackend{
		session:   &wl.Session{Sock: socket, Ctx: wlctx, Display: wl.NewDisplay(wlctx)},
		shm:       shm,
		sessProxy: session,
	}

	type captureResult struct {
		hash uint32
		err  error
	}
	captureDone := make(chan captureResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		hash, captureErr := backend.GrabFullHash(ctx)
		captureDone <- captureResult{hash: hash, err: captureErr}
	}()

	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatalf("synthetic compositor did not deliver session constraints: %v", ctx.Err())
	}
	select {
	case got := <-captureDone:
		// The compositor closes after sending constraints. Depending on which
		// queued Wayland request reaches the kernel first, the disconnect can
		// surface during pool creation or a subsequent frame dispatch. The
		// invariant is no published hash after a failed transport.
		transportLost := errors.Is(got.err, io.EOF) || errors.Is(got.err, syscall.EPIPE) || errors.Is(got.err, syscall.ECONNRESET)
		if got.hash != 0 || !transportLost {
			t.Fatalf("failed shared-memory setup published hash=%08x err=%v", got.hash, got.err)
		}
	case <-ctx.Done():
		t.Fatalf("capture did not return after pool transport closed: %v", ctx.Err())
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close after failed pool setup: %v", err)
	}
}

// A synthetic Wayland socket advertises actual session constraints and frame
// readiness, while the backend performs the real shm/pool/frame wire sequence.
func TestExtCaptureSyntheticWaylandCaptureLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name            string
		operation       func(*ExtCaptureBackend) (image.Image, uint32, error)
		wantImage       bool
		unsetScale      bool
		wantEmptyRegion bool
	}{
		{"full image", func(b *ExtCaptureBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}, true, false, false},
		{"cropped image", func(b *ExtCaptureBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rect(1, 1, 2, 2))
			return img, 0, err
		}, true, false, false},
		{"full hash", func(b *ExtCaptureBackend) (image.Image, uint32, error) {
			h, err := b.GrabFullHash(context.Background())
			return nil, h, err
		}, false, false, false},
		{"zero region means full hash", func(b *ExtCaptureBackend) (image.Image, uint32, error) {
			h, err := b.GrabRegionHash(context.Background(), image.Rectangle{})
			return nil, h, err
		}, false, false, false},
		{"region hash", func(b *ExtCaptureBackend) (image.Image, uint32, error) {
			h, err := b.GrabRegionHash(context.Background(), image.Rect(0, 0, 1, 1))
			return nil, h, err
		}, false, false, false},
		{"region entirely outside captured output", func(b *ExtCaptureBackend) (image.Image, uint32, error) {
			h, err := b.GrabRegionHash(context.Background(), image.Rect(400, 500, 402, 503))
			return nil, h, err
		}, false, false, true},
		{"region hash before output scale", func(b *ExtCaptureBackend) (image.Image, uint32, error) {
			h, err := b.GrabRegionHash(context.Background(), image.Rect(0, 0, 1, 1))
			return nil, h, err
		}, false, true, false},
		{"cropped image before output scale", func(b *ExtCaptureBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rect(1, 1, 2, 2))
			return img, 0, err
		}, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sock := testutil.ShortSocketPath(t, "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			serverTrace := make(chan string, 64)
			releaseServer := make(chan struct{})
			go func() {
				conn, acceptErr := listener.AcceptUnix()
				if acceptErr != nil {
					serverDone <- acceptErr
					return
				}
				defer conn.Close()
				_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
				request := make([]byte, 12)
				if _, lintShadow61 := io.ReadFull(conn, request); lintShadow61 != nil {
					serverDone <- lintShadow61
					return
				}
				callbackID := wl.Uint32(request[8:12])
				serverTrace <- fmt.Sprintf("initial display sync callback=%d", callbackID)
				// The test registers wl_shm as object 2 and session as object 3.
				for _, event := range []struct {
					sender, op uint32
					body       []byte
				}{
					{3, 0, wlUint32Payload(2, 2)}, {3, 1, wlUint32Payload(0)}, {callbackID, 0, nil},
				} {
					if lintShadow74 := writeExtTestEvent(conn, event.sender, event.op, event.body); lintShadow74 != nil {
						serverDone <- lintShadow74
						return
					}
				}
				var frameID uint32
				for {
					var header [8]byte
					if _, lintShadow82 := io.ReadFull(conn, header[:]); lintShadow82 != nil {
						serverDone <- lintShadow82
						return
					}
					size := int(wl.Uint32(header[4:8])>>16) - 8
					if size < 0 || size > 4096 {
						serverDone <- io.ErrUnexpectedEOF
						return
					}
					body := make([]byte, size)
					if _, lintShadow92 := io.ReadFull(conn, body); lintShadow92 != nil {
						serverDone <- lintShadow92
						return
					}
					sender := wl.Uint32(header[:4])
					opcode := wl.Uint32(header[4:8]) & 0xffff
					serverTrace <- fmt.Sprintf("request sender=%d opcode=%d size=%d", sender, opcode, size)
					if sender == 3 && opcode == 0 && len(body) >= 4 {
						frameID = wl.Uint32(body[:4])
					}
					if frameID != 0 && sender == frameID && opcode == 3 {
						lintShadow103 := writeExtTestEvent(conn, frameID, 3, nil)
						serverDone <- lintShadow103
						<-releaseServer
						return
					}
				}
			}()
			ctx, err := wl.Connect(sock)
			if err != nil {
				close(releaseServer)
				t.Fatal(err)
			}
			shm := &wl.Shm{}
			sess := &wlRawProxy{}
			ctx.Register(shm)
			ctx.Register(sess)
			if shm.ID() != 2 || sess.ID() != 3 {
				close(releaseServer)
				t.Fatalf("unexpected fixture objects: shm=%d sess=%d", shm.ID(), sess.ID())
			}
			backend := &ExtCaptureBackend{session: &wl.Session{Sock: sock, Ctx: ctx, Display: wl.NewDisplay(ctx)}, shm: shm, sessProxy: sess, outputScale: 1}
			if tc.unsetScale {
				backend.outputScale = 0 // compositor has not yet announced a scale
			}
			done := make(chan struct{})
			var img image.Image
			var hash uint32
			var captureErr error
			go func() { img, hash, captureErr = tc.operation(backend); close(done) }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				var trace []string
				for len(serverTrace) > 0 {
					trace = append(trace, <-serverTrace)
				}
				t.Fatalf("capture did not complete; trace=%v", trace)
			}
			select {
			case err := <-serverDone:
				if err != nil {
					t.Fatalf("synthetic compositor: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("compositor never saw frame capture")
			}
			close(releaseServer)
			if tc.wantEmptyRegion {
				if !errors.Is(captureErr, find.ErrEmptyRegion) || img != nil || hash != 0 {
					t.Fatalf("out-of-frame region disclosed screenshot/hash: image=%v hash=%08x err=%v", img, hash, captureErr)
				}
			} else if captureErr != nil {
				t.Fatalf("screencopy failed: %v", captureErr)
			}
			if tc.wantEmptyRegion {
				// Error responses must still release the compositor's capture resources.
			} else if tc.wantImage {
				if img == nil {
					t.Fatal("screencopy returned nil")
				}
				if (tc.name == "cropped image" || tc.name == "cropped image before output scale") && img.Bounds().Size() != image.Pt(1, 1) {
					t.Fatalf("cropped image wrong geometry: %v", img.Bounds())
				}
				if tc.name == "full image" && img.Bounds().Size() != image.Pt(2, 2) {
					t.Fatalf("full image wrong geometry: %v", img.Bounds())
				}
			} else {
				rect := image.Rectangle{}
				if tc.name == "region hash" || tc.name == "region hash before output scale" {
					rect = image.Rect(0, 0, 1, 1)
				}
				want, err := find.PixelHashBGRA(make([]byte, 16), 2, 2, 8, rect)
				if err != nil {
					t.Fatal(err)
				}
				if hash != want {
					t.Fatalf("framebuffer hash=%08x, want %08x", hash, want)
				}
			}
			if err := backend.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

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
	sock := testutil.ShortSocketPath(t, "wayland.sock")
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

// wl_output only sends the scale event from version 2, so binding lower leaves
// the output scale unknown and every capture crops at scale one.
func TestWlOutputBindVersionRequestsTheScaleEvent(t *testing.T) {
	cases := []struct {
		name       string
		advertised uint32
		want       uint32
	}{
		{name: "version 1 cannot carry scale", advertised: 1, want: 1},
		{name: "version 2 carries scale", advertised: 2, want: 2},
		{name: "newer versions bind no higher", advertised: 4, want: 2},
		{name: "unspecified", advertised: 0, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wlOutputBindVersion(tc.advertised); got != tc.want {
				t.Fatalf("wlOutputBindVersion(%d) = %d, want %d", tc.advertised, got, tc.want)
			}
		})
	}
}

// The scale event is what turns a logical region into the output's pixel scale,
// so it has to be recorded, and a zero scale normalized to one.
func TestApplyOutputEventRecordsScale(t *testing.T) {
	cases := []struct {
		name    string
		opcode  uint32
		data    []byte
		want    uint32
		comment string
	}{
		{name: "scale 2", opcode: wlOutputScaleOpcode, data: []byte{2, 0, 0, 0}, want: 2},
		{name: "scale 3", opcode: wlOutputScaleOpcode, data: []byte{3, 0, 0, 0}, want: 3},
		{name: "little endian", opcode: wlOutputScaleOpcode, data: []byte{0x02, 0x01, 0, 0}, want: 258},
		{name: "zero normalized to one", opcode: wlOutputScaleOpcode, data: []byte{0, 0, 0, 0}, want: 1},
		{name: "geometry ignored", opcode: 0, data: []byte{9, 0, 0, 0}, want: 0},
		{name: "mode ignored", opcode: 1, data: []byte{2, 0, 0, 0}, want: 0},
		{name: "done ignored", opcode: 2, data: nil, want: 0},
		{name: "short payload ignored", opcode: wlOutputScaleOpcode, data: []byte{2, 0}, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &ExtCaptureBackend{}
			b.applyOutputEvent(tc.opcode, tc.data)
			if b.outputScale != tc.want {
				t.Fatalf("outputScale = %d, want %d", b.outputScale, tc.want)
			}
		})
	}
}

// A recorded scale has to reach the crop, which is the whole point of reading
// the event. With the scale unknown the region is cropped at scale one.
func TestExtCaptureScaleDrivesTheCaptureRegion(t *testing.T) {
	region := image.Rect(0, 0, 100, 100)

	if got := logicalRectToPhysical(region, 1); got != region {
		t.Fatalf("scale 1 region = %v, want %v", got, region)
	}
	if got, want := logicalRectToPhysical(region, 2), image.Rect(0, 0, 200, 200); got != want {
		t.Fatalf("scale 2 region = %v, want %v", got, want)
	}

	b := &ExtCaptureBackend{}
	b.applyOutputEvent(wlOutputScaleOpcode, []byte{2, 0, 0, 0})
	if b.outputScale == 0 {
		t.Fatal("output scale was not recorded")
	}
	got := logicalRectToPhysical(region, int(b.outputScale))
	if want := image.Rect(0, 0, 200, 200); got != want {
		t.Fatalf("region at the recorded scale = %v, want %v", got, want)
	}
}

// A region that covers no pixels of the capture must not hash to zero. Zero is a
// valid checksum for a real region, so a settle loop polling a nonexistent region
// saw a constant value and declared it stable.
func TestCroppedHashRegionRejectsRegionCoveringNoPixels(t *testing.T) {
	const w, h = 100, 50
	inside := image.Rect(0, 0, 10, 10)
	got, err := croppedHashRegion(inside, 1, w, h)
	if err != nil {
		t.Fatalf("a region inside the capture was refused: %v", err)
	}
	if got.Empty() {
		t.Fatal("a region inside the capture resolved to nothing")
	}

	// Wholly outside the capture. The cause is matchable so a caller can tell a
	// region that covers nothing from a backend that failed for another reason.
	if _, err := croppedHashRegion(image.Rect(500, 500, 520, 520), 1, w, h); !errors.Is(err, find.ErrEmptyRegion) {
		t.Fatalf("a region wholly outside the capture: error = %v, want ErrEmptyRegion", err)
	}
	// Degenerate: no pixels at all.
	if _, err := croppedHashRegion(image.Rectangle{}, 1, w, h); err == nil {
		t.Fatal("an empty region was accepted")
	}
	// Entirely outside in the scaled space only.
	if _, err := croppedHashRegion(image.Rect(10, 10, 20, 20), 8, w, h); err == nil {
		t.Fatal("a region pushed outside the capture by scaling was accepted")
	}
}

func TestExtCaptureIncompleteSessionFailsClosedWithoutPanic(t *testing.T) {
	for _, b := range []*ExtCaptureBackend{nil, {}, {session: nil}} {
		for _, op := range []struct {
			name string
			run  func() error
		}{
			{"grab", func() error {
				img, err := b.Grab(context.Background(), image.Rect(1, 2, 3, 4))
				if img != nil {
					t.Fatalf("invalid backend returned image %v", img)
				}
				return err
			}},
			{"full hash", func() error { _, err := b.GrabFullHash(context.Background()); return err }},
			{"region hash", func() error { _, err := b.GrabRegionHash(context.Background(), image.Rect(1, 2, 3, 4)); return err }},
		} {
			t.Run(op.name, func(t *testing.T) {
				err := op.run()
				if err == nil || (!strings.Contains(err.Error(), "not initialised") && !strings.Contains(err.Error(), "nil")) {
					t.Fatalf("incomplete backend accepted operation: %v", err)
				}
			})
		}
	}
}

type extCaptureObservedDoneContext struct {
	context.Context
	doneCalled chan struct{}
	doneOnce   sync.Once
}

func (c *extCaptureObservedDoneContext) Done() <-chan struct{} {
	c.doneOnce.Do(func() { close(c.doneCalled) })
	return c.Context.Done()
}

// A capture canceled during operation admission must stop before touching
// session or shared-memory state.
func TestExtCaptureCancellationDuringAdmissionDoesNotStartCapture(t *testing.T) {
	backend := &ExtCaptureBackend{}
	backend.mu.Lock()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &extCaptureObservedDoneContext{Context: base, doneCalled: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		_, err := backend.GrabFullHash(ctx)
		result <- err
	}()

	select {
	case <-ctx.doneCalled:
	case <-time.After(time.Second):
		backend.mu.Unlock()
		t.Fatal("capture did not reach context admission")
	}
	cancel()
	backend.mu.Unlock()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("capture after admission cancellation returned %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled capture did not leave the backend critical section")
	}

	if backend.session != nil || backend.shm != nil || backend.sessProxy != nil {
		t.Fatal("canceled uninitialized capture published protocol state")
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close backend after canceled capture: %v", err)
	}
}
