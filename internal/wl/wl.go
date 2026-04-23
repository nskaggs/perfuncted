// Package wl is a minimal pure-Go Wayland client.
package wl

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

var le = binary.LittleEndian

// ── Proxy interface and BaseProxy ─────────────────────────────────────────────

// Ctx represents a Wayland connection context used by proxies. It is an interface
// so tests can provide mocks implementing the required methods.
type Ctx interface {
	Register(p Proxy)
	SetProxy(id uint32, p Proxy)
	WriteMsg(data, oob []byte) error
	Dispatch() error
	Close() error
}

// Proxy is implemented by all Wayland protocol objects.
type Proxy interface {
	ID() uint32
	SetID(uint32)
	SetCtx(Ctx)
	Dispatch(opcode uint32, fd int, data []byte)
}

// BaseProxy provides ID/context bookkeeping. Embed it in protocol object structs.
type BaseProxy struct {
	id  uint32
	ctx Ctx
}

func (b *BaseProxy) ID() uint32      { return b.id }
func (b *BaseProxy) SetID(id uint32) { b.id = id }
func (b *BaseProxy) SetCtx(c Ctx)    { b.ctx = c }
func (b *BaseProxy) Ctx() Ctx        { return b.ctx }

// RawProxy is a Proxy backed by a user-supplied dispatch function.
// Use it to implement custom Wayland protocols without code generation.
type RawProxy struct {
	BaseProxy
	OnEvent func(opcode uint32, fd int, data []byte)
}

func (p *RawProxy) Dispatch(opcode uint32, fd int, data []byte) {
	if p.OnEvent != nil {
		p.OnEvent(opcode, fd, data)
	}
}

// ── Context ───────────────────────────────────────────────────────────────────

// Context is a Wayland client connection and object registry.
type Context struct {
	conn    *net.UnixConn
	objects map[uint32]Proxy
	nextID  uint32
}

// Connect opens a Wayland connection to addr (must be an absolute socket path).
func Connect(addr string) (*Context, error) {
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: addr, Net: "unix"})
	if err != nil {
		return nil, err
	}
	return &Context{conn: conn, objects: make(map[uint32]Proxy), nextID: 1}, nil
}

// Register assigns the next client-side object ID to p and tracks it.
func (ctx *Context) Register(p Proxy) {
	ctx.nextID++
	p.SetID(ctx.nextID)
	p.SetCtx(ctx)
	ctx.objects[ctx.nextID] = p
}

// SetProxy registers p with a specific compositor-assigned ID.
// Use this for server-created objects (new_id events from compositor side).
func (ctx *Context) SetProxy(id uint32, p Proxy) {
	p.SetID(id)
	p.SetCtx(ctx)
	ctx.objects[id] = p
}

// WriteMsg sends a raw Wayland message with optional ancillary (OOB) data.
func (ctx *Context) WriteMsg(data, oob []byte) error {
	n, oobn, err := ctx.conn.WriteMsgUnix(data, oob, nil)
	if err != nil {
		return err
	}
	if n != len(data) || oobn != len(oob) {
		return fmt.Errorf("wl: short write (%d/%d data, %d/%d oob)", n, len(data), oobn, len(oob))
	}
	return nil
}

// Dispatch reads and dispatches exactly one Wayland message.
// Messages from unknown sender IDs are silently discarded (not an error).
func (ctx *Context) Dispatch() error {
	var hdr [8]byte
	if _, err := io.ReadFull(ctx.conn, hdr[:]); err != nil {
		return fmt.Errorf("wl: %w", err)
	}
	senderID := Uint32(hdr[0:4])
	sizeOpcode := Uint32(hdr[4:8])
	size := int(sizeOpcode>>16) - 8
	opcode := sizeOpcode & 0xffff
	if size < 0 {
		return fmt.Errorf("wl: invalid message size %d", int(sizeOpcode>>16))
	}
	var data []byte
	if size > 0 {
		data = make([]byte, size)
		if _, err := io.ReadFull(ctx.conn, data); err != nil {
			return fmt.Errorf("wl: %w", err)
		}
	}
	if p, ok := ctx.objects[senderID]; ok {
		p.Dispatch(opcode, -1, data)
	}
	return nil
}

// Close closes the Wayland socket connection.
func (ctx *Context) Close() error { return ctx.conn.Close() }

// SafeClose closes ctx if non-nil and its underlying connection is non-nil.
// Use when callers may construct partial contexts in tests.
func SafeClose(ctx *Context) error {
	if ctx == nil || ctx.conn == nil {
		return nil
	}
	return ctx.conn.Close()
}

// ── Wire encoding helpers ─────────────────────────────────────────────────────

// PutUint32 encodes v into b[0:4] in little-endian byte order.
func PutUint32(b []byte, v uint32) { le.PutUint32(b, v) }

