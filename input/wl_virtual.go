//go:build linux
// +build linux

package input

// WlVirtualBackend injects input directly into a Wayland compositor using:
//   - zwlr_virtual_pointer_manager_v1 for absolute mouse movement and clicks
//   - zwp_virtual_keyboard_v1 for layout-independent keyboard (custom XKB keymap)
//
// This backend is preferred over uinput on wlroots compositors (sway, Hyprland)
// because it operates in the compositor's own coordinate space and does not
// require the outer compositor to relay events.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nskaggs/perfuncted/ctxutil"
	"github.com/nskaggs/perfuncted/internal/wl"
)

var _ Inputter = (*WlVirtualBackend)(nil)

// Button codes (Linux input event codes)
const (
	btnLeft   = 0x110
	btnRight  = 0x111
	btnMiddle = 0x112
)

// WlVirtualBackend implements Inputter for wlroots Wayland compositors.
type WlVirtualBackend struct {
	// mu protects session, display, ptr, kbd, and output dimensions.
	mu       sync.Mutex
	session  *wl.Session
	ptr      *wl.RawProxy // zwlr_virtual_pointer_v1
	kbd      *wlKeyboard
	outW     uint32 // logical output width
	outH     uint32 // logical output height
	outPhysW uint32 // physical output width (pixels)
	outPhysH uint32 // physical output height (pixels)
	outScale uint32 // wl_output scale factor
}

// NewWlVirtualBackend connects to sock and initialises virtual pointer and keyboard.
// The output dimensions are probed from the first wl_output advertised.
func NewWlVirtualBackend(sock string) (*WlVirtualBackend, error) { //nolint:gocyclo
	// Use the helper to connect and enumerate globals.
	s, err := wl.NewSession(sock)
	if err != nil {
		return nil, fmt.Errorf("input/wl-virtual: %w", err)
	}
	b := &WlVirtualBackend{session: s}

	var ptrMgrID, ptrMgrVer uint32
	var kbdMgrID, kbdMgrVer uint32
	var outID, seatID uint32

	if ev, ok := s.Globals["zwlr_virtual_pointer_manager_v1"]; ok {
		ptrMgrID = ev.Name
		ptrMgrVer = ev.Version
	}
	if ev, ok := s.Globals["zwp_virtual_keyboard_manager_v1"]; ok {
		kbdMgrID = ev.Name
		kbdMgrVer = ev.Version
	}
	if ev, ok := s.Globals["wl_seat"]; ok {
		seatID = ev.Name
	}
	if ev, ok := s.Globals["wl_output"]; ok {
		outID = ev.Name
	}

	if ptrMgrID == 0 {
		_ = s.Close()
		return nil, fmt.Errorf("input/wl-virtual: zwlr_virtual_pointer_manager_v1 not advertised")
	}
	if kbdMgrID == 0 {
		_ = s.Close()
		return nil, fmt.Errorf("input/wl-virtual: zwp_virtual_keyboard_v1 not advertised")
	}
	if seatID == 0 {
		_ = s.Close()
		return nil, fmt.Errorf("input/wl-virtual: no wl_seat advertised")
	}

	// Bind virtual pointer manager.
	registry := s.Registry
	ctx := s.Ctx
	initErr := wl.WithOperation(ctx, func() error {
		mgrProxy := &wl.RawProxy{}
		ctx.Register(mgrProxy)
		if bindErr := registry.Bind(ptrMgrID, "zwlr_virtual_pointer_manager_v1", min(ptrMgrVer, 2), mgrProxy.ID()); bindErr != nil {
			return fmt.Errorf("input/wl-virtual: bind virtual pointer manager: %w", bindErr)
		}

		// Bind wl_output to read dimensions.
		outProxy := &wl.RawProxy{}
		ctx.Register(outProxy)
		b.outW, b.outH = 1920, 1080 // fallback
		if outID != 0 {
			if bindErr := registry.Bind(outID, "wl_output", 1, outProxy.ID()); bindErr == nil {
				// Handle mode (physical size) and scale events and maintain logical dims.
				outProxy.OnEvent = func(opcode uint32, _ int, data []byte) {
					switch opcode {
					case 1: // mode: flags, width, height, refresh
						if len(data) >= 12 {
							b.outPhysW = wl.Uint32(data[4:8])
							b.outPhysH = wl.Uint32(data[8:12])
							if b.outScale == 0 {
								b.outScale = 1
							}
							b.outW = b.outPhysW / b.outScale
							b.outH = b.outPhysH / b.outScale
						}
					case 3: // scale
						if len(data) >= 4 {
							b.outScale = wl.Uint32(data[0:4])
							if b.outScale == 0 {
								b.outScale = 1
							}
							if b.outPhysW != 0 {
								b.outW = b.outPhysW / b.outScale
							}
							if b.outPhysH != 0 {
								b.outH = b.outPhysH / b.outScale
							}
						}
					}
				}
				if err := s.Display.RoundTrip(); err != nil {
					return fmt.Errorf("input/wl-virtual: output round-trip: %w", err)
				}
			}
		}

		// Create virtual pointer: manager.create_virtual_pointer(seat=null, new_id)
		b.ptr = &wl.RawProxy{}
		ctx.Register(b.ptr)
		var buf [16]byte
		wl.PutUint32(buf[0:], mgrProxy.ID())
		wl.PutUint32(buf[4:], 16<<16) // size=16, opcode=0 (create_virtual_pointer)
		wl.PutUint32(buf[8:], 0)      // seat = null
		wl.PutUint32(buf[12:], b.ptr.ID())
		if writeErr := ctx.WriteMsg(buf[:], nil); writeErr != nil {
			return fmt.Errorf("input/wl-virtual: create virtual pointer: %w", writeErr)
		}

		kbd, kbdErr := newWlKeyboard(ctx, registry, kbdMgrID, kbdMgrVer, seatID)
		if kbdErr != nil {
			return fmt.Errorf("input/wl-virtual: %w", kbdErr)
		}
		b.kbd = kbd
		return nil
	})
	if initErr != nil {
		_ = s.Close()
		return nil, initErr
	}
	return b, nil
}

