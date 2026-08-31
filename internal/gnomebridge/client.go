//go:build linux
// +build linux

package gnomebridge

import (
	"context"
	"errors"
	"fmt"
	"image"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

type objectCaller interface {
	CallWithContext(context.Context, string, dbus.Flags, ...any) *dbus.Call
}

// Client is a typed client for the explicit GNOME bridge interfaces. It does
// not expose an arbitrary method/property dispatcher.
type Client struct {
	conn *dbus.Conn
	obj  objectCaller
	// supportsFDs is set for test transports; real connections use the
	// capability advertised by the underlying D-Bus transport.
	supportsFDs bool

	mu           sync.Mutex
	closed       bool
	closeErr     error
	closeDone    chan struct{}
	protocol     uint32
	extensionVer string
	shellVer     string
	caps         []string
}

// NewClientForBus connects to addr and negotiates protocol version. The
// address is required so an explicit target cannot accidentally use the
// caller's unrelated process session bus.
func NewClientForBus(ctx context.Context, addr string) (*Client, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if addr == "" {
		return nil, fmt.Errorf("%w: D-Bus session address is unset", ErrUnavailable)
	}
	// Negotiation must not hang indefinitely when callers pass a
	// background context from OpenRuntime probes or session setup.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	conn, err := dbusutil.SessionBusAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("gnome bridge: session bus: %w", err)
	}
	if !dbusutil.HasService(conn, BusName) {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: %s is not on the session bus", ErrUnavailable, BusName)
	}
	client, err := newClient(ctx, conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client, nil
}

func newClient(ctx context.Context, conn *dbus.Conn) (*Client, error) {
	c := &Client{conn: conn, obj: conn.Object(BusName, dbus.ObjectPath(ObjectPath)), supportsFDs: conn.SupportsUnixFDs(), closeDone: make(chan struct{})}
	if err := negotiate(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func negotiate(ctx context.Context, c *Client) error {
	var protocol uint32
	if err := c.call(ctx, CoreInterface, "GetProtocolVersion", &protocol); err != nil {
		return fmt.Errorf("gnome bridge: negotiate protocol: %w", err)
	}
	if protocol != ProtocolVersion {
		return &ProtocolError{Expected: ProtocolVersion, Actual: protocol}
	}
	var extensionVersion, shellVersion string
	if err := c.call(ctx, CoreInterface, "GetExtensionVersion", &extensionVersion); err != nil {
		return fmt.Errorf("gnome bridge: get extension version: %w", err)
	}
	if err := c.call(ctx, CoreInterface, "GetShellVersion", &shellVersion); err != nil {
		return fmt.Errorf("gnome bridge: get Shell version: %w", err)
	}
	var caps []string
	if err := c.call(ctx, CoreInterface, "GetCapabilities", &caps); err != nil {
		return fmt.Errorf("gnome bridge: get capabilities: %w", err)
	}
	c.protocol = protocol
	c.extensionVer = extensionVersion
	c.shellVer = shellVersion
	c.caps = slices.Clone(caps)
	return nil
}

func newClientWithObject(ctx context.Context, object objectCaller) (*Client, error) {
	c := &Client{obj: object, supportsFDs: true, closeDone: make(chan struct{})}
	if err := negotiate(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ProtocolVersion returns the negotiated protocol version.
func (c *Client) ProtocolVersion() uint32 {
	if c == nil {
		return 0
	}
	return c.protocol
}

// ExtensionVersion returns the running extension version.
func (c *Client) ExtensionVersion() string {
	if c == nil {
		return ""
	}
	return c.extensionVer
}

// ShellVersion returns the running GNOME Shell version.
func (c *Client) ShellVersion() string {
	if c == nil {
		return ""
	}
	return c.shellVer
}

// Capabilities returns a defensive copy of capabilities advertised by the
// extension.
func (c *Client) Capabilities() []string {
	if c == nil {
		return nil
	}
	return slices.Clone(c.caps)
}

func (c *Client) HasCapability(capability string) bool {
	return c != nil && slices.Contains(c.caps, capability)
}

func (c *Client) call(ctx context.Context, iface, method string, out ...any) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed || c.obj == nil {
		c.mu.Unlock()
		return fmt.Errorf("%w: client is closed", ErrUnavailable)
	}
	object := c.obj
	c.mu.Unlock()
	call := object.CallWithContext(ctx, iface+"."+method, 0)
	if call.Err != nil {
		return translateDBusError(call.Err)
	}
	if len(out) == 0 {
		return nil
	}
	if err := call.Store(out...); err != nil {
		return fmt.Errorf("decode %s.%s reply: %w", iface, method, err)
	}
	return nil
}

func translateDBusError(err error) error {
	var dbusErr *dbus.Error
	if !errors.As(err, &dbusErr) {
		return err
	}
	switch dbusErr.Name {
	case "org.freedesktop.DBus.Error.Disconnected",
		"org.freedesktop.DBus.Error.NameHasNoOwner",
		"org.freedesktop.DBus.Error.NoReply",
		"org.freedesktop.DBus.Error.ServiceUnknown",
		"org.freedesktop.DBus.Error.UnknownMethod",
		"org.freedesktop.DBus.Error.UnknownObject":
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	case "io.github.nskaggs.perfuncted.Gnome1.Error.NotFound":
		return fmt.Errorf("%w: %w", ErrObjectNotFound, err)
	case "io.github.nskaggs.perfuncted.Gnome1.Error.Unsupported":
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	default:
		if strings.Contains(strings.ToLower(dbusErr.Error()), "window not found") {
			return fmt.Errorf("%w: %w", ErrObjectNotFound, err)
		}
		return err
	}
}

// Ping verifies that the service is still responsive.
func (c *Client) Ping(ctx context.Context) error { return c.call(ctx, CoreInterface, "Ping") }

func (c *Client) ListWindows(ctx context.Context) ([]WindowInfo, error) {
	var windows []WindowInfo
	if err := c.call(ctx, WindowsInterface, "ListWindows", &windows); err != nil {
		return nil, fmt.Errorf("gnome bridge: list windows: %w", err)
	}
	return windows, nil
}

func (c *Client) GetWindow(ctx context.Context, id string) (WindowInfo, error) {
	var window WindowInfo
	if err := c.callWithArgsOut(ctx, WindowsInterface, "GetWindow", []any{id}, &window); err != nil {
		return WindowInfo{}, fmt.Errorf("gnome bridge: get window %q: %w", id, err)
	}
	return window, nil
}

func (c *Client) GetActiveWindow(ctx context.Context) (WindowInfo, error) {
	var window WindowInfo
	if err := c.call(ctx, WindowsInterface, "GetActiveWindow", &window); err != nil {
		return WindowInfo{}, fmt.Errorf("gnome bridge: get active window: %w", err)
	}
	return window, nil
}

// SubscribeWindowEvents subscribes to lifecycle and focus signals from the
// Windows interface. The returned cancel function is idempotent and must be
// called when the subscriber is no longer needed.
func (c *Client) SubscribeWindowEvents(ctx context.Context) (<-chan WindowEvent, func(), error) {
	ctx = contextutil.Default(ctx)
	c.mu.Lock()
	if c.closed || c.conn == nil {
		c.mu.Unlock()
		return nil, nil, fmt.Errorf("%w: client is closed", ErrUnavailable)
	}
	conn := c.conn
	c.mu.Unlock()

	matchOptions := []dbus.MatchOption{
		dbus.WithMatchInterface(WindowsInterface),
		dbus.WithMatchObjectPath(dbus.ObjectPath(ObjectPath)),
	}
	addCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		addCtx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	if err := conn.AddMatchSignalContext(addCtx, matchOptions...); err != nil {
		return nil, nil, fmt.Errorf("gnome bridge: subscribe window events: %w", err)
	}
	raw := make(chan *dbus.Signal, 32)
	conn.Signal(raw)
	events := make(chan WindowEvent, 32)
	stop := make(chan struct{})
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			close(stop)
			conn.RemoveSignal(raw)
			cleanupCtx, cleanup := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cleanup()
			_ = conn.RemoveMatchSignalContext(cleanupCtx, matchOptions...)
		})
	}
	go func() {
		defer close(events)
		defer cancel()
		pending := make([]WindowEvent, 0, maxWindowEventQueue)
		for {
			var eventOut chan<- WindowEvent
			var next WindowEvent
			if len(pending) != 0 {
				eventOut = events
				next = pending[0]
			}
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case signal, ok := <-raw:
				if !ok {
					return
				}
				event, ok := decodeWindowEvent(signal)
				if ok {
					pending = enqueueWindowEvent(pending, event)
				}
			case eventOut <- next:
				pending = pending[1:]
			}
		}
	}()
	return events, cancel, nil
}

