package screen

import (
	"context"
	"fmt"
	"image"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/shmutil"
	"github.com/nskaggs/perfuncted/internal/wl"
)

var _ Screenshotter = (*ExtCaptureBackend)(nil)

// ExtCaptureBackend captures the screen using ext_image_copy_capture_manager_v1.
// This protocol is detected by probing compositor globals at runtime; it is
// available where the compositor advertises ext_image_copy_capture_manager_v1.
// Do not assume specific compositor versions — rely solely on protocol presence.
type ExtCaptureBackend struct {
	// mu protects the session-owned protocol state, shm, manager globals, and outputProxy.
	mu sync.Mutex
	// lifecycleMu protects closed, closeDone, and active capture cancellation.
	// It is deliberately separate from mu: Close must be able to cancel a
	// blocked Wayland operation without waiting for that operation's resource
	// lock.
	lifecycleMu  sync.Mutex
	closed       bool
	closeDone    chan struct{}
	closeErr     error
	active       map[uint64]context.CancelFunc
	activeDone   chan struct{}
	nextCapture  uint64
	session      *wl.Session
	shm          *wl.Shm
	mgrID        uint32
	mgrVer       uint32
	sourceMgrID  uint32
	sourceMgrVer uint32
	outputProxy  *wlRawProxy
	outputScale  uint32

	// cached proxies
	mgrProxy       *wlRawProxy
	sourceMgrProxy *wlRawProxy
	sourceProxy    *wlRawProxy
	sessProxy      *wlRawProxy

	// pooled shared-memory mmap to avoid creating/munmapping on every capture
	cachedBuf []byte
	cachedFd  *os.File
	cachedBI  bufInfo
}

type extSessionInfo struct {
	width, height, format uint32
}

func applyExtSessionEvent(si *extSessionInfo, stopped, invalid *bool, opcode uint32, data []byte) {
	switch opcode {
	case 0: // buffer_size
		if len(data) < 8 {
			*invalid = true
			return
		}
		si.width = wl.Uint32(data[0:4])
		si.height = wl.Uint32(data[4:8])
	case 1: // shm_format
		if len(data) < 4 {
			*invalid = true
			return
		}
		si.format = wl.Uint32(data[0:4])
	case 5: // stopped
		*stopped = true
	}
}

