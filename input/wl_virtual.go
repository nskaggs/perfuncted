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
	"fmt"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Button codes (Linux input event codes)
const (
	btnLeft   = 0x110
	btnRight  = 0x111
	btnMiddle = 0x112
)

// WlVirtualBackend implements Inputter for wlroots Wayland compositors.
type WlVirtualBackend struct {
	display *wl.Display
	ptr     *wl.RawProxy // zwlr_virtual_pointer_v1
	kbd     *wlKeyboard
	outW    uint32 // output width
	outH    uint32 // output height
}

// NewWlVirtualBackend connects to sock and initialises virtual pointer and keyboard.
// The output dimensions are probed from the first wl_output advertised.
func NewWlVirtualBackend(sock string) (*WlVirtualBackend, error) {
	ctx, err := wl.Connect(sock)
	if err != nil {
		return nil, fmt.Errorf("input/wl-virtual: connect: %w", err)
	}
	display := wl.NewDisplay(ctx)
	registry, err := display.GetRegistry()
	if err != nil {
		ctx.Close()
		return nil, fmt.Errorf("input/wl-virtual: get registry: %w", err)
	}

	b := &WlVirtualBackend{display: display}

	var ptrMgrID, ptrMgrVer uint32
	var kbdMgrID, kbdMgrVer uint32
	var outID, seatID uint32

	registry.SetGlobalHandler(func(ev wl.GlobalEvent) {
		switch ev.Interface {
		case "zwlr_virtual_pointer_manager_v1":
			ptrMgrID = ev.Name
			ptrMgrVer = ev.Version
		case "zwp_virtual_keyboard_manager_v1":
			kbdMgrID = ev.Name
			kbdMgrVer = ev.Version
		case "wl_seat":
			if seatID == 0 {
				seatID = ev.Name
			}
		case "wl_output":
			if outID == 0 {
				outID = ev.Name
			}
		}
	})
	if err := display.RoundTrip(); err != nil {
		ctx.Close()
		return nil, fmt.Errorf("input/wl-virtual: registry roundtrip: %w", err)
	}
	if ptrMgrID == 0 {
		ctx.Close()
		return nil, fmt.Errorf("input/wl-virtual: zwlr_virtual_pointer_manager_v1 not advertised")
	}
	if kbdMgrID == 0 {
		ctx.Close()
		return nil, fmt.Errorf("input/wl-virtual: zwp_virtual_keyboard_manager_v1 not advertised")
	}
	if seatID == 0 {
		ctx.Close()
		return nil, fmt.Errorf("input/wl-virtual: no wl_seat advertised")
	}

	// Bind virtual pointer manager.
	mgrProxy := &wl.RawProxy{}
	ctx.Register(mgrProxy)
	if err := registry.Bind(ptrMgrID, "zwlr_virtual_pointer_manager_v1", min(ptrMgrVer, 2), mgrProxy.ID()); err != nil {
		ctx.Close()
		return nil, fmt.Errorf("input/wl-virtual: bind virtual pointer manager: %w", err)
	}

	// Bind wl_output to read dimensions.
	outProxy := &wl.RawProxy{}
	ctx.Register(outProxy)
	b.outW, b.outH = 1920, 1080 // fallback
	if outID != 0 {
		if err := registry.Bind(outID, "wl_output", 1, outProxy.ID()); err == nil {
			outProxy.OnEvent = func(opcode uint32, _ int, data []byte) {
				if opcode == 1 && len(data) >= 12 { // mode event: flags, width, height, refresh
					b.outW = wl.Uint32(data[4:8])
					b.outH = wl.Uint32(data[8:12])
				}
			}
			display.RoundTrip() //nolint:errcheck
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
	if err := ctx.WriteMsg(buf[:], nil); err != nil {
		ctx.Close()
		return nil, fmt.Errorf("input/wl-virtual: create virtual pointer: %w", err)
	}

	kbd, err := newWlKeyboard(ctx, registry, kbdMgrID, kbdMgrVer, seatID)
	if err != nil {
		ctx.Close()
		return nil, fmt.Errorf("input/wl-virtual: %w", err)
	}
	b.kbd = kbd

	return b, nil
}

func (b *WlVirtualBackend) now() uint32 {
	return uint32(time.Now().UnixMilli() & 0xffffffff)
}

func (b *WlVirtualBackend) ptrFrame() error {
	var buf [8]byte
	wl.PutUint32(buf[0:], b.ptr.ID())
	wl.PutUint32(buf[4:], 8<<16|4) // size=8, opcode=4 (frame)
	return b.display.Context().WriteMsg(buf[:], nil)
}

// MouseMove moves the pointer to absolute position (x, y) in the compositor's
// output coordinate space (i.e. sway display pixels).
func (b *WlVirtualBackend) MouseMove(x, y int) error {
	var buf [28]byte
	wl.PutUint32(buf[0:], b.ptr.ID())
	wl.PutUint32(buf[4:], 28<<16|1) // size=28, opcode=1 (motion_absolute)
	wl.PutUint32(buf[8:], b.now())
	wl.PutUint32(buf[12:], uint32(x))
	wl.PutUint32(buf[16:], uint32(y))
	wl.PutUint32(buf[20:], b.outW)
	wl.PutUint32(buf[24:], b.outH)
	if err := b.display.Context().WriteMsg(buf[:], nil); err != nil {
		return err
	}
	return b.ptrFrame()
}

func (b *WlVirtualBackend) button(code, state uint32) error {
	var buf [20]byte
	wl.PutUint32(buf[0:], b.ptr.ID())
	wl.PutUint32(buf[4:], 20<<16|2) // size=20, opcode=2 (button)
	wl.PutUint32(buf[8:], b.now())
	wl.PutUint32(buf[12:], code)
	wl.PutUint32(buf[16:], state)
	if err := b.display.Context().WriteMsg(buf[:], nil); err != nil {
		return err
	}
	return b.ptrFrame()
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
func (b *WlVirtualBackend) MouseDown(button int) error { return b.button(btnCode(button), 1) }

// MouseUp releases a mouse button.
func (b *WlVirtualBackend) MouseUp(button int) error { return b.button(btnCode(button), 0) }

// MouseClick moves to (x,y) then clicks the given button.
func (b *WlVirtualBackend) MouseClick(x, y, button int) error {
	if err := b.MouseMove(x, y); err != nil {
		return err
	}
	time.Sleep(40 * time.Millisecond)
	if err := b.MouseDown(button); err != nil {
		return err
	}
	time.Sleep(40 * time.Millisecond)
	return b.MouseUp(button)
}

// Type sends a string as keyboard events.
func (b *WlVirtualBackend) Type(s string) error { return b.kbd.typeString(s) }

// KeyTap presses and releases a key, respecting any held modifiers.
func (b *WlVirtualBackend) KeyTap(key string) error { return b.kbd.tapKey(key) }

// KeyDown presses and holds a key. Modifier keys update the compositor's
// modifier state; other keys are held until released with KeyUp.
func (b *WlVirtualBackend) KeyDown(key string) error { return b.kbd.pressKey(key) }

// KeyUp releases a previously held key.
func (b *WlVirtualBackend) KeyUp(key string) error { return b.kbd.releaseKey(key) }

// scroll sends an axis event for the given number of discrete scroll notches.
// axis 0 = vertical, axis 1 = horizontal. Positive values scroll down/right;
// negative values scroll up/left.
func (b *WlVirtualBackend) scroll(axis uint32, clicks int) error {
	// wl_pointer.axis: value in wl_fixed_t (24.8 fixed-point).
	// Convention: ~15 pixels per discrete scroll notch.
	value := int32(clicks * 15 * 256) // wl_fixed_t
	var buf [20]byte
	wl.PutUint32(buf[0:], b.ptr.ID())
	wl.PutUint32(buf[4:], 20<<16|3) // size=20, opcode=3 (axis)
	wl.PutUint32(buf[8:], b.now())
	wl.PutUint32(buf[12:], axis)          // 0=vertical, 1=horizontal
	wl.PutUint32(buf[16:], uint32(value)) // wl_fixed_t signed value
	if err := b.display.Context().WriteMsg(buf[:], nil); err != nil {
		return err
	}
	return b.ptrFrame()
}

// ScrollUp scrolls the mouse wheel up by the given number of notches.
func (b *WlVirtualBackend) ScrollUp(clicks int) error { return b.scroll(0, -clicks) }

// ScrollDown scrolls the mouse wheel down by the given number of notches.
func (b *WlVirtualBackend) ScrollDown(clicks int) error { return b.scroll(0, clicks) }

// ScrollLeft scrolls the mouse wheel left by the given number of notches.
func (b *WlVirtualBackend) ScrollLeft(clicks int) error { return b.scroll(1, -clicks) }

// ScrollRight scrolls the mouse wheel right by the given number of notches.
func (b *WlVirtualBackend) ScrollRight(clicks int) error { return b.scroll(1, clicks) }

// Close closes the Wayland connection.
func (b *WlVirtualBackend) Close() error { return b.display.Context().Close() }
