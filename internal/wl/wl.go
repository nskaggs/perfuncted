// Package wl is a minimal pure-Go Wayland client.
package wl

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

var le = binary.LittleEndian

// ── Proxy interface and BaseProxy ─────────────────────────────────────────────

// Ctx represents a Wayland connection context used by proxies. It is an interface
// so tests can provide mocks implementing the required methods.
type Ctx interface {
	Register(p Proxy)
	SetProxy(id uint32, p Proxy)
	Unregister(p Proxy)
	WriteMsgContext(context.Context, []byte, []byte) error
	DispatchContext(context.Context) error
	WithOperationContext(context.Context, func() error) error
	Close() error
}

// WithOperation serializes a multi-message protocol operation on a context.
// The non-context form is retained for setup and other operations whose
// lifetime is owned by the caller.
func WithOperation(ctx Ctx, fn func() error) error {
	return ctx.WithOperationContext(context.Background(), fn)
}

// WithOperationContext serializes a complete logical operation and honors
// cancellation while waiting for the shared transport.
func WithOperationContext(ctx Ctx, cancel context.Context, fn func() error) error {
	return ctx.WithOperationContext(cancel, fn)
}

// WriteMsg sends a message using a background context. Context-bearing
// operations should call WriteMsgContext directly.
func WriteMsg(ctx Ctx, data, oob []byte) error {
	return ctx.WriteMsgContext(context.Background(), data, oob)
}

// DispatchContext reads one message and honors cancellation.
func DispatchContext(cancel context.Context, ctx Ctx) error {
	return ctx.DispatchContext(cancel)
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
	buf     []byte

	objectsMu         sync.RWMutex
	writeGateOnce     sync.Once
	writeGate         chan struct{}
	dispatchGateOnce  sync.Once
	dispatchGate      chan struct{}
	roundTripGateOnce sync.Once
	roundTripGate     chan struct{}
	operationGateOnce sync.Once
	operationGate     chan struct{}
}

// Connect opens a Wayland connection to addr (must be an absolute socket path).
func Connect(addr string) (*Context, error) {
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: addr, Net: "unix"})
	if err != nil {
		return nil, err
	}
	return &Context{
		conn:    conn,
		objects: make(map[uint32]Proxy),
		nextID:  1,
		buf:     make([]byte, 4096),
	}, nil
}

// Register assigns the next client-side object ID to p and tracks it.
func (ctx *Context) Register(p Proxy) {
	ctx.objectsMu.Lock()
	defer ctx.objectsMu.Unlock()
	if ctx.objects == nil {
		ctx.objects = make(map[uint32]Proxy)
	}
	ctx.nextID++
	p.SetID(ctx.nextID)
	p.SetCtx(ctx)
	ctx.objects[ctx.nextID] = p
}

// SetProxy registers p with a specific compositor-assigned ID.
// Use this for server-created objects (new_id events from compositor side).
func (ctx *Context) SetProxy(id uint32, p Proxy) {
	ctx.objectsMu.Lock()
	defer ctx.objectsMu.Unlock()
	if ctx.objects == nil {
		ctx.objects = make(map[uint32]Proxy)
	}
	p.SetID(id)
	p.SetCtx(ctx)
	ctx.objects[id] = p
}

func (ctx *Context) unregister(id uint32, expected Proxy) {
	ctx.objectsMu.Lock()
	defer ctx.objectsMu.Unlock()
	if current, ok := ctx.objects[id]; ok && current == expected {
		delete(ctx.objects, id)
	}
}

// Unregister removes p from the client-side object registry when it is still
// registered under the same ID. It is safe to call after a protocol destroy.
func (ctx *Context) Unregister(p Proxy) {
	if ctx == nil || p == nil {
		return
	}
	ctx.unregister(p.ID(), p)
}

// Unregister removes p from the context's client-side object registry.
func Unregister(ctx Ctx, p Proxy) {
	if ctx == nil || p == nil {
		return
	}
	ctx.Unregister(p)
}

