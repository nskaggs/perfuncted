//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/nskaggs/perfuncted/internal/wl"
)

var _ Inputter = (*WlInputMethodBackend)(nil)

// WlInputMethodBackend implements Unicode text injection via the
// zwp_input_method_manager_v2 protocol (input-method-unstable-v2). When
// available this backend sends commit_string + commit to the compositor so
// focused clients receive UTF-8 text without relying on keyboard layouts.
// For other operations (mouse, key taps) it delegates to an underlying
// Inputter (prefer wl-virtual when available).
type WlInputMethodBackend struct {
	session *wl.Session
	display *wl.Display
	ctx     *wl.Context
	mgr     *wl.RawProxy
	seat    *wl.RawProxy
	im      *wl.RawProxy
	serial  uint32
	other   Inputter
}

// NewWlInputMethodBackend tries to bind zwp_input_method_manager_v2 and a
// wl_seat, creates an input_method object, and returns a composite Inputter
// that uses the input-method path for Type() and delegates other operations
// to a wl-virtual backend when available.
func NewWlInputMethodBackend(sock string, maxX, maxY int32) (Inputter, error) {
	if sock == "" {
		return nil, fmt.Errorf("input: WAYLAND_DISPLAY not set")
	}
	// Use the centralized Session helper to connect and enumerate globals.
	sess, err := wl.NewSession(sock)
	if err != nil {
		return nil, fmt.Errorf("input/wl-im: %w", err)
	}
	ctx := sess.Ctx
	display := sess.Display
	registry := sess.Registry

	var mgrID, seatID uint32
	if ev, ok := sess.Globals["zwp_input_method_manager_v2"]; ok {
		mgrID = ev.Name
	}
	if ev, ok := sess.Globals["wl_seat"]; ok {
		seatID = ev.Name
	}
	if mgrID == 0 {
		_ = sess.Close()
		return nil, fmt.Errorf("input/wl-im: zwp_input_method_manager_v2 not advertised")
	}
	if seatID == 0 {
		_ = sess.Close()
		return nil, fmt.Errorf("input/wl-im: no wl_seat advertised")
	}

	// Bind manager
	mgrProxy := &wl.RawProxy{}
	ctx.Register(mgrProxy)
	if err := registry.Bind(mgrID, "zwp_input_method_manager_v2", 1, mgrProxy.ID()); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("input/wl-im: bind manager: %w", err)
	}

	// Bind seat (client-side proxy)
	seatProxy := &wl.RawProxy{}
	ctx.Register(seatProxy)
	if err := registry.Bind(seatID, "wl_seat", 1, seatProxy.ID()); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("input/wl-im: bind seat: %w", err)
	}

	// Create input_method object: manager.get_input_method(seat, new_id)
	im := &wl.RawProxy{}
	ctx.Register(im)
	var buf [16]byte
	wl.PutUint32(buf[0:], mgrProxy.ID())
	wl.PutUint32(buf[4:], 16<<16) // size=16, opcode=0 (get_input_method)
	wl.PutUint32(buf[8:], seatProxy.ID())
	wl.PutUint32(buf[12:], im.ID())
	if err := ctx.WriteMsg(buf[:], nil); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("input/wl-im: get_input_method: %w", err)
	}

	backend := &WlInputMethodBackend{session: sess, display: display, ctx: ctx, mgr: mgrProxy, seat: seatProxy, im: im}

	// Listen for done events so we can manage the serial used by commit().
	im.OnEvent = func(opcode uint32, _ int, _ []byte) {
		// According to the protocol: done event opcode = 5
		if opcode == 5 {
			backend.serial++
		}
	}

	// Round-trip now to ensure the input_method object is created and listeners attached.
	if err := display.RoundTrip(); err != nil {
		_ = backend.session.Close()
		return nil, fmt.Errorf("input/wl-im: round-trip after create: %w", err)
	}

	// Prefer wl-virtual for non-Type operations when available.
	if b, err := NewWlVirtualBackend(sock); err == nil {
		backend.other = b
	} else if _, statErr := os.Stat("/dev/uinput"); statErr == nil {
		if b, err := NewUinputBackend(maxX, maxY); err == nil {
			backend.other = b
		}
	}

	return backend, nil
}