// Uint32 decodes a little-endian uint32 from b[0:4].
func Uint32(b []byte) uint32 { return le.Uint32(b) }

func put32(buf []byte, v uint32) []byte { return le.AppendUint32(buf, v) }

// putStr appends a Wayland wire-encoded string to buf.
// Format: length uint32 (strlen+1, includes null), string bytes, null+padding to 4B.
// The length field is strlen+1 per spec — NOT PaddedLen(strlen+1).
func putStr(buf []byte, s string) []byte {
	n := uint32(len(s) + 1) // includes null terminator
	buf = put32(buf, n)
	buf = append(buf, s...)
	padded := (n + 3) &^ 3        // round up to 4-byte boundary
	zeros := int(padded) - len(s) // null terminator + any extra padding bytes
	for i := 0; i < zeros; i++ {
		buf = append(buf, 0)
	}
	return buf
}

// SocketPath returns the absolute path to the Wayland socket from the environment.
var socketPathOverride string

// SetSocketPathOverride sets an explicit Wayland socket path to use instead
// of reading WAYLAND_DISPLAY/XDG_RUNTIME_DIR from the process environment.
// Pass an empty string to clear the override.
func SetSocketPathOverride(sock string) { socketPathOverride = sock }

func SocketPath() string {
	if socketPathOverride != "" {
		return socketPathOverride
	}
	sock := os.Getenv("WAYLAND_DISPLAY")
	if sock == "" {
		return ""
	}
	if filepath.IsAbs(sock) {
		return sock
	}
	xrd := os.Getenv("XDG_RUNTIME_DIR")
	if xrd == "" {
		return sock
	}
	return filepath.Join(xrd, sock)
}

// ── Display ───────────────────────────────────────────────────────────────────

// Display represents wl_display, which is always object ID 1.
type Display struct{ ctx *Context }

// NewDisplay wraps an existing Context as a Display (wl_display = ID 1).
func NewDisplay(ctx *Context) *Display { return &Display{ctx: ctx} }

// Context returns the underlying connection context.
func (d *Display) Context() Ctx { return d.ctx }

// GetRegistry sends wl_display.get_registry and returns the Registry object.
func (d *Display) GetRegistry() (*Registry, error) {
	reg := &Registry{}
	d.ctx.Register(reg)
	var buf [12]byte
	PutUint32(buf[0:], 1)        // wl_display is always ID 1
	PutUint32(buf[4:], 12<<16|1) // size=12, opcode=1 (get_registry)
	PutUint32(buf[8:], reg.ID())
	return reg, d.ctx.WriteMsg(buf[:], nil)
}

// Sync sends wl_display.sync and returns a Callback object.
func (d *Display) Sync() (*Callback, error) {
	cb := &Callback{}
	d.ctx.Register(cb)
	var buf [12]byte
	PutUint32(buf[0:], 1)      // wl_display is always ID 1
	PutUint32(buf[4:], 12<<16) // size=12, opcode=0 (sync)
	PutUint32(buf[8:], cb.ID())
	return cb, d.ctx.WriteMsg(buf[:], nil)
}

// RoundTrip performs a synchronous wl_display.sync, pumping events until done.
func (d *Display) RoundTrip() error {
	cb, err := d.Sync()
	if err != nil {
		return err
	}
	done := make(chan struct{}, 1)
	cb.doneHandler = func() { done <- struct{}{} }
	for {
		if err := d.ctx.Dispatch(); err != nil {
			return err
		}
		select {
		case <-done:
			return nil
		default:
		}
	}
}

// ── Registry ──────────────────────────────────────────────────────────────────

// GlobalEvent carries the data of a wl_registry.global event.
type GlobalEvent struct {
	Name      uint32
	Interface string
	Version   uint32
}

// Registry wraps wl_registry.
type Registry struct {
	BaseProxy
	globalHandler func(GlobalEvent)
}

// SetGlobalHandler registers f to receive wl_registry.global events.
func (r *Registry) SetGlobalHandler(f func(GlobalEvent)) { r.globalHandler = f }

// Dispatch implements Proxy for incoming wl_registry events.
func (r *Registry) Dispatch(opcode uint32, _ int, data []byte) {
	if opcode != 0 || r.globalHandler == nil || len(data) < 8 {
		return
	}
	ev := GlobalEvent{Name: Uint32(data[0:4])}
	slen := int(Uint32(data[4:8]))
	if slen > 0 && 8+slen <= len(data) {
		ev.Interface = string(data[8 : 8+slen-1]) // strip null terminator
	}
	padded := (slen + 3) &^ 3
	if off := 8 + padded; off+4 <= len(data) {
		ev.Version = Uint32(data[off : off+4])
	}
	r.globalHandler(ev)
}