const maxWindowEventQueue = 64

func enqueueWindowEvent(queue []WindowEvent, event WindowEvent) []WindowEvent {
	if key, coalesce := windowEventQueueKey(event); coalesce {
		for i := len(queue) - 1; i >= 0; i-- {
			if existingKey, _ := windowEventQueueKey(queue[i]); existingKey == key {
				queue[i] = event
				return queue
			}
		}
	}
	if len(queue) >= maxWindowEventQueue {
		for i, existing := range queue {
			if existing.Kind == WindowChangedEvent {
				copy(queue[i:], queue[i+1:])
				queue = queue[:len(queue)-1]
				return append(queue, event)
			}
		}
		return queue
	}
	return append(queue, event)
}

func windowEventQueueKey(event WindowEvent) (string, bool) {
	switch event.Kind {
	case WindowChangedEvent:
		return "window-changed:" + event.ID, true
	case FocusChangedEvent:
		return "focus-changed", true
	default:
		return "", false
	}
}

func decodeWindowEvent(signal *dbus.Signal) (WindowEvent, bool) {
	if signal == nil || signal.Path != dbus.ObjectPath(ObjectPath) {
		return WindowEvent{}, false
	}
	switch signal.Name {
	case WindowsInterface + ".WindowAdded":
		return decodeWindowInfoEvent(signal, WindowAddedEvent)
	case WindowsInterface + ".WindowChanged":
		return decodeWindowInfoEvent(signal, WindowChangedEvent)
	case WindowsInterface + ".WindowRemoved":
		if len(signal.Body) != 1 {
			return WindowEvent{}, false
		}
		id, ok := signal.Body[0].(string)
		return WindowEvent{Kind: WindowRemovedEvent, ID: id}, ok
	case WindowsInterface + ".FocusChanged":
		if len(signal.Body) != 1 {
			return WindowEvent{}, false
		}
		id, ok := signal.Body[0].(string)
		return WindowEvent{Kind: FocusChangedEvent, ID: id}, ok
	default:
		return WindowEvent{}, false
	}
}

