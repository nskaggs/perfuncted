//go:build linux
// +build linux

package input

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

// XTestBackend injects keyboard and mouse events via the X11 XTEST extension.
// It only works on X11 or XWayland sessions. Prefer UinputBackend when available.
type XTestBackend struct {
	conn  *xgb.Conn
	root  xproto.Window
	delay time.Duration
}

// NewXTestBackend connects to the named X11 display and initialises XTEST.
// Pass an empty string to use the DISPLAY environment variable.
func NewXTestBackend(displayName string) (*XTestBackend, error) {
	conn, err := xgb.NewConnDisplay(displayName)
	if err != nil {
		return nil, fmt.Errorf("input/xtest: connect to display %q: %w", displayName, err)
	}
	if err := xtest.Init(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("input/xtest: init XTEST: %w", err)
	}
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	return &XTestBackend{conn: conn, root: root, delay: 50 * time.Millisecond}, nil
}

// keysymForName maps a key name to an X11 keysym value.
var keysymForName = map[string]xproto.Keysym{
	"a": 0x61, "b": 0x62, "c": 0x63, "d": 0x64, "e": 0x65,
	"f": 0x66, "g": 0x67, "h": 0x68, "i": 0x69, "j": 0x6a,
	"k": 0x6b, "l": 0x6c, "m": 0x6d, "n": 0x6e, "o": 0x6f,
	"p": 0x70, "q": 0x71, "r": 0x72, "s": 0x73, "t": 0x74,
	"u": 0x75, "v": 0x76, "w": 0x77, "x": 0x78, "y": 0x79, "z": 0x7a,
	"0": 0x30, "1": 0x31, "2": 0x32, "3": 0x33, "4": 0x34,
	"5": 0x35, "6": 0x36, "7": 0x37, "8": 0x38, "9": 0x39,
	" ": 0x20, "space": 0x20,
	"return": 0xff0d, "enter": 0xff0d,
	"tab":    0xff09,
	"escape": 0xff1b, "esc": 0xff1b,
	"up": 0xff52, "down": 0xff54, "left": 0xff51, "right": 0xff53,
	"ctrl": 0xffe3, "shift": 0xffe1, "alt": 0xffe9, "super": 0xffeb,
	"f1": 0xffbe, "f2": 0xffbf, "f3": 0xffc0, "f4": 0xffc1,
	"f5": 0xffc2, "f6": 0xffc3, "f7": 0xffc4, "f8": 0xffc5,
	"f9": 0xffc6, "f10": 0xffc7, "f11": 0xffc8, "f12": 0xffc9,
}

func (b *XTestBackend) keycodeFor(key string) (xproto.Keycode, error) {
	sym, ok := keysymForName[strings.ToLower(key)]
	if !ok && len(key) == 1 {
		sym = xproto.Keysym(key[0])
		ok = true
	}
	if !ok {
		return 0, fmt.Errorf("input/xtest: unknown key %q", key)
	}
	km, err := xproto.GetKeyboardMapping(b.conn,
		xproto.Setup(b.conn).MinKeycode,
		byte(xproto.Setup(b.conn).MaxKeycode-xproto.Setup(b.conn).MinKeycode+1)).Reply()
	if err != nil {
		return 0, fmt.Errorf("input/xtest: GetKeyboardMapping: %w", err)
	}
	kpk := int(km.KeysymsPerKeycode)
	min := int(xproto.Setup(b.conn).MinKeycode)
	for i, s := range km.Keysyms {
		if s == sym {
			return xproto.Keycode(min + i/kpk), nil
		}
	}
	return 0, fmt.Errorf("input/xtest: keysym 0x%x for key %q not found in keymap", sym, key)
}

func (b *XTestBackend) KeyDown(ctx context.Context, key string) error {
	kc, err := b.keycodeFor(key)
	if err != nil {
		return err
	}
	return xtest.FakeInputChecked(b.conn, xproto.KeyPress, byte(kc), xproto.TimeCurrentTime, b.root, 0, 0, 0).Check()
}

// KeyUp releases a previously held key.
func (b *XTestBackend) KeyUp(ctx context.Context, key string) error {
	kc, err := b.keycodeFor(key)
	if err != nil {
		return err
	}
	return xtest.FakeInputChecked(b.conn, xproto.KeyRelease, byte(kc), xproto.TimeCurrentTime, b.root, 0, 0, 0).Check()
}