// NewExtCaptureBackendForSocket returns an ExtCaptureBackend for sock if the
// compositor advertises the full ext-image-copy stack needed for capture.
func NewExtCaptureBackendForSocket(sock string) (*ExtCaptureBackend, error) { //nolint:gocyclo
	if sock == "" {
		return nil, fmt.Errorf("screen/ext: WAYLAND_DISPLAY not set")
	}
	s, err := wl.NewSession(sock)
	if err != nil {
		return nil, fmt.Errorf("screen/ext: %w", err)
	}
	ctx := s.Ctx
	registry := s.Registry
	b := &ExtCaptureBackend{session: s}

	for _, ev := range s.GlobalsSnapshot() {
		switch ev.Interface {
		case "ext_image_copy_capture_manager_v1":
			if b.mgrID == 0 {
				b.mgrID = ev.Name
				b.mgrVer = ev.Version
			}
		case "ext_output_image_capture_source_manager_v1":
			if b.sourceMgrID == 0 {
				b.sourceMgrID = ev.Name
				b.sourceMgrVer = ev.Version
			}
		}
	}
	initErr := wl.WithOperation(ctx, func() error {
		for _, ev := range s.GlobalsSnapshot() {
			if ev.Interface != "wl_output" {
				continue
			}
			out := &wlRawProxy{}
			ctx.Register(out)
			if err := registry.Bind(ev.Name, ev.Interface, 1, out.ID()); err == nil {
				b.outputProxy = out
				// record output scale via dispatchFn
				out.dispatchFn = func(op uint32, _ int, data []byte) {
					if op == 3 && len(data) >= 4 { // scale
						b.outputScale = wl.Uint32(data[0:4])
						if b.outputScale == 0 {
							b.outputScale = 1
						}
					}
				}
			}
			break
		}
		for _, ev := range s.GlobalsSnapshot() {
			if ev.Interface != "wl_shm" {
				continue
			}
			shm := &wl.Shm{}
			ctx.Register(shm)
			if err := registry.Bind(ev.Name, ev.Interface, 1, shm.ID()); err == nil {
				b.shm = shm
			}
			break
		}

		if b.mgrID == 0 {
			return fmt.Errorf("screen/ext: compositor does not advertise ext_image_copy_capture_manager_v1")
		}
		if b.sourceMgrID == 0 {
			return fmt.Errorf("screen/ext: compositor does not advertise ext_output_image_capture_source_manager_v1")
		}
		if b.outputProxy == nil {
			return fmt.Errorf("screen/ext: wl_output not advertised")
		}
		if b.shm == nil {
			return fmt.Errorf("screen/ext: wl_shm not advertised")
		}

		// Initialize persistent proxies.
		b.mgrProxy = &wlRawProxy{}
		ctx.Register(b.mgrProxy)
		if err := registry.Bind(b.mgrID, "ext_image_copy_capture_manager_v1", min(b.mgrVer, 1), b.mgrProxy.ID()); err != nil {
			return fmt.Errorf("screen/ext: bind manager: %w", err)
		}

		b.sourceMgrProxy = &wlRawProxy{}
		ctx.Register(b.sourceMgrProxy)
		if err := registry.Bind(b.sourceMgrID, "ext_output_image_capture_source_manager_v1", min(b.sourceMgrVer, 1), b.sourceMgrProxy.ID()); err != nil {
			return fmt.Errorf("screen/ext: bind output source manager: %w", err)
		}

		b.sourceProxy = &wlRawProxy{}
		ctx.Register(b.sourceProxy)
		if err := sendExtOutputCreateSource(context.Background(), ctx, b.sourceMgrProxy.ID(), b.sourceProxy.ID(), b.outputProxy.ID()); err != nil {
			return fmt.Errorf("screen/ext: create_source: %w", err)
		}

		b.sessProxy = &wlRawProxy{}
		ctx.Register(b.sessProxy)
		if err := sendExtCreateSession(context.Background(), ctx, b.mgrProxy.ID(), b.sessProxy.ID(), b.sourceProxy.ID()); err != nil {
			return fmt.Errorf("screen/ext: create_session: %w", err)
		}
		return nil
	})
	if initErr != nil {
		_ = s.Close()
		return nil, initErr
	}
	return b, nil
}

// GrabFullHash returns a CRC32 checksum of the entire screen in the
// canonical find.PixelHash representation, so values are comparable with
// hashes taken from Grab images or other backends.
func (b *ExtCaptureBackend) GrabFullHash(ctx context.Context) (uint32, error) {
	var hash uint32
	if err := b.grabInternal(ctx, func(pixels []byte, w, h, stride int) error {
		hash = find.PixelHash(decodeBGRA(pixels, w, h, stride), nil)
		return nil
	}); err != nil {
		return 0, err
	}
	return hash, nil
}

// GrabRegionHash computes a canonical find.PixelHash fingerprint for rect.
func (b *ExtCaptureBackend) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	if rect.Empty() {
		return b.GrabFullHash(ctx)
	}
	var hash uint32
	if err := b.grabInternal(ctx, func(pixels []byte, w, h, stride int) error {
		// Crop rect to the buffer bounds.
		scale := int(b.outputScale)
		if scale <= 0 {
			scale = 1
		}
		r := logicalRectToPhysical(rect, scale).Intersect(image.Rect(0, 0, w, h))
		if r.Empty() {
			hash = 0
			return nil
		}
		if r.Min.X < 0 || (r.Max.Y-1)*stride+r.Max.X*4 > len(pixels) {
			return fmt.Errorf("screen/ext: region out of bounds")
		}
		hash = find.PixelHash(decodeBGRARect(pixels, w, h, stride, r), nil)
		return nil
	}); err != nil {
		return 0, err
	}
	return hash, nil
}