// Type delegates to the wl-virtual keyboard backend via TypeContext.
func (b *WlInputMethodBackend) Type(ctx context.Context, s string) error {
	return b.TypeContext(ctx, s)
}

// TypeContext delegates to the wl-virtual keyboard backend (other), which
// handles both literal text and {key combos} via a custom XKB keymap.
// The input-method protocol (commit_string) requires compositor-side activation
// which is unreliable in headless CI environments.
func (b *WlInputMethodBackend) TypeContext(ctx context.Context, s string) error {
	if b.other != nil {
		return b.other.Type(ctx, s)
	}
	return fmt.Errorf("input/wl-im: no subordinate backend for typing")
}

// Delegate other methods to the underlying backend when present.
func (b *WlInputMethodBackend) KeyDown(ctx context.Context, key string) error {
	if b.other == nil {
		return fmt.Errorf("input/wl-im: KeyDown unsupported (no subordinate backend)")
	}
	return b.other.KeyDown(ctx, key)
}
func (b *WlInputMethodBackend) KeyUp(ctx context.Context, key string) error {
	if b.other == nil {
		return fmt.Errorf("input/wl-im: KeyUp unsupported (no subordinate backend)")
	}
	return b.other.KeyUp(ctx, key)
}
func (b *WlInputMethodBackend) MouseMove(ctx context.Context, x, y int) error {
	if b.other == nil {
		return fmt.Errorf("input/wl-im: MouseMove unsupported (no subordinate backend)")
	}
	return b.other.MouseMove(ctx, x, y)
}
func (b *WlInputMethodBackend) MouseClick(ctx context.Context, x, y, button int) error {
	if b.other == nil {
		return fmt.Errorf("input/wl-im: MouseClick unsupported (no subordinate backend)")
	}
	return b.other.MouseClick(ctx, x, y, button)
}
func (b *WlInputMethodBackend) MouseDown(ctx context.Context, button int) error {
	if b.other == nil {
		return fmt.Errorf("input/wl-im: MouseDown unsupported (no subordinate backend)")
	}
	return b.other.MouseDown(ctx, button)
}
func (b *WlInputMethodBackend) MouseUp(ctx context.Context, button int) error {
	if b.other == nil {
		return fmt.Errorf("input/wl-im: MouseUp unsupported (no subordinate backend)")
	}
	return b.other.MouseUp(ctx, button)
}
func (b *WlInputMethodBackend) ScrollUp(ctx context.Context, clicks int) error {
	if b.other == nil {
		return fmt.Errorf("input/wl-im: ScrollUp unsupported (no subordinate backend)")
	}
	return b.other.ScrollUp(ctx, clicks)
}
func (b *WlInputMethodBackend) ScrollDown(ctx context.Context, clicks int) error {
	if b.other == nil {
		return fmt.Errorf("input/wl-im: ScrollDown unsupported (no subordinate backend)")
	}
	return b.other.ScrollDown(ctx, clicks)
}
func (b *WlInputMethodBackend) ScrollLeft(ctx context.Context, clicks int) error {
	if b.other == nil {
		return fmt.Errorf("input/wl-im: ScrollLeft unsupported (no subordinate backend)")
	}
	return b.other.ScrollLeft(ctx, clicks)
}
func (b *WlInputMethodBackend) ScrollRight(ctx context.Context, clicks int) error {
	if b.other == nil {
		return fmt.Errorf("input/wl-im: ScrollRight unsupported (no subordinate backend)")
	}
	return b.other.ScrollRight(ctx, clicks)
}

func (b *WlInputMethodBackend) Close() error {
	var errs []error
	if b.other != nil {
		if err := b.other.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if b.session != nil {
		if err := b.session.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}
