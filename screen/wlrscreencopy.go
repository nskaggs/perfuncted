package screen

import (
	"context"
	"fmt"
	"hash/crc32"
	"image"
	"sync"
	"syscall"
	"time"

	"github.com/nskaggs/perfuncted/internal/shmutil"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// GrabFullHash returns a CRC32 checksum of the entire screen.
// Bypasses intermediate image allocation by hashing the raw buffer directly.
func (b *WlrScreencopyBackend) GrabFullHash(ctx context.Context) (uint32, error) {
	var hash uint32
	if err := b.withWlrContext(func(ctx *wl.Context) error {
		display := wl.NewDisplay(ctx)
		registry, err := display.GetRegistry()
		if err != nil {
			return fmt.Errorf("screen/wlr: get registry: %w", err)
		}

		var shm *wl.Shm
		var output *wlRawProxy
		var mgrName, mgrVer uint32

		registry.SetGlobalHandler(func(ev wl.GlobalEvent) {
			switch ev.Interface {
			case "zwlr_screencopy_manager_v1":
				mgrName = ev.Name
				mgrVer = ev.Version
			case "wl_output":
				if output == nil {
					out := &wlRawProxy{}
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

		// Calculate CRC32 IEEE of the raw buffer.
		hash = crc32.ChecksumIEEE(pixels)
		return nil
	}); err != nil {
		return 0, err
	}
	return hash, nil
}

// WlrScreencopyBackend captures the screen using zwlr_screencopy_manager_v1.
// This protocol is advertised by wlroots-based compositors (Sway, Hyprland, etc.)
// and is detected at runtime by enumerating compositor globals.
// Each Grab() opens a fresh Wayland connection and closes it on return,
// matching the one-shot pattern used by grim and other capture tools.
// (moved) see constructor NewWlrScreencopyBackendWithConnector for fields.
// per-backend cached Wayland context to avoid reconnecting for each Grab.
// Access is serialized via b.ctxMu because wl.Context is not safe for concurrent use.
var (
	// default TTL for cached contexts; tests may override via SetWlrCacheTTL
	defaultWlrCacheTTL = 5 * time.Minute
	// global wlr cached contexts: cap and bookkeeping
	maxWlrCachedContexts = 4
	globalWlrMu          sync.Mutex
	globalWlrCtxs        = make(map[*wl.Context]time.Time)
)

// NewWlrScreencopyBackendWithConnector constructs a backend with an injectable
// connector and TTL (used by tests). Use NewWlrScreencopyBackend for normal use.
func NewWlrScreencopyBackendWithConnector(sock string, connect func(string) (*wl.Context, error), ttl time.Duration) *WlrScreencopyBackend {
	if connect == nil {
		connect = wl.Connect
	}
	if ttl <= 0 {
		ttl = defaultWlrCacheTTL
	}
	b := &WlrScreencopyBackend{sock: sock}
	b.connect = connect
	b.ttl = ttl
	b.done = make(chan struct{})
	return b
}

type WlrScreencopyBackend struct {
	sock     string
	ctxMu    sync.Mutex
	ctx      *wl.Context
	lastUsed time.Time
	connect  func(string) (*wl.Context, error)
	ttl      time.Duration
	janOnce  sync.Once
	done     chan struct{}
	// last observed output scale (1 if unknown)
	scale uint32
}

// withWlrContext ensures a wl.Context exists for this backend and calls fn
// while holding the per-backend lock to serialize access. If the context
// appears broken (fn returns an error), the context is closed and reset. A
// background janitor evicts idle contexts after b.ttl.
func (b *WlrScreencopyBackend) withWlrContext(fn func(ctx *wl.Context) error) error {
	b.janOnce.Do(func() {
		go func() {
			interval := b.ttl / 10
			if interval < time.Millisecond {
				interval = time.Millisecond
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					b.ctxMu.Lock()
					if b.ctx != nil {
						idle := time.Since(b.lastUsed)
						if idle > b.ttl {
							_ = wl.SafeClose(b.ctx)
							b.ctx = nil
						}
					}
					b.ctxMu.Unlock()
				case <-b.done:
					return
				}
			}
		}()
	})

	b.ctxMu.Lock()
	defer b.ctxMu.Unlock()
	if b.ctx == nil {
		ctx, err := b.connect(b.sock)
		if err != nil {
			return fmt.Errorf("screen/wlr: connect: %w", err)
		}
		b.ctx = ctx
		// register in global cache
		globalWlrMu.Lock()
		globalWlrCtxs[b.ctx] = time.Now()
		// enforce cap
		if len(globalWlrCtxs) > maxWlrCachedContexts {
			// find oldest and evict until cap satisfied
			for len(globalWlrCtxs) > maxWlrCachedContexts {
				var oldest *wl.Context
				var oldestT time.Time
				for c, t := range globalWlrCtxs {
					if oldest == nil || t.Before(oldestT) {
						oldest = c
						oldestT = t
					}
				}
				if oldest != nil {
					_ = wl.SafeClose(oldest)
					delete(globalWlrCtxs, oldest)
				}
			}
		}
		globalWlrMu.Unlock()
	}
	b.lastUsed = time.Now()
	if err := fn(b.ctx); err != nil {
		// close and remove from global cache
		_ = wl.SafeClose(b.ctx)
		globalWlrMu.Lock()
		delete(globalWlrCtxs, b.ctx)
		globalWlrMu.Unlock()
		b.ctx = nil
		return err
	}
	b.lastUsed = time.Now()
	// update global last-used
	globalWlrMu.Lock()
	if b.ctx != nil {
		globalWlrCtxs[b.ctx] = b.lastUsed
	}
	globalWlrMu.Unlock()
	return nil
}

// SetWlrCacheTTL allows tests to tune the default TTL used by backends created
// with NewWlrScreencopyBackend. Backends created via the WithConnector variant
// may pass a custom TTL directly.
func SetWlrCacheTTL(d time.Duration) { defaultWlrCacheTTL = d }

// NewWlrScreencopyBackend verifies that zwlr_screencopy_manager_v1 is
// advertised on WAYLAND_DISPLAY and returns a backend if so.
func NewWlrScreencopyBackend() (*WlrScreencopyBackend, error) {
	return NewWlrScreencopyBackendForSocket(wl.SocketPath())
}

// NewWlrScreencopyBackendForSocket verifies that zwlr_screencopy_manager_v1 is
// advertised on sock and returns a backend if so.
func NewWlrScreencopyBackendForSocket(sock string) (*WlrScreencopyBackend, error) {
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
	b := NewWlrScreencopyBackendWithConnector(sock, wl.Connect, defaultWlrCacheTTL)
	return b, nil
}

// Grab captures the entire output and returns the cropped rect.
// Reuses a cached Wayland connection to reduce connect/close overhead.
func (b *WlrScreencopyBackend) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	var outImg image.Image
	if err := b.withWlrContext(func(ctx *wl.Context) error {
		display := wl.NewDisplay(ctx)
		registry, err := display.GetRegistry()
		if err != nil {
			return fmt.Errorf("screen/wlr: get registry: %w", err)
		}

		var shm *wl.Shm
		var output *wlRawProxy
		var outputScale uint32 = 1
		var mgrName, mgrVer uint32

		registry.SetGlobalHandler(func(ev wl.GlobalEvent) {
			switch ev.Interface {
			case "zwlr_screencopy_manager_v1":
				mgrName = ev.Name
				mgrVer = ev.Version
			case "wl_output":
				if output == nil {
					out := &wlRawProxy{}
					ctx.Register(out)
					ver := min(ev.Version, 4)
					if err := registry.Bind(ev.Name, ev.Interface, ver, out.ID()); err == nil {
						output = out
						// capture mode and scale events
						out.dispatchFn = func(opcode uint32, _ int, data []byte) {
							switch opcode {
							case 1: // mode
								// mode provides physical size; currently we only care about scale
								// (captured buffer dimensions are used directly).
								// keep the case so Dispatch pumps it.
							case 3: // scale
								if len(data) >= 4 {
									outputScale = wl.Uint32(data[0:4])
									if outputScale == 0 {
										outputScale = 1
									}
								}
							}
						}
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
	img, err := b.Grab(context.Background(), image.Rect(0, 0, 0, 0))
	if err != nil {
		return 0, 0, err
	}
	bounds := img.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	if b.scale > 1 {
		w = w / int(b.scale)
		h = h / int(b.scale)
	}
	return w, h, nil
}

func (b *WlrScreencopyBackend) Close() error {
	// stop janitor and close any active context
	if b.done != nil {
		close(b.done)
	}
	b.ctxMu.Lock()
	if b.ctx != nil {
		_ = wl.SafeClose(b.ctx)
		b.ctx = nil
	}
	b.ctxMu.Unlock()
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
