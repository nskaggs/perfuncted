package screen

import (
	"fmt"
	"image"
	"sync"
	"syscall"

	"github.com/nskaggs/perfuncted/internal/shmutil"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// WlrScreencopyBackend captures the screen using zwlr_screencopy_manager_v1.
// This protocol is advertised by wlroots-based compositors (Sway, Hyprland, etc.)
// and is detected at runtime by enumerating compositor globals.
// Each Grab() opens a fresh Wayland connection and closes it on return,
// matching the one-shot pattern used by grim and other capture tools.
type WlrScreencopyBackend struct {
	sock string
}

// cached Wayland contexts per socket to avoid reconnecting for each Grab.
// Access to each cached context is serialized via cachedWlrCtx.mu because
// wl.Context is not safe for concurrent use.
var (
	wlrCacheMu sync.Mutex
	wlrCaches  = map[string]*cachedWlrCtx{}
)

type cachedWlrCtx struct {
	ctx *wl.Context
	mu  sync.Mutex
}

// withWlrContext ensures a wl.Context exists for sock and calls fn while
// holding the per-context lock to serialize access. If the context appears
// broken (fn returns an error), the context is closed and reset.
func withWlrContext(sock string, fn func(ctx *wl.Context) error) error {
	wlrCacheMu.Lock()
	c := wlrCaches[sock]
	if c == nil {
		c = &cachedWlrCtx{}
		wlrCaches[sock] = c
	}
	wlrCacheMu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx == nil {
		ctx, err := wl.Connect(sock)
		if err != nil {
			return fmt.Errorf("screen/wlr: connect: %w", err)
		}
		c.ctx = ctx
	}
	if err := fn(c.ctx); err != nil {
		// reset cached context on error to allow reconnect next time
		_ = c.ctx.Close()
		c.ctx = nil
		return err
	}
	return nil
}

// NewWlrScreencopyBackend verifies that zwlr_screencopy_manager_v1 is
// advertised on WAYLAND_DISPLAY and returns a backend if so.
func NewWlrScreencopyBackend() (*WlrScreencopyBackend, error) {
	sock := wl.SocketPath()
	if sock == "" {
		return nil, fmt.Errorf("screen/wlr: WAYLAND_DISPLAY not set")
	}

	s, err := wl.NewSession(sock)
	if err != nil {
		return nil, fmt.Errorf("screen/wlr: %w", err)
	}
	// NewSession performed a round-trip and collected globals.
	if _, ok := s.Globals["zwlr_screencopy_manager_v1"]; !ok {
		_ = s.Close()
		return nil, fmt.Errorf("screen/wlr: compositor does not advertise zwlr_screencopy_manager_v1")
	}
	_ = s.Close()
	return &WlrScreencopyBackend{sock: sock}, nil
}

// Grab captures the entire output and returns the cropped rect.
// Reuses a cached Wayland connection to reduce connect/close overhead.
func (b *WlrScreencopyBackend) Grab(rect image.Rectangle) (image.Image, error) {
	var outImg image.Image
	if err := withWlrContext(b.sock, func(ctx *wl.Context) error {
		display := wl.NewDisplay(ctx)
		registry, err := display.GetRegistry()
		if err != nil {
			return fmt.Errorf("screen/wlr: get registry: %w", err)
		}

		var shm *wl.Shm
		var output *wl.Output
		var mgrName, mgrVer uint32

		registry.SetGlobalHandler(func(ev wl.GlobalEvent) {
			switch ev.Interface {
			case "zwlr_screencopy_manager_v1":
				mgrName = ev.Name
				mgrVer = ev.Version
			case "wl_output":
				if output == nil {
					out := &wl.Output{}
					ctx.Register(out)
					ver := min(ev.Version, 4)
					if err := registry.Bind(ev.Name, ev.Interface, ver, out.ID()); err == nil {
						output = out
					}
				}
			case "wl_shm":
				s := &wl.Shm{}
				ctx.Register(s)
				if err := registry.Bind(ev.Name, ev.Interface, 1, s.ID()); err == nil {
					shm = s
				}
			}
		})

		if err := display.RoundTrip(); err != nil {
			return fmt.Errorf("screen/wlr: registry round-trip: %w", err)
		}
		if mgrName == 0 {
			return fmt.Errorf("screen/wlr: zwlr_screencopy_manager_v1 not found")
		}
		if shm == nil || output == nil {
			return fmt.Errorf("screen/wlr: wl_shm or wl_output missing")
		}

		mgrProxy := &wlRawProxy{}
		ctx.Register(mgrProxy)
		if err := registry.Bind(mgrName, "zwlr_screencopy_manager_v1", min(mgrVer, 3), mgrProxy.ID()); err != nil {
			return fmt.Errorf("screen/wlr: bind manager: %w", err)
		}

		frameProxy := &wlRawProxy{}
		ctx.Register(frameProxy)

		if err := wlSendCaptureOutput(ctx, mgrProxy.ID(), 1, output.ID(), frameProxy.ID()); err != nil {
			return fmt.Errorf("screen/wlr: capture_output: %w", err)
		}

		type bufInfo struct{ format, width, height, stride uint32 }
		var bi bufInfo
		var ready, failed, bufDone bool

		frameProxy.dispatchFn = func(opcode uint32, _ int, data []byte) {
			switch opcode {
			case 0: // buffer
				bi.format = wl.Uint32(data[0:4])
				bi.width = wl.Uint32(data[4:8])
				bi.height = wl.Uint32(data[8:12])
				bi.stride = wl.Uint32(data[12:16])
			case 2: // ready
				ready = true
			case 3: // failed
				failed = true
			case 6: // buffer_done (protocol v2+)
				bufDone = true
			}
		}

		for !bufDone && bi.width == 0 && !failed {
			if err := ctx.Dispatch(); err != nil {
				return fmt.Errorf("screen/wlr: dispatch: %w", err)
			}
		}
		if failed {
			return fmt.Errorf("screen/wlr: compositor signalled frame failed")
		}

		size := int(bi.stride * bi.height)
		f, err := shmutil.CreateFile(int64(size))
		if err != nil {
			return fmt.Errorf("screen/wlr: shm file: %w", err)
		}
		defer f.Close()

		pixels, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
		if err != nil {
			return fmt.Errorf("screen/wlr: mmap: %w", err)
		}
		defer syscall.Munmap(pixels) //nolint:errcheck

		pool, err := shm.CreatePool(int(f.Fd()), int32(size))
		if err != nil {
			return fmt.Errorf("screen/wlr: create_pool: %w", err)
		}
		defer pool.Destroy() //nolint:errcheck

		buf, err := pool.CreateBuffer(0, int32(bi.width), int32(bi.height), int32(bi.stride), bi.format)
		if err != nil {
			return fmt.Errorf("screen/wlr: create_buffer: %w", err)
		}
		defer buf.Destroy() //nolint:errcheck

		if err := wlSendFrameCopy(ctx, frameProxy.ID(), buf.ID()); err != nil {
			return fmt.Errorf("screen/wlr: frame copy: %w", err)
		}

		for !ready && !failed {
			if err := ctx.Dispatch(); err != nil {
				return fmt.Errorf("screen/wlr: dispatch: %w", err)
			}
		}
		if failed {
			return fmt.Errorf("screen/wlr: compositor signalled frame failed after copy")
		}

		img := decodeBGRA(pixels, int(bi.width), int(bi.height), int(bi.stride))

		if rect.Dx() <= 0 || rect.Dy() <= 0 {
			outImg = img
			return nil
		}

		outImg = cropRGBA(img, rect)
		return nil
	}); err != nil {
		return nil, err
	}
	return outImg, nil
}

// Resolution returns the output resolution by performing a full screencopy
// and reading the buffer dimensions from the compositor.
func (b *WlrScreencopyBackend) Resolution() (int, int, error) {
	img, err := b.Grab(image.Rect(0, 0, 0, 0))
	if err != nil {
		return 0, 0, err
	}
	bounds := img.Bounds()
	return bounds.Dx(), bounds.Dy(), nil
}

func (b *WlrScreencopyBackend) Close() error {
	wlrCacheMu.Lock()
	c := wlrCaches[b.sock]
	if c != nil {
		// lock per-context to ensure no Grab is in progress
		c.mu.Lock()
		if c.ctx != nil {
			_ = c.ctx.Close()
			c.ctx = nil
		}
		c.mu.Unlock()
		delete(wlrCaches, b.sock)
	}
	wlrCacheMu.Unlock()
	return nil
}

// ── shared Wayland helpers used by multiple Wayland backends ─────────────────

// wlRawProxy is a Dispatcher backed by a user-supplied function.
// Used to implement custom protocols without generated bindings.
type wlRawProxy struct {
	wl.BaseProxy
	dispatchFn func(opcode uint32, fd int, data []byte)
}

func (p *wlRawProxy) Dispatch(opcode uint32, fd int, data []byte) {
	if p.dispatchFn != nil {
		p.dispatchFn(opcode, fd, data)
	}
}

// wlSendCaptureOutput sends zwlr_screencopy_manager_v1.capture_output.
// Wire layout: [new_id:frame][int:overlay_cursor][object:output]
func wlSendCaptureOutput(ctx *wl.Context, mgrID, overlayCursor, outputID, frameID uint32) error {
	const msgSize = 8 + 4 + 4 + 4
	var buf [msgSize]byte
	wl.PutUint32(buf[0:], mgrID)
	wl.PutUint32(buf[4:], uint32(msgSize<<16)) // opcode 0: capture_output
	wl.PutUint32(buf[8:], frameID)
	wl.PutUint32(buf[12:], overlayCursor)
	wl.PutUint32(buf[16:], outputID)
	return ctx.WriteMsg(buf[:], nil)
}

// wlSendFrameCopy sends zwlr_screencopy_frame_v1.copy(buffer).
func wlSendFrameCopy(ctx *wl.Context, frameID, bufID uint32) error {
	const msgSize = 8 + 4
	var buf [msgSize]byte
	wl.PutUint32(buf[0:], frameID)
	wl.PutUint32(buf[4:], uint32(msgSize<<16)) // opcode 0: copy
	wl.PutUint32(buf[8:], bufID)
	return ctx.WriteMsg(buf[:], nil)
}
