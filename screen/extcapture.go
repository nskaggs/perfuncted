package screen

import (
	"context"
	"fmt"
	"hash/crc32"
	"image"
	"os"
	"sync"
	"syscall"

	"github.com/nskaggs/perfuncted/internal/shmutil"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// ExtCaptureBackend captures the screen using ext_image_copy_capture_manager_v1.
// This protocol is detected by probing compositor globals at runtime; it is
// available where the compositor advertises ext_image_copy_capture_manager_v1.
// Do not assume specific compositor versions — rely solely on protocol presence.
type ExtCaptureBackend struct {
	mu           sync.Mutex
	session      *wl.Session
	display      *wl.Display
	registry     *wl.Registry
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

// NewExtCaptureBackend returns an ExtCaptureBackend if the compositor advertises
// the full ext-image-copy stack needed for output capture, otherwise an error.
func NewExtCaptureBackend() (*ExtCaptureBackend, error) {
	return NewExtCaptureBackendForSocket(wl.SocketPath())
}

// NewExtCaptureBackendForSocket returns an ExtCaptureBackend for sock if the
// compositor advertises the full ext-image-copy stack needed for capture.
func NewExtCaptureBackendForSocket(sock string) (*ExtCaptureBackend, error) {
	if sock == "" {
		return nil, fmt.Errorf("screen/ext: WAYLAND_DISPLAY not set")
	}
	s, err := wl.NewSession(sock)
	if err != nil {
		return nil, fmt.Errorf("screen/ext: %w", err)
	}
	ctx := s.Ctx
	display := s.Display
	registry := s.Registry
	b := &ExtCaptureBackend{session: s, display: display, registry: registry}

	if ev, ok := s.Globals["ext_image_copy_capture_manager_v1"]; ok {
		b.mgrID = ev.Name
		b.mgrVer = ev.Version
	}
	if ev, ok := s.Globals["ext_output_image_capture_source_manager_v1"]; ok {
		b.sourceMgrID = ev.Name
		b.sourceMgrVer = ev.Version
	}
	if ev, ok := s.Globals["wl_output"]; ok {
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
	}
	if ev, ok := s.Globals["wl_shm"]; ok {
		shm := &wl.Shm{}
		ctx.Register(shm)
		if err := registry.Bind(ev.Name, ev.Interface, 1, shm.ID()); err == nil {
			b.shm = shm
		}
	}

	if b.mgrID == 0 {
		_ = s.Close()
		return nil, fmt.Errorf("screen/ext: compositor does not advertise ext_image_copy_capture_manager_v1")
	}
	if b.sourceMgrID == 0 {
		_ = s.Close()
		return nil, fmt.Errorf("screen/ext: compositor does not advertise ext_output_image_capture_source_manager_v1")
	}
	if b.outputProxy == nil {
		_ = s.Close()
		return nil, fmt.Errorf("screen/ext: wl_output not advertised")
	}
	if b.shm == nil {
		_ = s.Close()
		return nil, fmt.Errorf("screen/ext: wl_shm not advertised")
	}

	// Initialize persistent proxies.
	b.mgrProxy = &wlRawProxy{}
	ctx.Register(b.mgrProxy)
	if err := registry.Bind(b.mgrID, "ext_image_copy_capture_manager_v1", min(b.mgrVer, 1), b.mgrProxy.ID()); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("screen/ext: bind manager: %w", err)
	}

	b.sourceMgrProxy = &wlRawProxy{}
	ctx.Register(b.sourceMgrProxy)
	if err := registry.Bind(b.sourceMgrID, "ext_output_image_capture_source_manager_v1", min(b.sourceMgrVer, 1), b.sourceMgrProxy.ID()); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("screen/ext: bind output source manager: %w", err)
	}

	b.sourceProxy = &wlRawProxy{}
	ctx.Register(b.sourceProxy)
	if err := sendExtOutputCreateSource(ctx, b.sourceMgrProxy.ID(), b.sourceProxy.ID(), b.outputProxy.ID()); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("screen/ext: create_source: %w", err)
	}

	b.sessProxy = &wlRawProxy{}
	ctx.Register(b.sessProxy)
	if err := sendExtCreateSession(ctx, b.mgrProxy.ID(), b.sessProxy.ID(), b.sourceProxy.ID()); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("screen/ext: create_session: %w", err)
	}

	return b, nil
}

