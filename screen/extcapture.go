package screen

import (
	"fmt"
	"image"
	"syscall"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// ExtCaptureBackend captures the screen using ext_image_copy_capture_manager_v1.
// This protocol is detected by probing compositor globals at runtime; it is
// available where the compositor advertises ext_image_copy_capture_manager_v1.
// Do not assume specific compositor versions — rely solely on protocol presence.
type ExtCaptureBackend struct {
	display  *wl.Display
	registry *wl.Registry
	shm      *wl.Shm
	mgrID    uint32
	mgrVer   uint32
	output   *wl.Output
}

// NewExtCaptureBackend returns an ExtCaptureBackend if the compositor advertises
// ext_image_copy_capture_manager_v1, otherwise an error.
func NewExtCaptureBackend() (*ExtCaptureBackend, error) {
	sock := wl.SocketPath()
	if sock == "" {
		return nil, fmt.Errorf("screen/ext: WAYLAND_DISPLAY not set")
	}
	ctx, err := wl.Connect(sock)
	if err != nil {
		return nil, fmt.Errorf("screen/ext: connect: %w", err)
	}
	display := wl.NewDisplay(ctx)
	registry, err := display.GetRegistry()
	if err != nil {
		ctx.Close()
		return nil, fmt.Errorf("screen/ext: get registry: %w", err)
	}
	b := &ExtCaptureBackend{display: display, registry: registry}
	registry.SetGlobalHandler(func(ev wl.GlobalEvent) {
		switch ev.Interface {
		case "ext_image_copy_capture_manager_v1":
			b.mgrID = ev.Name
			b.mgrVer = ev.Version
		case "wl_output":
			if b.output == nil {
				out := &wl.Output{}
				ctx.Register(out)
				if err := registry.Bind(ev.Name, ev.Interface, 1, out.ID()); err == nil {
					b.output = out
				}
			}
		case "wl_shm":
			shm := &wl.Shm{}
			ctx.Register(shm)
			if err := registry.Bind(ev.Name, ev.Interface, 1, shm.ID()); err == nil {
				b.shm = shm
			}
		}
	})
	if err := display.RoundTrip(); err != nil {
		ctx.Close()
		return nil, fmt.Errorf("screen/ext: registry round-trip: %w", err)
	}
	if b.mgrID == 0 {
		ctx.Close()
		return nil, fmt.Errorf("screen/ext: compositor does not advertise ext_image_copy_capture_manager_v1")
	}
	return b, nil
}

// Grab captures the full output then returns the cropped rect.
// Wire protocol: ext_image_copy_capture_manager_v1 opcode 0 = create_session,
// ext_image_copy_capture_session_v1 opcode 1 = create_frame,
// ext_image_copy_capture_frame_v1 opcode 1 = attach_buffer / opcode 2 = capture.
func (b *ExtCaptureBackend) Grab(rect image.Rectangle) (image.Image, error) {
	ctx := b.display.Context()

	// Bind manager.
	mgrProxy := &wlRawProxy{}
	ctx.Register(mgrProxy)
	if err := b.registry.Bind(b.mgrID, "ext_image_copy_capture_manager_v1", min(b.mgrVer, 1), mgrProxy.ID()); err != nil {
		return nil, fmt.Errorf("screen/ext: bind manager: %w", err)
	}

	// create_session(new_id, output, options=0) — opcode 0.
	sessProxy := &wlRawProxy{}
	ctx.Register(sessProxy)
	{
		const msgSize = 8 + 4 + 4 + 4
		var buf [msgSize]byte
		wl.PutUint32(buf[0:], mgrProxy.ID())
		wl.PutUint32(buf[4:], msgSize<<16)
		wl.PutUint32(buf[8:], sessProxy.ID())
		wl.PutUint32(buf[12:], b.output.ID())
		wl.PutUint32(buf[16:], 0)
		if err := ctx.WriteMsg(buf[:], nil); err != nil {
			return nil, fmt.Errorf("screen/ext: create_session: %w", err)
		}
	}

	// Session events: 0=buffer_size(w,h), 1=shm_format(fmt), 2=stopped.
	type sessInfo struct{ width, height, format uint32 }
	var si sessInfo
	sessProxy.dispatchFn = func(opcode uint32, _ int, data []byte) {
		switch opcode {
		case 0: // buffer_size
			si.width = wl.Uint32(data[0:4])
			si.height = wl.Uint32(data[4:8])
		case 1: // shm_format
			si.format = wl.Uint32(data[0:4])
		}
	}
	if err := b.display.RoundTrip(); err != nil {
		return nil, fmt.Errorf("screen/ext: session round-trip: %w", err)
	}
	if si.width == 0 || si.height == 0 {
		return nil, fmt.Errorf("screen/ext: session did not report buffer size")
	}

	stride := si.width * 4
	size := int(stride * si.height)
	f, err := wlCreateShmFile(int64(size))
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
	{
		const msgSize = 8 + 4
		var buf [msgSize]byte
		wl.PutUint32(buf[0:], sessProxy.ID())
		wl.PutUint32(buf[4:], msgSize<<16|1)
		wl.PutUint32(buf[8:], frameProxy.ID())
		if err := ctx.WriteMsg(buf[:], nil); err != nil {
			return nil, fmt.Errorf("screen/ext: create_frame: %w", err)
		}
	}

	// attach_buffer(buffer) — frame opcode 1.
	{
		const msgSize = 8 + 4
		var buf [msgSize]byte
		wl.PutUint32(buf[0:], frameProxy.ID())
		wl.PutUint32(buf[4:], msgSize<<16|1)
		wl.PutUint32(buf[8:], wlbuf.ID())
		if err := ctx.WriteMsg(buf[:], nil); err != nil {
			return nil, fmt.Errorf("screen/ext: attach_buffer: %w", err)
		}
	}

	// capture — frame opcode 2.
	var ready, failed bool
	frameProxy.dispatchFn = func(opcode uint32, _ int, _ []byte) {
		switch opcode {
		case 0: // transform
		case 1: // ready
			ready = true
		case 2: // failed
			failed = true
		}
	}
	{
		const msgSize = 8
		var buf [msgSize]byte
		wl.PutUint32(buf[0:], frameProxy.ID())
		wl.PutUint32(buf[4:], msgSize<<16|2)
		if err := ctx.WriteMsg(buf[:], nil); err != nil {
			return nil, fmt.Errorf("screen/ext: capture: %w", err)
		}
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
	out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	for y := rect.Min.Y; y < rect.Max.Y && y < int(si.height); y++ {
		for x := rect.Min.X; x < rect.Max.X && x < int(si.width); x++ {
			out.SetRGBA(x-rect.Min.X, y-rect.Min.Y, img.RGBAAt(x, y))
		}
	}
	return out, nil
}

func (b *ExtCaptureBackend) Close() error { return b.display.Context().Close() }