func (b *XTestBackend) PressCombo(ctx context.Context, combo string) error {
	parts := strings.Split(strings.ToLower(combo), "+")
	kcs := make([]xproto.Keycode, 0, len(parts))
	for _, p := range parts {
		kc, err := b.keycodeFor(strings.TrimSpace(p))
		if err != nil {
			return err
		}
		kcs = append(kcs, kc)
	}
	// Press all
	for _, kc := range kcs {
		if err := xtest.FakeInputChecked(b.conn, xproto.KeyPress, byte(kc), xproto.TimeCurrentTime, b.root, 0, 0, 0).Check(); err != nil {
			return err
		}
	}
	time.Sleep(b.delay)
	// Release in reverse
	for i := len(kcs) - 1; i >= 0; i-- {
		if err := xtest.FakeInputChecked(b.conn, xproto.KeyRelease, byte(kcs[i]), xproto.TimeCurrentTime, b.root, 0, 0, 0).Check(); err != nil {
			return err
		}
	}
	return nil
}

func (b *XTestBackend) KeyTap(ctx context.Context, key string) error {

	if err := b.KeyDown(ctx, key); err != nil {
		return err
	}
	time.Sleep(b.delay)
	return b.KeyUp(ctx, key)
}

func (b *XTestBackend) Type(ctx context.Context, s string) error {
	return b.TypeContext(ctx, s)
}

func (b *XTestBackend) TypeContext(ctx context.Context, s string) error {
	for _, ch := range s {
		if err := b.KeyTap(ctx, string(ch)); err != nil {
			return err
		}
		time.Sleep(b.delay)
	}
	return nil
}

func (b *XTestBackend) MouseMove(ctx context.Context, x, y int) error {
	return xtest.FakeInputChecked(b.conn, xproto.MotionNotify, 0,
		xproto.TimeCurrentTime, b.root, int16(x), int16(y), 0).Check()
}

func (b *XTestBackend) MouseClick(ctx context.Context, x, y, button int) error {
	if err := b.MouseMove(ctx, x, y); err != nil {
		return err
	}
	if err := b.MouseDown(ctx, button); err != nil {
		return err
	}
	time.Sleep(b.delay)
	return b.MouseUp(ctx, button)
}

func (b *XTestBackend) MouseDown(ctx context.Context, button int) error {
	return xtest.FakeInputChecked(b.conn, xproto.ButtonPress, byte(button),
		xproto.TimeCurrentTime, b.root, 0, 0, 0).Check()
}

func (b *XTestBackend) MouseUp(ctx context.Context, button int) error {
	return xtest.FakeInputChecked(b.conn, xproto.ButtonRelease, byte(button),
		xproto.TimeCurrentTime, b.root, 0, 0, 0).Check()
}

// ScrollUp scrolls the mouse wheel up by the given number of notches.
// X11 scroll is button 4 (up) / 5 (down).
func (b *XTestBackend) ScrollUp(ctx context.Context, clicks int) error {
	for i := 0; i < clicks; i++ {
		if err := b.MouseDown(ctx, 4); err != nil {
			return err
		}
		if err := b.MouseUp(ctx, 4); err != nil {
			return err
		}
	}
	return nil
}

// ScrollDown scrolls the mouse wheel down by the given number of notches.
func (b *XTestBackend) ScrollDown(ctx context.Context, clicks int) error {
	for i := 0; i < clicks; i++ {
		if err := b.MouseDown(ctx, 5); err != nil {
			return err
		}
		if err := b.MouseUp(ctx, 5); err != nil {
			return err
		}
	}
	return nil
}

// ScrollLeft scrolls the mouse wheel left by the given number of notches.
// X11 scroll is button 6 (left) / 7 (right).
func (b *XTestBackend) ScrollLeft(ctx context.Context, clicks int) error {
	for i := 0; i < clicks; i++ {
		if err := b.MouseDown(ctx, 6); err != nil {
			return err
		}
		if err := b.MouseUp(ctx, 6); err != nil {
			return err
		}
	}
	return nil
}

// ScrollRight scrolls the mouse wheel right by the given number of notches.
func (b *XTestBackend) ScrollRight(ctx context.Context, clicks int) error {
	for i := 0; i < clicks; i++ {
		if err := b.MouseDown(ctx, 7); err != nil {
			return err
		}
		if err := b.MouseUp(ctx, 7); err != nil {
			return err
		}
	}
	return nil
}

func (b *XTestBackend) Close() error {
	b.conn.Close()
	return nil
}