// GrabFullHash returns a CRC32 checksum of the entire screen.
// Bypasses intermediate image allocation by hashing the raw buffer directly.
func (b *ExtCaptureBackend) GrabFullHash(ctx context.Context) (uint32, error) {
	var hash uint32
	if err := b.grabInternal(ctx, func(pixels []byte, _, _, _ int) error {
		hash = crc32.ChecksumIEEE(pixels)
		return nil
	}); err != nil {
		return 0, err
	}
	return hash, nil
}

// GrabRegionHash computes a CRC32 fingerprint for rect using the raw
// shared-memory buffer so no intermediate image allocation is needed.
func (b *ExtCaptureBackend) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	if rect.Empty() {
		return b.GrabFullHash(ctx)
	}
	var hash uint32
	if err := b.grabInternal(ctx, func(pixels []byte, w, h, stride int) error {
		// Crop rect to the buffer bounds.
		r := rect.Intersect(image.Rect(0, 0, w, h))
		if r.Empty() {
			hash = 0
			return nil
		}
		hasher := crc32.NewIEEE()
		rowBytes := r.Dx() * 4
		for y := r.Min.Y; y < r.Max.Y; y++ {
			start := y*stride + r.Min.X*4
			end := start + rowBytes
			if start < 0 || end > len(pixels) {
				return fmt.Errorf("screen/ext: region out of bounds")
			}
			_, _ = hasher.Write(pixels[start:end])
		}
		hash = hasher.Sum32()
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
		// Decode XRGB8888 (BGRA on little-endian x86).
		img := decodeBGRA(pixels, w, h, stride)

		// Crop to requested rect. Convert logical rect -> physical using outputScale.
		if rect.Dx() <= 0 || rect.Dy() <= 0 {
			outImg = img
			return nil
		}
		scale := int(b.outputScale)
		if scale <= 0 {
			scale = 1
		}
		phys := image.Rect(rect.Min.X*scale, rect.Min.Y*scale, rect.Max.X*scale, rect.Max.Y*scale)
		outImg = cropRGBA(img, phys)
		return nil
	}); err != nil {
		return nil, err
	}
	return outImg, nil
}