func decodeWindowInfoEvent(signal *dbus.Signal, kind WindowEventKind) (WindowEvent, bool) {
	if len(signal.Body) != 1 {
		return WindowEvent{}, false
	}
	var window WindowInfo
	if err := dbus.Store(signal.Body, &window); err != nil {
		return WindowEvent{}, false
	}
	return WindowEvent{Kind: kind, Window: window, ID: window.ID}, true
}

func (c *Client) windowAction(ctx context.Context, method, id string, args ...any) error {
	params := append([]any{id}, args...)
	if err := c.callWithArgs(ctx, WindowsInterface, method, params...); err != nil {
		return fmt.Errorf("gnome bridge: %s %q: %w", method, id, err)
	}
	return nil
}

func (c *Client) callWithArgs(ctx context.Context, iface, method string, args ...any) error {
	return c.callWithArgsOut(ctx, iface, method, args)
}

func (c *Client) callWithArgsOut(ctx context.Context, iface, method string, args []any, out ...any) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed || c.obj == nil {
		c.mu.Unlock()
		return fmt.Errorf("%w: client is closed", ErrUnavailable)
	}
	object := c.obj
	c.mu.Unlock()
	call := object.CallWithContext(ctx, iface+"."+method, 0, args...)
	if call.Err != nil {
		return translateDBusError(call.Err)
	}
	if len(out) != 0 {
		if err := call.Store(out...); err != nil {
			return fmt.Errorf("decode %s.%s reply: %w", iface, method, err)
		}
	}
	return nil
}

func (c *Client) Activate(ctx context.Context, id string) error {
	return c.windowAction(ctx, "Activate", id)
}
func (c *Client) Move(ctx context.Context, id string, x, y int32) error {
	return c.windowAction(ctx, "Move", id, x, y)
}
func (c *Client) Resize(ctx context.Context, id string, width, height int32) error {
	return c.windowAction(ctx, "Resize", id, width, height)
}
func (c *Client) Minimize(ctx context.Context, id string) error {
	return c.windowAction(ctx, "Minimize", id)
}
func (c *Client) Maximize(ctx context.Context, id string) error {
	return c.windowAction(ctx, "Maximize", id)
}
func (c *Client) Restore(ctx context.Context, id string) error {
	return c.windowAction(ctx, "Restore", id)
}
func (c *Client) Fullscreen(ctx context.Context, id string) error {
	return c.windowAction(ctx, "Fullscreen", id)
}
func (c *Client) Unfullscreen(ctx context.Context, id string) error {
	return c.windowAction(ctx, "Unfullscreen", id)
}
func (c *Client) CloseWindow(ctx context.Context, id string) error {
	return c.windowAction(ctx, "Close", id)
}

