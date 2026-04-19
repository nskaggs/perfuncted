package screen

import (
	"fmt"
	"image"
	"syscall"

	"github.com/nskaggs/perfuncted/internal/shmutil"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// ExtCaptureBackend captures the screen using ext_image_copy_capture_manager_v1.
// This protocol is detected by probing compositor globals at runtime; it is
// available where the compositor advertises ext_image_copy_capture_manager_v1.
// Do not assume specific compositor versions — rely solely on protocol presence.
type ExtCaptureBackend struct {
	display      *wl.Display
	registry     *wl.Registry
	shm          *wl.Shm
	mgrID        uint32
	mgrVer       uint32
	sourceMgrID  uint32
	sourceMgrVer uint32
	output       *wl.Output
}

// NewExtCaptureBackend returns an ExtCaptureBackend if the compositor advertises
// the full ext-image-copy stack needed for output capture, otherwise an error.
func NewExtCaptureBackend() (*ExtCaptureBackend, error) {
	sock := wl.SocketPath()
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
	b := &ExtCaptureBackend{display: display, registry: registry}

	if ev, ok := s.Globals["ext_image_copy_capture_manager_v1"]; ok {
		b.mgrID = ev.Name
		b.mgrVer = ev.Version
	}
	if ev, ok := s.Globals["ext_output_image_capture_source_manager_v1"]; ok {
		b.sourceMgrID = ev.Name
		b.sourceMgrVer = ev.Version
	}
	if ev, ok := s.Globals["wl_output"]; ok {
		out := &wl.Output{}
		ctx.Register(out)
		if err := registry.Bind(ev.Name, ev.Interface, 1, out.ID()); err == nil {
			b.output = out
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
		_ = ctx.Close()
		return nil, fmt.Errorf("screen/ext: compositor does not advertise ext_image_copy_capture_manager_v1")
	}
	if b.sourceMgrID == 0 {
		_ = ctx.Close()
		return nil, fmt.Errorf("screen/ext: compositor does not advertise ext_output_image_capture_source_manager_v1")
	}
	if b.output == nil {
		_ = ctx.Close()
		return nil, fmt.Errorf("screen/ext: wl_output not advertised")
	}
	if b.shm == nil {
		_ = ctx.Close()
		return nil, fmt.Errorf("screen/ext: wl_shm not advertised")
	}
	return b, nil
}

// Grab captures the full output then returns the cropped rect.
// Wire protocol:
//   - ext_output_image_capture_source_manager_v1 opcode 0 = create_source
//   - ext_image_copy_capture_manager_v1 opcode 0 = create_session
//   - ext_image_copy_capture_session_v1 opcode 0 = create_frame
//   - ext_image_copy_capture_frame_v1 opcode 1 = attach_buffer
//   - ext_image_copy_capture_frame_v1 opcode 2 = damage_buffer
//   - ext_image_copy_capture_frame_v1 opcode 3 = capture
func (b *ExtCaptureBackend) Grab(rect image.Rectangle) (image.Image, error) {
	ctx := b.display.Context()

	// Bind managers.
	mgrProxy := &wlRawProxy{}
	ctx.Register(mgrProxy)
	if err := b.registry.Bind(b.mgrID, "ext_image_copy_capture_manager_v1", min(b.mgrVer, 1), mgrProxy.ID()); err != nil {
		return nil, fmt.Errorf("screen/ext: bind manager: %w", err)
	}
	defer sendWaylandRequest(ctx, mgrProxy.ID(), 2, nil) //nolint:errcheck

	sourceMgrProxy := &wlRawProxy{}
	ctx.Register(sourceMgrProxy)
	if err := b.registry.Bind(b.sourceMgrID, "ext_output_image_capture_source_manager_v1", min(b.sourceMgrVer, 1), sourceMgrProxy.ID()); err != nil {
		return nil, fmt.Errorf("screen/ext: bind output source manager: %w", err)
	}
	defer sendWaylandRequest(ctx, sourceMgrProxy.ID(), 1, nil) //nolint:errcheck

	sourceProxy := &wlRawProxy{}
	ctx.Register(sourceProxy)
	if err := sendExtOutputCreateSource(ctx, sourceMgrProxy.ID(), sourceProxy.ID(), b.output.ID()); err != nil {
		return nil, fmt.Errorf("screen/ext: create_source: %w", err)
	}
	defer sendWaylandRequest(ctx, sourceProxy.ID(), 0, nil) //nolint:errcheck

	// create_session(new_id, source, options=0) — opcode 0.
	sessProxy := &wlRawProxy{}
	ctx.Register(sessProxy)
	if err := sendExtCreateSession(ctx, mgrProxy.ID(), sessProxy.ID(), sourceProxy.ID()); err != nil {
		return nil, fmt.Errorf("screen/ext: create_session: %w", err)
	}
	defer sendWaylandRequest(ctx, sessProxy.ID(), 1, nil) //nolint:errcheck

	// Session events: 0=buffer_size, 1=shm_format, 5=stopped.
	type sessInfo struct{ width, height, format uint32 }
	var si sessInfo
	var stopped bool
	sessProxy.dispatchFn = func(opcode uint32, _ int, data []byte) {
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
		return nil, fmt.Errorf("screen/ext: session round-trip: %w", err)
	}
	if stopped {
		return nil, fmt.Errorf("screen/ext: capture session stopped before constraints arrived")
	}
	if si.width == 0 || si.height == 0 {
		return nil, fmt.Errorf("screen/ext: session did not report buffer size")
	}

	stride := si.width * 4
	size := int(stride * si.height)
	f, err := shmutil.CreateFile(int64(size))
	if err != nil {
		return nil, fmt.Errorf("screen/ext: shm file: %w", err)
	}
	defer f.Close()

	pixels, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("screen/ext: mmap: %w", err)
	}
	defer syscall.Munmap(pixels) //nolint:errcheck

	pool, err := b.shm.CreatePool(int(f.Fd()), int32(size))
	if err != nil {
		return nil, fmt.Errorf("screen/ext: create_pool: %w", err)
	}
	defer pool.Destroy() //nolint:errcheck

	wlbuf, err := pool.CreateBuffer(0, int32(si.width), int32(si.height), int32(stride), si.format)
	if err != nil {
		return nil, fmt.Errorf("screen/ext: create_buffer: %w", err)
	}
	defer wlbuf.Destroy() //nolint:errcheck

	// create_frame(new_id) — session opcode 1.
	frameProxy := &wlRawProxy{}
	ctx.Register(frameProxy)
	if err := sendExtCreateFrame(ctx, sessProxy.ID(), frameProxy.ID()); err != nil {
		return nil, fmt.Errorf("screen/ext: create_frame: %w", err)
	}
	defer sendWaylandRequest(ctx, frameProxy.ID(), 0, nil) //nolint:errcheck

	// attach_buffer(buffer) — frame opcode 1.
	if err := sendExtAttachBuffer(ctx, frameProxy.ID(), wlbuf.ID()); err != nil {
		return nil, fmt.Errorf("screen/ext: attach_buffer: %w", err)
	}
	if err := sendExtDamageBuffer(ctx, frameProxy.ID(), int32(si.width), int32(si.height)); err != nil {
		return nil, fmt.Errorf("screen/ext: damage_buffer: %w", err)
	}

	// capture — frame opcode 3.
	var ready, failed bool
	frameProxy.dispatchFn = func(opcode uint32, _ int, _ []byte) {
		switch opcode {
		case 0: // transform
		case 1: // damage
		case 2: // presentation_time
		case 3: // ready
			ready = true
		case 4: // failed
			failed = true
		}
	}
	if err := sendExtCapture(ctx, frameProxy.ID()); err != nil {
		return nil, fmt.Errorf("screen/ext: capture: %w", err)
	}

	for !ready && !failed {
		if err := ctx.Dispatch(); err != nil {
			return nil, fmt.Errorf("screen/ext: dispatch: %w", err)
		}
	}
	if failed {
		return nil, fmt.Errorf("screen/ext: compositor signalled frame failed")
	}

	// Decode XRGB8888 (BGRA on little-endian x86).
	img := decodeBGRA(pixels, int(si.width), int(si.height), int(stride))

	// Crop to requested rect.
	return cropRGBA(img, rect), nil
}

func (b *ExtCaptureBackend) Close() error { return b.display.Context().Close() }

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