// WriteMsg sends a raw Wayland message using a background context.
func (ctx *Context) WriteMsg(data, oob []byte) error {
	return ctx.WriteMsgContext(context.Background(), data, oob)
}

// WriteMsgContext sends a raw Wayland message and interrupts a blocked Unix
// socket write when cancel is done. The write lock covers both the write and
// deadline restoration, so cancellation cannot leak a deadline into a later
// operation.
func (ctx *Context) WriteMsgContext(cancel context.Context, data, oob []byte) error {
	if cancel == nil {
		cancel = context.Background() //nolint:contextcheck // nil is intentionally normalized for this low-level API.
	}
	if err := cancel.Err(); err != nil {
		return err
	}
	if ctx == nil || ctx.conn == nil {
		return nil
	}

	select {
	case <-cancel.Done():
		return cancel.Err()
	case <-ctx.writeGateChannel():
	}
	defer func() { ctx.writeGateChannel() <- struct{}{} }()
	if err := cancel.Err(); err != nil {
		return err
	}

	finished := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-cancel.Done():
			// SetWriteDeadline is safe while WriteMsgUnix is blocked and does
			// not close the shared connection used by other Session handles.
			_ = ctx.conn.SetWriteDeadline(time.Now())
		case <-finished:
		}
	}()

	n, oobn, err := ctx.conn.WriteMsgUnix(data, oob, nil)
	close(finished)
	<-watcherDone
	resetErr := ctx.conn.SetWriteDeadline(time.Time{})
	cancelErr := cancel.Err()
	resetCause := func() error {
		if resetErr == nil {
			return nil
		}
		return fmt.Errorf("wl: reset write deadline: %w", resetErr)
	}
	if err != nil {
		if cancelErr != nil {
			if resetErr != nil {
				return errors.Join(cancelErr, err, resetCause())
			}
			return errors.Join(cancelErr, err)
		}
		if resetErr != nil {
			return errors.Join(err, resetCause())
		}
		return err
	}
	if resetErr != nil {
		if cancelErr != nil {
			return errors.Join(cancelErr, resetCause())
		}
		return resetCause()
	}
	if n != len(data) || oobn != len(oob) {
		shortErr := fmt.Errorf("wl: short write (%d/%d data, %d/%d oob)", n, len(data), oobn, len(oob))
		if cancelErr != nil {
			return errors.Join(cancelErr, shortErr)
		}
		return shortErr
	}
	// A complete successful write owns the request: cancellation observed
	// concurrently after WriteMsgUnix completed must not report a false failure.
	return nil
}

// Dispatch reads and dispatches exactly one Wayland message.
// Messages from unknown sender IDs are silently discarded (not an error).
func (ctx *Context) Dispatch() error {
	return ctx.DispatchContext(context.Background())
}

// dispatch reads and dispatches exactly one Wayland message.
func (ctx *Context) dispatch() error {
	// If the underlying connection is nil (tests may construct zero-value Contexts),
	// treat dispatch as a no-op rather than panicking.
	if ctx == nil || ctx.conn == nil {
		return nil
	}
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
		if size > len(ctx.buf) {
			ctx.buf = make([]byte, size)
		}
		data = ctx.buf[:size]
		if _, err := io.ReadFull(ctx.conn, data); err != nil {
			return fmt.Errorf("wl: %w", err)
		}
	}
	ctx.objectsMu.RLock()
	p, ok := ctx.objects[senderID]
	ctx.objectsMu.RUnlock()
	if ok {
		p.Dispatch(opcode, -1, data)
	}
	return nil
}