// Grab captures the full output then returns the cropped rect.
func (b *ExtCaptureBackend) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	var outImg image.Image
	if err := b.grabInternal(ctx, func(pixels []byte, w, h, stride int) error {
		// Crop to requested rect. Convert logical rect -> physical using outputScale.
		if rect.Dx() <= 0 || rect.Dy() <= 0 {
			outImg = decodeBGRA(pixels, w, h, stride)
			return nil
		}
		scale := int(b.outputScale)
		if scale <= 0 {
			scale = 1
		}
		phys := logicalRectToPhysical(rect, scale)
		outImg = decodeBGRARect(pixels, w, h, stride, phys)
		return nil
	}); err != nil {
		return nil, err
	}
	return outImg, nil
}

func (b *ExtCaptureBackend) grabInternal(ctx context.Context, fn func(pixels []byte, w, h, stride int) error) error { //nolint:gocyclo
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("screen/ext: capture canceled: %w", err)
	}
	captureCtx, finish, err := b.beginCapture(ctx)
	if err != nil {
		return err
	}
	defer finish()

	b.mu.Lock()
	defer b.mu.Unlock()
	if err := captureCtx.Err(); err != nil {
		return fmt.Errorf("screen/ext: capture canceled: %w", err)
	}

	wlctx := b.session.Display.Context()
	return wl.WithOperationContext(captureCtx, wlctx, func() error { //nolint:contextcheck // this helper serializes an existing operation context.
		var cleanupCtx context.Context
		var cleanupCancel context.CancelFunc
		cleanupContext := func() context.Context {
			if cleanupCtx == nil {
				cleanupCtx, cleanupCancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
			}
			return cleanupCtx
		}
		defer func() {
			if cleanupCancel != nil {
				cleanupCancel()
			}
		}()

		// Session events: 0=buffer_size, 1=shm_format, 5=stopped.
		var si extSessionInfo
		var stopped, invalidEvent bool
		b.sessProxy.dispatchFn = func(opcode uint32, _ int, data []byte) {
			applyExtSessionEvent(&si, &stopped, &invalidEvent, opcode, data)
		}
		if err := b.session.Display.RoundTripContext(captureCtx); err != nil {
			return fmt.Errorf("screen/ext: session round-trip: %w", err)
		}
		if invalidEvent {
			return fmt.Errorf("screen/ext: compositor sent malformed session constraints")
		}
		if stopped {
			return fmt.Errorf("screen/ext: capture session stopped before constraints arrived")
		}
		if err := captureCtx.Err(); err != nil {
			return fmt.Errorf("screen/ext: capture canceled: %w", err)
		}
		if si.width == 0 || si.height == 0 {
			return fmt.Errorf("screen/ext: session did not report buffer size")
		}

		stride64 := uint64(si.width) * 4
		if stride64 > uint64(^uint32(0)) {
			return fmt.Errorf("screen/ext: capture width is too large")
		}
		stride := uint32(stride64)
		size, err := captureBufferSize(si.width, si.height, stride)
		if err != nil {
			return fmt.Errorf("screen/ext: invalid buffer geometry: %w", err)
		}

		// Reuse a pooled mmap if the buffer geometry hasn't changed.
		var pixels []byte
		wantedBI := bufInfo{format: si.format, width: si.width, height: si.height, stride: stride}
		if b.cachedBuf != nil && b.cachedBI == wantedBI && len(b.cachedBuf) >= size {
			pixels = b.cachedBuf[:size]
		} else {
			// Tear down any existing cached mapping.
			if b.cachedBuf != nil {
				_ = syscall.Munmap(b.cachedBuf)
				b.cachedBuf = nil
			}
			if b.cachedFd != nil {
				_ = b.cachedFd.Close()
				b.cachedFd = nil
			}
			f, createErr := shmutil.CreateFile(int64(size))
			if createErr != nil {
				return fmt.Errorf("screen/ext: shm file: %w", createErr)
			}
			px, mmapErr := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
			if mmapErr != nil {
				f.Close()
				return fmt.Errorf("screen/ext: mmap: %w", mmapErr)
			}
			b.cachedFd = f
			b.cachedBuf = px
			b.cachedBI = bufInfo{format: si.format, width: si.width, height: si.height, stride: stride}
			pixels = b.cachedBuf[:size]
		}

		pool, err := b.shm.CreatePoolContext(captureCtx, int(b.cachedFd.Fd()), int32(size))
		if err != nil {
			return fmt.Errorf("screen/ext: create_pool: %w", err)
		}
		defer func() { //nolint:contextcheck // cleanup intentionally uses an independent bounded context.
			_ = pool.DestroyContext(cleanupContext())
			wl.Unregister(wlctx, pool)
		}()

		wlbuf, err := pool.CreateBufferContext(captureCtx, 0, int32(si.width), int32(si.height), int32(stride), si.format)
		if err != nil {
			return fmt.Errorf("screen/ext: create_buffer: %w", err)
		}
		defer func() { //nolint:contextcheck // cleanup intentionally uses an independent bounded context.
			_ = wlbuf.DestroyContext(cleanupContext())
			wl.Unregister(wlctx, wlbuf)
		}()

		// create_frame(new_id) — session opcode 1.
		frameProxy := &wlRawProxy{}
		wlctx.Register(frameProxy)
		defer func() { //nolint:contextcheck // cleanup intentionally uses an independent bounded context.
			_ = sendWaylandRequest(cleanupContext(), wlctx, frameProxy.ID(), 0, nil)
			wl.Unregister(wlctx, frameProxy)
		}()
		if err := sendExtCreateFrame(captureCtx, wlctx, b.sessProxy.ID(), frameProxy.ID()); err != nil {
			return fmt.Errorf("screen/ext: create_frame: %w", err)
		}

		// attach_buffer(buffer) — frame opcode 1.
		if err := sendExtAttachBuffer(captureCtx, wlctx, frameProxy.ID(), wlbuf.ID()); err != nil {
			return fmt.Errorf("screen/ext: attach_buffer: %w", err)
		}
		if err := sendExtDamageBuffer(captureCtx, wlctx, frameProxy.ID(), int32(si.width), int32(si.height)); err != nil {
			return fmt.Errorf("screen/ext: damage_buffer: %w", err)
		}

		// capture — frame opcode 3.
		var ready, failed bool
		frameProxy.dispatchFn = func(opcode uint32, _ int, _ []byte) {
			switch opcode {
			case 3: // ready
				ready = true
			case 4: // failed
				failed = true
			}
		}
		if err := sendExtCapture(captureCtx, wlctx, frameProxy.ID()); err != nil {
			return fmt.Errorf("screen/ext: capture: %w", err)
		}

		for !ready && !failed {
			if err := captureCtx.Err(); err != nil {
				return err
			}
			if err := wl.DispatchContext(captureCtx, wlctx); err != nil {
				return fmt.Errorf("screen/ext: dispatch: %w", err)
			}
		}
		if failed {
			return fmt.Errorf("screen/ext: compositor signalled frame failed")
		}

		return fn(pixels, int(si.width), int(si.height), int(stride))
	})
}

