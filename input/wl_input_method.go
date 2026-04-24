//go:build linux
// +build linux

package input

import (
	"context"
	"fmt"
	"os"

	"github.com/nskaggs/perfuncted/internal/wl"
)

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

// encodeWlString encodes a Wayland string (length+bytes+null+padding).
func encodeWlString(s string) []byte {
	n := uint32(len(s) + 1)
	b := make([]byte, 4)
	wl.PutUint32(b, n)
	out := make([]byte, 0, 4+len(s)+4)
	out = append(out, b...)
	out = append(out, s...)
	padded := (n + 3) &^ 3
	zeros := int(padded) - len(s)
	for i := 0; i < zeros; i++ {
		out = append(out, 0)
	}
	return out
}

// sendIMRequest writes a request to the input_method object with the given
// opcode and payload (payload should already be Wayland-encoded for strings).
func (b *WlInputMethodBackend) sendIMRequest(opcode uint32, payload []byte) error {
	size := 8 + len(payload)
	buf := make([]byte, size)
	wl.PutUint32(buf[0:], b.im.ID())
	wl.PutUint32(buf[4:], uint32(size)<<16|opcode)
	if len(payload) > 0 {
		copy(buf[8:], payload)
	}
	return b.ctx.WriteMsg(buf, nil)
}

// Type sends s using input-method commit_string + commit(serial). If that
// path fails, fall back to the delegated backend if available.
func (b *WlInputMethodBackend) Type(ctx context.Context, s string) error {
	return b.TypeContext(ctx, s)
}

// TypeContext is an alias for Type to match bundle patterns.
func (b *WlInputMethodBackend) TypeContext(ctx context.Context, s string) error {
	if b.im == nil {
		if b.other != nil {
			return b.other.Type(ctx, s)
		}
		return fmt.Errorf("input/wl-im: input method unavailable")
	}
	// Wayland message limit: keep segments <= 4000 bytes for safety.
	const maxChunk = 4000
	for start := 0; start < len(s); {
		end := start + maxChunk
		if end > len(s) {
			end = len(s)
		}
		chunk := s[start:end]
		// commit_string
		payload := encodeWlString(chunk)
		if err := b.sendIMRequest(0, payload); err != nil { // opcode 0 = commit_string
			// fall back
			if b.other != nil {
				return b.other.Type(ctx, s)
			}
			return fmt.Errorf("input/wl-im: commit_string: %w", err)
		}
		// commit(serial)
		var cbuf [12]byte
		wl.PutUint32(cbuf[0:], b.im.ID())
		wl.PutUint32(cbuf[4:], 12<<16|3) // size=12, opcode=3 (commit)
		wl.PutUint32(cbuf[8:], b.serial)
		if err := b.ctx.WriteMsg(cbuf[:], nil); err != nil {
			if b.other != nil {
				return b.other.Type(ctx, s)
			}
			return fmt.Errorf("input/wl-im: commit: %w", err)
		}
		// Wait for compositor to emit done (increments b.serial)
		if err := b.display.RoundTrip(); err != nil {
			// If RoundTrip fails, fall back
			if b.other != nil {
				return b.other.Type(ctx, s)
			}
			return fmt.Errorf("input/wl-im: round-trip after commit: %w", err)
		}
		start = end
	}
	return nil
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
func (b *WlInputMethodBackend) KeyTap(ctx context.Context, key string) error {
	if b.other == nil {
		return fmt.Errorf("input/wl-im: KeyTap unsupported (no subordinate backend)")
	}
	return b.other.KeyTap(ctx, key)
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

func (b *WlInputMethodBackend) PressCombo(ctx context.Context, combo string) error {
	if b.other == nil {
		return fmt.Errorf("input/wl-im: PressCombo unsupported (no subordinate backend)")
	}
	return b.other.PressCombo(ctx, combo)
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
	return fmt.Errorf("input/wl-im: close errors: %v", errs)
}