// DispatchContext reads and dispatches one Wayland message, interrupting the
// socket read when cancel is done. A read deadline preserves the connection for
// other users of a reference-counted session; callers decide whether a failed
// operation should discard it.
func (ctx *Context) DispatchContext(cancel context.Context) error {
	if cancel == nil {
		cancel = context.Background() //nolint:contextcheck // nil is intentionally normalized for this low-level API.
	}
	if err := cancel.Err(); err != nil {
		return err
	}
	if ctx == nil || ctx.conn == nil {
		return nil
	}
	gate := ctx.dispatchGateChannel()
	select {
	case <-cancel.Done():
		return cancel.Err()
	case <-gate:
	}
	defer func() { gate <- struct{}{} }()
	if err := cancel.Err(); err != nil {
		return err
	}

	finished := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-cancel.Done():
			// A deadline interrupts only this read; do not close the shared
			// reference-counted transport if setting it fails.
			_ = ctx.conn.SetReadDeadline(time.Now())
		case <-finished:
		}
	}()

	err := ctx.dispatch()
	close(finished)
	<-watcherDone
	resetErr := ctx.conn.SetReadDeadline(time.Time{})
	if cancelErr := cancel.Err(); cancelErr != nil {
		if resetErr != nil {
			return errors.Join(cancelErr, fmt.Errorf("wl: reset read deadline: %w", resetErr))
		}
		return cancelErr
	}
	if err != nil && resetErr != nil {
		return errors.Join(err, fmt.Errorf("wl: reset read deadline: %w", resetErr))
	}
	if resetErr != nil {
		return fmt.Errorf("wl: reset read deadline: %w", resetErr)
	}
	return err
}

// Close closes the Wayland socket connection.
// If the receiver or its underlying connection is nil (tests may construct
// partial contexts), treat it as a no-op rather than panicking.
func (ctx *Context) Close() error {
	if ctx == nil || ctx.conn == nil {
		return nil
	}
	gate := ctx.writeGateChannel()
	<-gate
	defer func() { gate <- struct{}{} }()
	return ctx.conn.Close()
}

// WithOperation serializes a multi-message protocol operation using a
// background context.
func (ctx *Context) WithOperation(fn func() error) error {
	return ctx.WithOperationContext(context.Background(), fn)
}

func (ctx *Context) dispatchGateChannel() chan struct{} {
	ctx.dispatchGateOnce.Do(func() {
		ctx.dispatchGate = make(chan struct{}, 1)
		ctx.dispatchGate <- struct{}{}
	})
	return ctx.dispatchGate
}

func (ctx *Context) roundTripGateChannel() chan struct{} {
	ctx.roundTripGateOnce.Do(func() {
		ctx.roundTripGate = make(chan struct{}, 1)
		ctx.roundTripGate <- struct{}{}
	})
	return ctx.roundTripGate
}

func (ctx *Context) operationGateChannel() chan struct{} {
	ctx.operationGateOnce.Do(func() {
		ctx.operationGate = make(chan struct{}, 1)
		ctx.operationGate <- struct{}{}
	})
	return ctx.operationGate
}

func (ctx *Context) writeGateChannel() chan struct{} {
	ctx.writeGateOnce.Do(func() {
		ctx.writeGate = make(chan struct{}, 1)
		ctx.writeGate <- struct{}{}
	})
	return ctx.writeGate
}