// beginCapture admits a capture and returns a context that Close can cancel.
// Admission is recorded before grabInternal takes mu, so Close cannot observe
// an operation as absent and then race with it acquiring the resource lock.
func (b *ExtCaptureBackend) beginCapture(ctx context.Context) (context.Context, func(), error) {
	ctx = contextutil.Default(ctx)
	captureCtx, cancel := context.WithCancel(ctx)

	b.lifecycleMu.Lock()
	if b.closed {
		b.lifecycleMu.Unlock()
		cancel()
		return nil, nil, fmt.Errorf("screen/ext: backend is closed")
	}
	if b.active == nil {
		b.active = make(map[uint64]context.CancelFunc)
	}
	if len(b.active) == 0 {
		b.activeDone = make(chan struct{})
	}
	b.nextCapture++
	id := b.nextCapture
	b.active[id] = cancel
	b.lifecycleMu.Unlock()

	finish := sync.OnceFunc(func() {
		cancel()
		b.lifecycleMu.Lock()
		delete(b.active, id)
		if len(b.active) == 0 && b.activeDone != nil {
			close(b.activeDone)
		}
		b.lifecycleMu.Unlock()
	})
	return captureCtx, finish, nil
}

// Close releases the ext-image-copy protocol resources.
func (b *ExtCaptureBackend) Close() error {
	b.lifecycleMu.Lock()
	if b.closed {
		done := b.closeDone
		b.lifecycleMu.Unlock()
		if done != nil {
			<-done
		}
		b.lifecycleMu.Lock()
		err := b.closeErr
		b.lifecycleMu.Unlock()
		return err
	}
	b.closed = true
	b.closeDone = make(chan struct{})
	done := b.closeDone
	activeDone := b.activeDone
	cancels := make([]context.CancelFunc, 0, len(b.active))
	for _, cancel := range b.active {
		cancels = append(cancels, cancel)
	}
	b.lifecycleMu.Unlock()

	// Never hold mu while canceling or waiting. A capture needs mu for its
	// complete serialized Wayland operation and must be allowed to unwind.
	for _, cancel := range cancels {
		cancel()
	}
	if activeDone != nil {
		<-activeDone
	}

	b.mu.Lock()
	// clean up pooled mmap and associated fd
	if b.cachedBuf != nil {
		_ = syscall.Munmap(b.cachedBuf)
		b.cachedBuf = nil
	}
	if b.cachedFd != nil {
		_ = b.cachedFd.Close()
		b.cachedFd = nil
	}
	b.mu.Unlock()

	var err error
	if b.session != nil {
		err = b.session.Close()
	}
	b.lifecycleMu.Lock()
	b.closeErr = err
	close(done)
	b.lifecycleMu.Unlock()
	return err
}