// Bind sends wl_registry.bind with correct Wayland string encoding.
// newID must be the ID of a Proxy already registered with the Context.
func (r *Registry) Bind(name uint32, iface string, ver, newID uint32) error {
	var buf []byte
	buf = put32(buf, r.ID())
	buf = put32(buf, 0) // placeholder: filled in below with size|opcode
	buf = put32(buf, name)
	buf = putStr(buf, iface)
	buf = put32(buf, ver)
	buf = put32(buf, newID)
	PutUint32(buf[4:], uint32(len(buf))<<16) // opcode 0 = bind
	return r.ctx.WriteMsg(buf, nil)
}

// ── Callback ──────────────────────────────────────────────────────────────────

// Callback wraps wl_callback.
type Callback struct {
	BaseProxy
	doneHandler func()
}

// SetDoneHandler registers f to be called on wl_callback.done.
func (c *Callback) SetDoneHandler(f func()) { c.doneHandler = f }

// Dispatch implements Proxy.
func (c *Callback) Dispatch(opcode uint32, _ int, _ []byte) {
	if opcode == 0 && c.doneHandler != nil {
		c.doneHandler()
	}
}

// ── Shm ───────────────────────────────────────────────────────────────────────

// Shm wraps wl_shm.
type Shm struct{ BaseProxy }

// Dispatch implements Proxy (wl_shm.format events are ignored).
func (s *Shm) Dispatch(_ uint32, _ int, _ []byte) {}

// CreatePool sends wl_shm.create_pool(new_id, fd, size) and returns the pool.
// fd is passed as ancillary data (OOB), not in the message body.
func (s *Shm) CreatePool(fd int, size int32) (*ShmPool, error) {
	pool := &ShmPool{}
	s.ctx.Register(pool)
	var buf [16]byte
	PutUint32(buf[0:], s.ID())
	PutUint32(buf[4:], 16<<16) // size=16, opcode=0 (create_pool)
	PutUint32(buf[8:], pool.ID())
	PutUint32(buf[12:], uint32(size))
	return pool, s.ctx.WriteMsg(buf[:], syscall.UnixRights(fd))
}

// ShmPool wraps wl_shm_pool.
type ShmPool struct{ BaseProxy }

// Dispatch implements Proxy.
func (p *ShmPool) Dispatch(_ uint32, _ int, _ []byte) {}

// CreateBuffer sends wl_shm_pool.create_buffer and returns the buffer.
func (p *ShmPool) CreateBuffer(offset, width, height, stride int32, format uint32) (*Buffer, error) {
	b := &Buffer{}
	p.ctx.Register(b)
	var buf [32]byte
	PutUint32(buf[0:], p.ID())
	PutUint32(buf[4:], 32<<16) // size=32, opcode=0 (create_buffer)
	PutUint32(buf[8:], b.ID())
	PutUint32(buf[12:], uint32(offset))
	PutUint32(buf[16:], uint32(width))
	PutUint32(buf[20:], uint32(height))
	PutUint32(buf[24:], uint32(stride))
	PutUint32(buf[28:], format)
	return b, p.ctx.WriteMsg(buf[:], nil)
}

// Destroy sends wl_shm_pool.destroy.
func (p *ShmPool) Destroy() error {
	var buf [8]byte
	PutUint32(buf[0:], p.ID())
	PutUint32(buf[4:], 8<<16|1) // size=8, opcode=1 (destroy)
	return p.ctx.WriteMsg(buf[:], nil)
}

// Buffer wraps wl_buffer.
type Buffer struct{ BaseProxy }

// Dispatch implements Proxy.
func (b *Buffer) Dispatch(_ uint32, _ int, _ []byte) {}

// Destroy sends wl_buffer.destroy.
func (b *Buffer) Destroy() error {
	var buf [8]byte
	PutUint32(buf[0:], b.ID())
	PutUint32(buf[4:], 8<<16) // size=8, opcode=0 (destroy)
	return b.ctx.WriteMsg(buf[:], nil)
}

// Output wraps wl_output (only its ID is needed for screen capture calls).
type Output struct{ BaseProxy }

// Dispatch implements Proxy (wl_output events are ignored).
func (o *Output) Dispatch(_ uint32, _ int, _ []byte) {}

// ListGlobals connects to sock, enumerates all advertised globals, and returns
// a set of interface names. Returns nil if the socket is unreachable.
func ListGlobals(sock string) map[string]bool {
	if sock == "" {
		return nil
	}
	ctx, err := Connect(sock)
	if err != nil {
		return nil
	}
	defer ctx.Close()
	display := NewDisplay(ctx)
	registry, err := display.GetRegistry()
	if err != nil {
		return nil
	}
	globals := make(map[string]bool)
	registry.SetGlobalHandler(func(ev GlobalEvent) { globals[ev.Interface] = true })
	if err := display.RoundTrip(); err != nil {
		return nil
	}
	return globals
}