// CaptureFull writes a PNG into fd through the bridge's Unix-FD argument and
// returns the logical and physical capture geometry.
func (c *Client) CaptureFull(ctx context.Context, fd int) (ScreenCapture, error) {
	var capture ScreenCapture
	err := c.capture(
		ctx,
		"CaptureFull",
		fd,
		nil,
		&capture.X,
		&capture.Y,
		&capture.Width,
		&capture.Height,
		&capture.PixelWidth,
		&capture.PixelHeight,
		&capture.Scale,
	)
	return capture, err
}

// CaptureRegion writes a PNG of rect into fd. Coordinates use GNOME's global
// logical screen coordinate space, the same space used by window geometry and
// public screen operations. It returns the logical and physical capture
// geometry.
func (c *Client) CaptureRegion(ctx context.Context, fd int, rect image.Rectangle) (ScreenCapture, error) {
	if rect.Empty() {
		return ScreenCapture{}, fmt.Errorf("gnome bridge: empty capture region")
	}
	var capture ScreenCapture
	err := c.capture(
		ctx,
		"CaptureRegion",
		fd,
		[]any{int32(rect.Min.X), int32(rect.Min.Y), int32(rect.Dx()), int32(rect.Dy())},
		&capture.X,
		&capture.Y,
		&capture.Width,
		&capture.Height,
		&capture.PixelWidth,
		&capture.PixelHeight,
		&capture.Scale,
	)
	return capture, err
}

func (c *Client) capture(ctx context.Context, method string, fd int, args []any, out ...any) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed || c.obj == nil {
		c.mu.Unlock()
		return fmt.Errorf("%w: client is closed", ErrUnavailable)
	}
	conn := c.conn
	object := c.obj
	c.mu.Unlock()
	if !c.supportsFDs && (conn == nil || !conn.SupportsUnixFDs()) {
		return ErrUnixFDUnsupported
	}
	params := append([]any{dbus.UnixFD(fd)}, args...)
	call := object.CallWithContext(ctx, ScreenInterface+"."+method, 0, params...)
	if call.Err != nil {
		return fmt.Errorf("gnome bridge: %s: %w", method, translateDBusError(call.Err))
	}
	if len(out) > 0 {
		if err := call.Store(out...); err != nil {
			return fmt.Errorf("decode %s reply: %w", method, err)
		}
	}
	return nil
}

func (c *Client) Key(ctx context.Context, keyval uint32, pressed bool) error {
	return c.callWithArgs(ctx, InputInterface, "Key", keyval, pressed)
}
func (c *Client) Text(ctx context.Context, text string) error {
	return c.callWithArgs(ctx, InputInterface, "Text", text)
}
func (c *Client) Paste(ctx context.Context, text string) error {
	return c.callWithArgs(ctx, InputInterface, "Paste", text)
}
func (c *Client) PointerMove(ctx context.Context, x, y int32) error {
	return c.callWithArgs(ctx, InputInterface, "PointerMove", x, y)
}
func (c *Client) PointerButton(ctx context.Context, button uint32, pressed bool) error {
	return c.callWithArgs(ctx, InputInterface, "PointerButton", button, pressed)
}
func (c *Client) Scroll(ctx context.Context, axis string, amount float64) error {
	return c.callWithArgs(ctx, InputInterface, "Scroll", axis, amount)
}
func (c *Client) PointerLocation(ctx context.Context) (int, int, error) {
	var x, y int32
	if err := c.call(ctx, InputInterface, "PointerLocation", &x, &y); err != nil {
		return 0, 0, fmt.Errorf("gnome bridge: pointer location: %w", err)
	}
	return int(x), int(y), nil
}

func (c *Client) GetText(ctx context.Context) (string, error) {
	var text string
	if err := c.call(ctx, ClipboardInterface, "GetText", &text); err != nil {
		return "", fmt.Errorf("gnome bridge: clipboard get: %w", err)
	}
	return text, nil
}
func (c *Client) SetText(ctx context.Context, text string) error {
	if err := c.callWithArgs(ctx, ClipboardInterface, "SetText", text); err != nil {
		return fmt.Errorf("gnome bridge: clipboard set: %w", err)
	}
	return nil
}

// Close releases the D-Bus connection and is safe to call more than once.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		done := c.closeDone
		c.mu.Unlock()
		<-done
		c.mu.Lock()
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	c.closed = true
	if c.closeDone == nil {
		c.closeDone = make(chan struct{})
	}
	conn := c.conn
	c.conn = nil
	c.obj = nil
	done := c.closeDone
	c.mu.Unlock()
	var err error
	if conn != nil {
		err = conn.Close()
	}
	c.mu.Lock()
	c.closeErr = err
	close(done)
	c.mu.Unlock()
	return err
}