func extCaptureAvailable(globals map[string]bool) (bool, string) {
	if globals == nil {
		return false, "no Wayland session"
	}
	if !globals["ext_image_copy_capture_manager_v1"] {
		return false, "ext_image_copy_capture_manager_v1 not advertised"
	}
	if !globals["ext_output_image_capture_source_manager_v1"] {
		return false, "ext_output_image_capture_source_manager_v1 not advertised"
	}
	return true, "ext_image_copy_capture_manager_v1 + ext_output_image_capture_source_manager_v1 advertised"
}

func sendExtOutputCreateSource(cancel context.Context, ctx wl.Ctx, managerID, sourceID, outputID uint32) error {
	return sendWaylandRequest(cancel, ctx, managerID, 0, wlUint32Payload(sourceID, outputID))
}

func sendExtCreateSession(cancel context.Context, ctx wl.Ctx, managerID, sessionID, sourceID uint32) error {
	return sendWaylandRequest(cancel, ctx, managerID, 0, wlUint32Payload(sessionID, sourceID, 0))
}

func sendExtCreateFrame(cancel context.Context, ctx wl.Ctx, sessionID, frameID uint32) error {
	return sendWaylandRequest(cancel, ctx, sessionID, 0, wlUint32Payload(frameID))
}

func sendExtAttachBuffer(cancel context.Context, ctx wl.Ctx, frameID, bufferID uint32) error {
	return sendWaylandRequest(cancel, ctx, frameID, 1, wlUint32Payload(bufferID))
}

func sendExtDamageBuffer(cancel context.Context, ctx wl.Ctx, frameID uint32, width, height int32) error {
	return sendWaylandRequest(cancel, ctx, frameID, 2, wlInt32Payload(0, 0, width, height))
}

func sendExtCapture(cancel context.Context, ctx wl.Ctx, frameID uint32) error {
	return sendWaylandRequest(cancel, ctx, frameID, 3, nil)
}

func sendWaylandRequest(cancel context.Context, ctx wl.Ctx, senderID, opcode uint32, payload []byte) error {
	size := 8 + len(payload)
	buf := make([]byte, size)
	wl.PutUint32(buf[0:], senderID)
	wl.PutUint32(buf[4:], uint32(size)<<16|opcode)
	copy(buf[8:], payload)
	return ctx.WriteMsgContext(cancel, buf, nil)
}

func wlUint32Payload(values ...uint32) []byte {
	buf := make([]byte, 4*len(values))
	for i, value := range values {
		wl.PutUint32(buf[i*4:], value)
	}
	return buf
}

func wlInt32Payload(values ...int32) []byte {
	buf := make([]byte, 4*len(values))
	for i, value := range values {
		wl.PutUint32(buf[i*4:], uint32(value))
	}
	return buf
}