// WithOperationContext serializes a complete logical operation and allows a
// caller to abandon admission while another owner is using the shared
// connection.
func (ctx *Context) WithOperationContext(cancel context.Context, fn func() error) error {
	if cancel == nil {
		cancel = context.Background() //nolint:contextcheck // nil is intentionally normalized for this low-level API.
	}
	if err := cancel.Err(); err != nil {
		return err
	}
	gate := ctx.operationGateChannel()
	select {
	case <-cancel.Done():
		return cancel.Err()
	case <-gate:
	}
	defer func() { gate <- struct{}{} }()
	if err := cancel.Err(); err != nil {
		return err
	}
	return fn()
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

// ResolveSocketPath resolves a Wayland socket path from WAYLAND_DISPLAY and
// XDG_RUNTIME_DIR. Relative socket names require XDG_RUNTIME_DIR; otherwise
// the socket cannot be resolved and the empty string is returned.
func ResolveSocketPath(waylandDisplay, xdgRuntimeDir string) string {
	if waylandDisplay == "" {
		return ""
	}
	if filepath.IsAbs(waylandDisplay) {
		return waylandDisplay
	}
	if xdgRuntimeDir == "" {
		return ""
	}
	return filepath.Join(xdgRuntimeDir, waylandDisplay)
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
	return d.GetRegistryContext(context.Background())
}

// GetRegistryContext sends wl_display.get_registry with cancellation-aware I/O.
func (d *Display) GetRegistryContext(cancel context.Context) (*Registry, error) {
	reg := &Registry{}
	d.ctx.Register(reg)
	var buf [12]byte
	PutUint32(buf[0:], 1)        // wl_display is always ID 1
	PutUint32(buf[4:], 12<<16|1) // size=12, opcode=1 (get_registry)
	PutUint32(buf[8:], reg.ID())
	return reg, d.ctx.WriteMsgContext(cancel, buf[:], nil)
}

// Sync sends wl_display.sync and returns a Callback object.
func (d *Display) Sync() (*Callback, error) {
	return d.SyncContext(context.Background())
}

// SyncContext sends wl_display.sync with cancellation-aware I/O.
func (d *Display) SyncContext(cancel context.Context) (*Callback, error) {
	cb := &Callback{}
	d.ctx.Register(cb)
	var buf [12]byte
	PutUint32(buf[0:], 1)      // wl_display is always ID 1
	PutUint32(buf[4:], 12<<16) // size=12, opcode=0 (sync)
	PutUint32(buf[8:], cb.ID())
	return cb, d.ctx.WriteMsgContext(cancel, buf[:], nil)
}

// RoundTrip performs a synchronous wl_display.sync, pumping events until done.
func (d *Display) RoundTrip() error {
	if d == nil || d.ctx == nil {
		return nil
	}
	return d.ctx.RoundTrip()
}

// RoundTripContext performs a synchronous wl_display.sync, pumping events
// until done or cancel is canceled.
func (d *Display) RoundTripContext(cancel context.Context) error {
	if d == nil || d.ctx == nil {
		return nil
	}
	return d.ctx.RoundTripContext(cancel)
}

// RoundTrip performs a synchronous wl_display.sync using this context. The
// round-trip lock prevents concurrent callers from consuming each other's sync
// callbacks on a shared connection.
func (ctx *Context) RoundTrip() error {
	return ctx.RoundTripContext(context.Background()) //nolint:contextcheck // RoundTrip is the non-cancelable convenience API.
}

// RoundTripContext performs a synchronous wl_display.sync, pumping events
// until done or cancel is canceled. The round-trip gate prevents concurrent
// callers from consuming each other's sync callbacks on a shared connection.
func (ctx *Context) RoundTripContext(cancel context.Context) error { //nolint:contextcheck // cancel is the first method argument after the receiver.
	if cancel == nil {
		cancel = context.Background() //nolint:contextcheck // nil is intentionally normalized for this low-level API.
	}
	if err := cancel.Err(); err != nil {
		return err
	}
	if ctx == nil || ctx.conn == nil {
		return nil
	}
	gate := ctx.roundTripGateChannel()
	select {
	case <-cancel.Done():
		return cancel.Err()
	case <-gate:
	}
	defer func() { gate <- struct{}{} }()
	if err := cancel.Err(); err != nil {
		return err
	}

	cb := &Callback{}
	ctx.Register(cb)
	defer ctx.unregister(cb.ID(), cb)
	var buf [12]byte
	PutUint32(buf[0:], 1)
	PutUint32(buf[4:], 12<<16)
	PutUint32(buf[8:], cb.ID())
	if err := ctx.WriteMsgContext(cancel, buf[:], nil); err != nil {
		return err
	}
	done := make(chan struct{}, 1)
	cb.SetDoneHandler(func() { done <- struct{}{} })
	for {
		if err := ctx.DispatchContext(cancel); err != nil {
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
	return r.BindContext(context.Background(), name, iface, ver, newID)
}

// BindContext sends wl_registry.bind with cancellation-aware I/O.
func (r *Registry) BindContext(cancel context.Context, name uint32, iface string, ver, newID uint32) error {
	var buf []byte
	buf = put32(buf, r.ID())
	buf = put32(buf, 0) // placeholder: filled in below with size|opcode
	buf = put32(buf, name)
	buf = putStr(buf, iface)
	buf = put32(buf, ver)
	buf = put32(buf, newID)
	PutUint32(buf[4:], uint32(len(buf))<<16) // opcode 0 = bind
	return r.ctx.WriteMsgContext(cancel, buf, nil)
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
	return s.CreatePoolContext(context.Background(), fd, size)
}

// CreatePoolContext sends wl_shm.create_pool with cancellation-aware I/O.
func (s *Shm) CreatePoolContext(cancel context.Context, fd int, size int32) (*ShmPool, error) {
	pool := &ShmPool{}
	s.ctx.Register(pool)
	var buf [16]byte
	PutUint32(buf[0:], s.ID())
	PutUint32(buf[4:], 16<<16) // size=16, opcode=0 (create_pool)
	PutUint32(buf[8:], pool.ID())
	PutUint32(buf[12:], uint32(size))
	if err := s.ctx.WriteMsgContext(cancel, buf[:], syscall.UnixRights(fd)); err != nil {
		Unregister(s.ctx, pool)
		return nil, err
	}
	return pool, nil
}

// ShmPool wraps wl_shm_pool.
type ShmPool struct{ BaseProxy }

// Dispatch implements Proxy.
func (p *ShmPool) Dispatch(_ uint32, _ int, _ []byte) {}

// CreateBuffer sends wl_shm_pool.create_buffer and returns the buffer.
func (p *ShmPool) CreateBuffer(offset, width, height, stride int32, format uint32) (*Buffer, error) {
	return p.CreateBufferContext(context.Background(), offset, width, height, stride, format)
}

// CreateBufferContext sends wl_shm_pool.create_buffer with cancellation-aware I/O.
func (p *ShmPool) CreateBufferContext(cancel context.Context, offset, width, height, stride int32, format uint32) (*Buffer, error) {
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
	if err := p.ctx.WriteMsgContext(cancel, buf[:], nil); err != nil {
		Unregister(p.ctx, b)
		return nil, err
	}
	return b, nil
}

// Destroy sends wl_shm_pool.destroy.
func (p *ShmPool) Destroy() error {
	return p.DestroyContext(context.Background())
}

// DestroyContext sends wl_shm_pool.destroy with cancellation-aware I/O.
func (p *ShmPool) DestroyContext(cancel context.Context) error {
	var buf [8]byte
	PutUint32(buf[0:], p.ID())
	PutUint32(buf[4:], 8<<16|1) // size=8, opcode=1 (destroy)
	if err := p.ctx.WriteMsgContext(cancel, buf[:], nil); err != nil {
		return err
	}
	Unregister(p.ctx, p)
	return nil
}

// Buffer wraps wl_buffer.
type Buffer struct{ BaseProxy }

// Dispatch implements Proxy.
func (b *Buffer) Dispatch(_ uint32, _ int, _ []byte) {}

// Destroy sends wl_buffer.destroy.
func (b *Buffer) Destroy() error {
	return b.DestroyContext(context.Background())
}

// DestroyContext sends wl_buffer.destroy with cancellation-aware I/O.
func (b *Buffer) DestroyContext(cancel context.Context) error {
	var buf [8]byte
	PutUint32(buf[0:], b.ID())
	PutUint32(buf[4:], 8<<16) // size=8, opcode=0 (destroy)
	if err := b.ctx.WriteMsgContext(cancel, buf[:], nil); err != nil {
		return err
	}
	Unregister(b.ctx, b)
	return nil
}

// SocketReachable checks whether sock is an existing Wayland socket.
func SocketReachable(sock string) bool {
	info, err := os.Stat(sock)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// ListGlobals connects to sock, enumerates all advertised globals, and returns
// a set of interface names. Returns nil if the socket is unreachable.
func ListGlobals(sock string) map[string]bool {
	if sock == "" {
		return nil
	}
	s, err := NewSession(sock)
	if err != nil {
		return nil
	}
	defer s.Close()
	globals := make(map[string]bool)
	for iface := range s.Globals {
		globals[iface] = true
	}
	return globals
}