func (b *WlVirtualBackend) now() uint32 {
	return uint32(time.Now().UnixMilli() & 0xffffffff)
}

func (b *WlVirtualBackend) withOperation(fn func(wl.Ctx) error) error {
	if b == nil || b.session == nil || b.session.Display == nil {
		return fmt.Errorf("input/wl-virtual: backend not initialised")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return wl.WithOperation(b.session.Display.Context(), func() error {
		return fn(b.session.Display.Context())
	})
}

func (b *WlVirtualBackend) ptrFrame(ctx wl.Ctx) error {
	var buf [8]byte
	wl.PutUint32(buf[0:], b.ptr.ID())
	wl.PutUint32(buf[4:], 8<<16|4) // size=8, opcode=4 (frame)
	return ctx.WriteMsg(buf[:], nil)
}

func (b *WlVirtualBackend) mouseMoveEvent(ctx wl.Ctx, x, y int) error {
	var buf [28]byte
	wl.PutUint32(buf[0:], b.ptr.ID())
	wl.PutUint32(buf[4:], 28<<16|1) // size=28, opcode=1 (motion_absolute)
	wl.PutUint32(buf[8:], b.now())
	wl.PutUint32(buf[12:], uint32(x*256)) // wl_fixed_t (24.8 fixed-point)
	wl.PutUint32(buf[16:], uint32(y*256))
	wl.PutUint32(buf[20:], b.outW)
	wl.PutUint32(buf[24:], b.outH)
	if err := ctx.WriteMsg(buf[:], nil); err != nil {
		return err
	}
	return b.ptrFrame(ctx)
}

func (b *WlVirtualBackend) buttonEvent(ctx wl.Ctx, code, state uint32) error {
	var buf [20]byte
	wl.PutUint32(buf[0:], b.ptr.ID())
	wl.PutUint32(buf[4:], 20<<16|2) // size=20, opcode=2 (button)
	wl.PutUint32(buf[8:], b.now())
	wl.PutUint32(buf[12:], code)
	wl.PutUint32(buf[16:], state)
	if err := ctx.WriteMsg(buf[:], nil); err != nil {
		return err
	}
	return b.ptrFrame(ctx)
}

// MouseMove moves the pointer to absolute position (x, y) in the compositor's
// output coordinate space (i.e. sway display pixels).
func (b *WlVirtualBackend) MouseMove(ctx context.Context, x, y int) error {
	ctx = ctxutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.withOperation(func(wlctx wl.Ctx) error {
		return b.mouseMoveEvent(wlctx, x, y)
	})
}

func (b *WlVirtualBackend) button(ctx context.Context, code, state uint32) error {
	ctx = ctxutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.withOperation(func(wlctx wl.Ctx) error {
		return b.buttonEvent(wlctx, code, state)
	})
}

func btnCode(button int) uint32 {
	switch button {
	case 2:
		return btnMiddle
	case 3:
		return btnRight
	default:
		return btnLeft
	}
}

// MouseDown presses a mouse button.
func (b *WlVirtualBackend) MouseDown(ctx context.Context, button int) error {
	return b.button(ctx, btnCode(button), 1)
}

// MouseUp releases a mouse button.
func (b *WlVirtualBackend) MouseUp(ctx context.Context, button int) error {
	return b.button(ctx, btnCode(button), 0)
}

// MouseClick moves to (x,y) then clicks the given button.
func (b *WlVirtualBackend) MouseClick(ctx context.Context, x, y, button int) error {
	ctx = ctxutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.withOperation(func(wlctx wl.Ctx) error {
		if err := b.mouseMoveEvent(wlctx, x, y); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.buttonEvent(wlctx, btnCode(button), 1); err != nil {
			return err
		}
		if err := sleepContext(ctx, 40*time.Millisecond); err != nil {
			if upErr := b.buttonEvent(wlctx, btnCode(button), 0); upErr != nil {
				return upErr
			}
			return err
		}
		return b.buttonEvent(wlctx, btnCode(button), 0)
	})
}

// Type sends a string as keyboard events using key syntax.
// Literal text is typed as-is; {keyname} sends named keys; modifier+key combos
// and {keyname down/up} are supported.
func (b *WlVirtualBackend) Type(ctx context.Context, s string) error {
	return b.typeContext(ctx, s)
}

func (b *WlVirtualBackend) typeContext(ctx context.Context, s string) error {
	ctx = ctxutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.withOperation(func(_ wl.Ctx) error {
		actions, err := parseKeySend(s)
		if err != nil {
			return err
		}
		return b.kbd.sendkeys(ctx, actions)
	})
}

// KeyDown presses and holds a key. Modifier keys update the compositor's
// modifier state; other keys are held until released with KeyUp.
func (b *WlVirtualBackend) KeyDown(ctx context.Context, key string) error {
	ctx = ctxutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.withOperation(func(_ wl.Ctx) error {
		return b.kbd.pressKey(key)
	})
}

// KeyUp releases a previously held key.
func (b *WlVirtualBackend) KeyUp(ctx context.Context, key string) error {
	ctx = ctxutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.withOperation(func(_ wl.Ctx) error {
		return b.kbd.releaseKey(key)
	})
}

// scroll sends an axis event for the given number of discrete scroll notches.
// axis 0 = vertical, axis 1 = horizontal. Positive values scroll down/right;
// negative values scroll up/left.
func (b *WlVirtualBackend) scroll(ctx context.Context, axis uint32, clicks int) error {
	ctx = ctxutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.withOperation(func(wlctx wl.Ctx) error {
		// wl_pointer.axis: value in wl_fixed_t (24.8 fixed-point).
		// Convention: ~15 pixels per discrete scroll notch.
		value := int32(clicks * 15 * 256) // wl_fixed_t
		var buf [20]byte
		wl.PutUint32(buf[0:], b.ptr.ID())
		wl.PutUint32(buf[4:], 20<<16|3) // size=20, opcode=3 (axis)
		wl.PutUint32(buf[8:], b.now())
		wl.PutUint32(buf[12:], axis)          // 0=vertical, 1=horizontal
		wl.PutUint32(buf[16:], uint32(value)) // wl_fixed_t signed value
		if err := wlctx.WriteMsg(buf[:], nil); err != nil {
			return err
		}
		return b.ptrFrame(wlctx)
	})
}

// ScrollUp scrolls the mouse wheel up by the given number of notches.
func (b *WlVirtualBackend) ScrollUp(ctx context.Context, clicks int) error {
	return b.scroll(ctx, 0, -clicks)
}

// ScrollDown scrolls the mouse wheel down by the given number of notches.
func (b *WlVirtualBackend) ScrollDown(ctx context.Context, clicks int) error {
	return b.scroll(ctx, 0, clicks)
}

// ScrollLeft scrolls the mouse wheel left by the given number of notches.
func (b *WlVirtualBackend) ScrollLeft(ctx context.Context, clicks int) error {
	return b.scroll(ctx, 1, -clicks)
}

// ScrollRight scrolls the mouse wheel right by the given number of notches.
func (b *WlVirtualBackend) ScrollRight(ctx context.Context, clicks int) error {
	return b.scroll(ctx, 1, clicks)
}

func (b *WlVirtualBackend) PointerLocation(ctx context.Context) (int, int, error) {
	ctx = ctxutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	return 0, 0, unsupportedError("input/wl-virtual", "pointer location")
}

func (b *WlVirtualBackend) Sync(ctx context.Context) error {
	ctx = ctxutil.Default(ctx)
	return ctx.Err()
}

// Close closes the Wayland connection. Safe to call multiple times and in any
// state (session may be nil if partially constructed).
func (b *WlVirtualBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.session != nil {
		return b.session.Close()
	}
	return nil
}