func (b *ExtCaptureBackend) grabInternal(ctx context.Context, fn func(pixels []byte, w, h, stride int) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	wlctx := b.display.Context()

	// Session events: 0=buffer_size, 1=shm_format, 5=stopped.
	type sessInfo struct{ width, height, format uint32 }
	var si sessInfo
	var stopped bool
	b.sessProxy.dispatchFn = func(opcode uint32, _ int, data []byte) {
		switch opcode {
		case 0: // buffer_size
			si.width = wl.Uint32(data[0:4])
			si.height = wl.Uint32(data[4:8])
		case 1: // shm_format
			si.format = wl.Uint32(data[0:4])
		case 5: // stopped
			stopped = true
		}
	}
	if err := b.display.RoundTrip(); err != nil {
		return fmt.Errorf("screen/ext: session round-trip: %w", err)
	}
	if stopped {
		return fmt.Errorf("screen/ext: capture session stopped before constraints arrived")
	}
	if si.width == 0 || si.height == 0 {
		return fmt.Errorf("screen/ext: session did not report buffer size")
	}

	stride := si.width * 4
	size := int(stride * si.height)

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
		f, err := shmutil.CreateFile(int64(size))
		if err != nil {
			return fmt.Errorf("screen/ext: shm file: %w", err)
		}
		px, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
		if err != nil {
			f.Close()
			return fmt.Errorf("screen/ext: mmap: %w", err)
		}
		b.cachedFd = f
		b.cachedBuf = px
		b.cachedBI = bufInfo{format: si.format, width: si.width, height: si.height, stride: stride}
		pixels = b.cachedBuf[:size]
	}

	pool, err := b.shm.CreatePool(int(b.cachedFd.Fd()), int32(size))
	if err != nil {
		return fmt.Errorf("screen/ext: create_pool: %w", err)
	}
	defer pool.Destroy() //nolint:errcheck

	wlbuf, err := pool.CreateBuffer(0, int32(si.width), int32(si.height), int32(stride), si.format)
	if err != nil {
		return fmt.Errorf("screen/ext: create_buffer: %w", err)
	}
	defer wlbuf.Destroy() //nolint:errcheck

	// create_frame(new_id) — session opcode 1.
	frameProxy := &wlRawProxy{}
	wlctx.Register(frameProxy)
	if err := sendExtCreateFrame(wlctx, b.sessProxy.ID(), frameProxy.ID()); err != nil {
		return fmt.Errorf("screen/ext: create_frame: %w", err)
	}
	defer sendWaylandRequest(wlctx, frameProxy.ID(), 0, nil) //nolint:errcheck

	// attach_buffer(buffer) — frame opcode 1.
	if err := sendExtAttachBuffer(wlctx, frameProxy.ID(), wlbuf.ID()); err != nil {
		return fmt.Errorf("screen/ext: attach_buffer: %w", err)
	}
	if err := sendExtDamageBuffer(wlctx, frameProxy.ID(), int32(si.width), int32(si.height)); err != nil {
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
	if err := sendExtCapture(wlctx, frameProxy.ID()); err != nil {
		return fmt.Errorf("screen/ext: capture: %w", err)
	}

	for !ready && !failed {
		if err := wlctx.Dispatch(); err != nil {
			return fmt.Errorf("screen/ext: dispatch: %w", err)
		}
	}
	if failed {
		return fmt.Errorf("screen/ext: compositor signalled frame failed")
	}

	return fn(pixels, int(si.width), int(si.height), int(stride))
}

func (b *ExtCaptureBackend) Close() error {
	// clean up pooled mmap and associated fd
	if b.cachedBuf != nil {
		_ = syscall.Munmap(b.cachedBuf)
		b.cachedBuf = nil
	}
	if b.cachedFd != nil {
		_ = b.cachedFd.Close()
		b.cachedFd = nil
	}
	if b.session != nil {
		return b.session.Close()
	}
	if b.display != nil {
		return b.display.Context().Close()
	}
	return nil
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

func sendExtOutputCreateSource(ctx wl.Ctx, managerID, sourceID, outputID uint32) error {
	return sendWaylandRequest(ctx, managerID, 0, wlUint32Payload(sourceID, outputID))
}

func sendExtCreateSession(ctx wl.Ctx, managerID, sessionID, sourceID uint32) error {
	return sendWaylandRequest(ctx, managerID, 0, wlUint32Payload(sessionID, sourceID, 0))
}

func sendExtCreateFrame(ctx wl.Ctx, sessionID, frameID uint32) error {
	return sendWaylandRequest(ctx, sessionID, 0, wlUint32Payload(frameID))
}

func sendExtAttachBuffer(ctx wl.Ctx, frameID, bufferID uint32) error {
	return sendWaylandRequest(ctx, frameID, 1, wlUint32Payload(bufferID))
}

func sendExtDamageBuffer(ctx wl.Ctx, frameID uint32, width, height int32) error {
	return sendWaylandRequest(ctx, frameID, 2, wlInt32Payload(0, 0, width, height))
}

func sendExtCapture(ctx wl.Ctx, frameID uint32) error {
	return sendWaylandRequest(ctx, frameID, 3, nil)
}

func sendWaylandRequest(ctx wl.Ctx, senderID, opcode uint32, payload []byte) error {
	size := 8 + len(payload)
	buf := make([]byte, size)
	wl.PutUint32(buf[0:], senderID)
	wl.PutUint32(buf[4:], uint32(size)<<16|opcode)
	copy(buf[8:], payload)
	return ctx.WriteMsg(buf, nil)
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
