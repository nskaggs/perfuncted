package input

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/bendahl/uinput"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/keymap"
)

const (
	remoteDesktopBus       = "org.freedesktop.portal.Desktop"
	remoteDesktopPath      = "/org/freedesktop/portal/desktop"
	remoteDesktopInterface = "org.freedesktop.portal.RemoteDesktop"
	portalRequestInterface = "org.freedesktop.portal.Request"
	portalSessionInterface = "org.freedesktop.portal.Session"
	portalRequestTimeout   = 45 * time.Second
	remoteOperationTimeout = 5 * time.Second

	portalDeviceKeyboard = uint32(1)
	portalDevicePointer  = uint32(2)
	portalPersistNever   = uint32(0)
	portalPersistForever = uint32(2)
)

type portalSession struct {
	conn          *dbus.Conn
	path          dbus.ObjectPath
	signals       chan *dbus.Signal
	match         []dbus.MatchOption
	resources     atomic.Bool
	resourcesDone chan struct{}
	resourcesErr  error
}

// OpenRemoteDesktopContext opens the opt-in XDG RemoteDesktop portal and
// establishes an EIS sender connection after user authorization. It never
// selects another input backend if the portal path fails.
func OpenRemoteDesktopContext(ctx context.Context, rt env.Runtime, options RemoteDesktopOptions) (Inputter, error) { //nolint:gocyclo // portal startup is a single consent, token, FD, and session-ownership transaction.
	if ctx == nil {
		return nil, fmt.Errorf("input/remote-desktop: %w: nil context", ErrRemoteDesktopUnavailable)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if rt.SocketPath() == "" {
		return nil, fmt.Errorf("input/remote-desktop: Wayland display is unavailable: %w", ErrRemoteDesktopUnavailable)
	}
	requestCtx, cancel := context.WithTimeout(ctx, portalRequestTimeout)
	defer cancel()
	conn, err := dbusutil.SessionBusAddressContext(requestCtx, rt.Get("DBUS_SESSION_BUS_ADDRESS"))
	if err != nil {
		return nil, fmt.Errorf("input/remote-desktop: session bus: %w: %w", ErrRemoteDesktopUnavailable, err)
	}
	portal := &portalSession{conn: conn, resourcesDone: make(chan struct{})}
	open := true
	defer func() { //nolint:contextcheck // failed startup closes acquired portal resources with bounded teardown context.
		if open {
			_ = portal.close()
		}
	}()
	if !conn.SupportsUnixFDs() {
		return nil, fmt.Errorf("input/remote-desktop: session bus does not support descriptor passing: %w", ErrRemoteDesktopUnavailable)
	}
	err = requireRemoteDesktopV2(requestCtx, conn)
	if err != nil {
		return nil, err
	}
	var tokenLock *portalTokenLock
	var restoreToken string
	if options.PersistPermission {
		tokenLock, err = lockPortalTokenStore(rt)
		if err != nil {
			return nil, fmt.Errorf("input/remote-desktop: secure restore-token store: %w", err)
		}
		defer tokenLock.Close()
		restoreToken, err = tokenLock.consume()
		if err != nil {
			return nil, fmt.Errorf("input/remote-desktop: consume restore token: %w", err)
		}
	}
	uniqueName, err := portalUniqueName(conn.Names())
	if err != nil {
		return nil, fmt.Errorf("input/remote-desktop: %w: %w", ErrRemoteDesktopUnavailable, err)
	}
	sessionHandle, err := portalCreateSession(requestCtx, conn, uniqueName)
	if err != nil {
		return nil, err
	}
	portal.path = dbus.ObjectPath(sessionHandle)
	if !portal.path.IsValid() {
		return nil, fmt.Errorf("input/remote-desktop: portal returned an invalid session handle: %w", ErrRemoteDesktopUnavailable)
	}
	err = portal.subscribeClosed(requestCtx)
	if err != nil {
		return nil, fmt.Errorf("input/remote-desktop: subscribe session lifecycle: %w: %w", ErrRemoteDesktopUnavailable, err)
	}
	selectOptions := map[string]dbus.Variant{
		"types":        dbus.MakeVariant(portalDeviceKeyboard | portalDevicePointer),
		"persist_mode": dbus.MakeVariant(portalPersistNever),
	}
	if options.PersistPermission {
		selectOptions["persist_mode"] = dbus.MakeVariant(portalPersistForever)
		if restoreToken != "" {
			selectOptions["restore_token"] = dbus.MakeVariant(restoreToken)
		}
	}
	if _, err = portalRequest(requestCtx, conn, uniqueName, "SelectDevices", []any{portal.path, selectOptions}); err != nil {
		return nil, err
	}
	startResults, err := portalRequest(requestCtx, conn, uniqueName, "Start", []any{portal.path, options.ParentWindow, map[string]dbus.Variant{}})
	if err != nil {
		return nil, err
	}
	devices, ok := startResults["devices"]
	if !ok {
		return nil, fmt.Errorf("input/remote-desktop: portal response omitted authorized devices: %w", ErrRemoteDesktopDenied)
	}
	deviceMask, ok := devices.Value().(uint32)
	if !ok || deviceMask&(portalDeviceKeyboard|portalDevicePointer) != (portalDeviceKeyboard|portalDevicePointer) {
		return nil, fmt.Errorf("input/remote-desktop: portal did not authorize keyboard and pointer input: %w", ErrRemoteDesktopDenied)
	}
	newRestoreToken := ""
	if options.PersistPermission {
		variant, exists := startResults["restore_token"]
		if !exists {
			return nil, fmt.Errorf("input/remote-desktop: portal did not grant persistent permission: %w", ErrRemoteDesktopDenied)
		}
		newRestoreToken, ok = variant.Value().(string)
		if !ok || newRestoreToken == "" {
			return nil, fmt.Errorf("input/remote-desktop: portal returned a malformed persistent permission token: %w", ErrRemoteDesktopDenied)
		}
		err = tokenLock.store(newRestoreToken)
		if err != nil {
			return nil, fmt.Errorf("input/remote-desktop: persist restore token securely: %w", err)
		}
	}
	object := conn.Object(remoteDesktopBus, remoteDesktopPath)
	var eisFD dbus.UnixFD
	err = object.CallWithContext(requestCtx, remoteDesktopInterface+".ConnectToEIS", 0, portal.path, map[string]dbus.Variant{}).Store(&eisFD)
	if err != nil {
		return nil, fmt.Errorf("input/remote-desktop: ConnectToEIS: %w: %w", ErrRemoteDesktopUnavailable, err)
	}
	if int(eisFD) < 0 {
		return nil, fmt.Errorf("input/remote-desktop: portal returned an invalid EIS descriptor: %w", ErrRemoteDesktopUnavailable)
	}
	file := os.NewFile(uintptr(eisFD), "perfuncted-eis")
	if file == nil {
		return nil, fmt.Errorf("input/remote-desktop: wrap EIS descriptor: %w", ErrRemoteDesktopUnavailable)
	}
	transport, connErr := net.FileConn(file)
	fileCloseErr := file.Close()
	if connErr != nil {
		return nil, fmt.Errorf("input/remote-desktop: connect EIS descriptor: %w: %w", ErrRemoteDesktopUnavailable, connErr)
	}
	if fileCloseErr != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("input/remote-desktop: close received EIS descriptor: %w", fileCloseErr)
	}
	eisConn, ok := transport.(*net.UnixConn)
	if !ok {
		_ = transport.Close()
		return nil, fmt.Errorf("input/remote-desktop: EIS descriptor is not a Unix socket: %w", ErrRemoteDesktopUnavailable)
	}
	if deadline, ok := requestCtx.Deadline(); ok {
		if err = eisConn.SetDeadline(deadline); err != nil {
			_ = eisConn.Close()
			return nil, fmt.Errorf("input/remote-desktop: bound EIS setup to startup deadline: %w", err)
		}
	}
	sender, err := setupEISender(requestCtx, eisConn)
	if err != nil {
		return nil, fmt.Errorf("input/remote-desktop: establish EIS sender: %w", err)
	}
	if err = eisConn.SetDeadline(time.Time{}); err != nil {
		_ = sender.client.close()
		return nil, fmt.Errorf("input/remote-desktop: clear EIS setup deadline: %w", err)
	}
	backend := newRemoteDesktopBackend(sender, portal) //nolint:contextcheck // lifecycle teardown uses its own bounded context.
	open = false
	return backend, nil
}

func requireRemoteDesktopV2(ctx context.Context, conn *dbus.Conn) error {
	object := conn.Object(remoteDesktopBus, remoteDesktopPath)
	var version dbus.Variant
	err := object.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, remoteDesktopInterface, "version").Store(&version)
	if err != nil {
		return fmt.Errorf("input/remote-desktop: query portal version: %w: %w", ErrRemoteDesktopUnavailable, err)
	}
	value, ok := version.Value().(uint32)
	if !ok || value < 2 {
		return fmt.Errorf("input/remote-desktop: portal version 2 with EIS support is required: %w", ErrRemoteDesktopUnavailable)
	}
	return nil
}

func portalCreateSession(ctx context.Context, conn *dbus.Conn, uniqueName string) (string, error) {
	token, err := portalHandleToken()
	if err != nil {
		return "", err
	}
	options := map[string]dbus.Variant{"session_handle_token": dbus.MakeVariant(token + "s")}
	results, err := portalRequest(ctx, conn, uniqueName, "CreateSession", []any{options})
	if err != nil {
		return "", err
	}
	variant, ok := results["session_handle"]
	if !ok {
		return "", fmt.Errorf("input/remote-desktop: portal omitted session handle: %w", ErrRemoteDesktopUnavailable)
	}
	switch value := variant.Value().(type) {
	case string:
		return value, nil
	case dbus.ObjectPath:
		return string(value), nil
	default:
		return "", fmt.Errorf("input/remote-desktop: portal returned malformed session handle: %w", ErrRemoteDesktopUnavailable)
	}
}

func portalHandleToken() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("input/remote-desktop: create request token: %w", err)
	}
	return "pf" + hex.EncodeToString(random[:]), nil
}

func portalRequest(ctx context.Context, conn *dbus.Conn, uniqueName, method string, args []any) (map[string]dbus.Variant, error) { //nolint:gocyclo // response matching covers method return, cancellation, denial, and typed result validation.
	token, err := portalHandleToken()
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("input/remote-desktop: %s requires portal options", method)
	}
	last, ok := args[len(args)-1].(map[string]dbus.Variant)
	if !ok {
		return nil, fmt.Errorf("input/remote-desktop: %s options have wrong type", method)
	}
	options := make(map[string]dbus.Variant, len(last)+1)
	for key, value := range last {
		options[key] = value
	}
	options["handle_token"] = dbus.MakeVariant(token)
	args = append([]any(nil), args...)
	args[len(args)-1] = options
	expectedPath := portalRequestPath(uniqueName, token)
	requestCtx, cancel := context.WithTimeout(ctx, portalRequestTimeout)
	defer cancel()
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	match := []dbus.MatchOption{
		dbus.WithMatchSender(remoteDesktopBus),
		dbus.WithMatchInterface(portalRequestInterface),
		dbus.WithMatchMember("Response"),
	}
	if err := conn.AddMatchSignalContext(requestCtx, match...); err != nil {
		return nil, fmt.Errorf("input/remote-desktop: add portal response match: %w: %w", ErrRemoteDesktopUnavailable, err)
	}
	defer func() {
		cleanupCtx, cleanup := context.WithTimeout(context.WithoutCancel(requestCtx), time.Second)
		defer cleanup()
		_ = conn.RemoveMatchSignalContext(cleanupCtx, match...)
	}()
	var returnedPath dbus.ObjectPath
	object := conn.Object(remoteDesktopBus, remoteDesktopPath)
	if err := object.CallWithContext(requestCtx, remoteDesktopInterface+"."+method, 0, args...).Store(&returnedPath); err != nil {
		if requestCtx.Err() != nil {
			return nil, requestCtx.Err()
		}
		return nil, fmt.Errorf("input/remote-desktop: %s request: %w: %w", method, ErrRemoteDesktopUnavailable, err)
	}
	if returnedPath == "" {
		returnedPath = expectedPath
	}
	for {
		select {
		case <-requestCtx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("input/remote-desktop: %s response timed out: %w", method, ErrRemoteDesktopUnavailable)
		case signal, ok := <-signals:
			if !ok {
				return nil, fmt.Errorf("input/remote-desktop: portal response stream closed: %w", ErrRemoteDesktopUnavailable)
			}
			if signal == nil || (signal.Path != returnedPath && signal.Path != expectedPath) || len(signal.Body) < 2 {
				continue
			}
			response, ok := signal.Body[0].(uint32)
			if !ok {
				return nil, fmt.Errorf("input/remote-desktop: malformed %s response: %w", method, ErrRemoteDesktopUnavailable)
			}
			if response != 0 {
				return nil, fmt.Errorf("input/remote-desktop: %s authorization response %d: %w", method, response, ErrRemoteDesktopDenied)
			}
			results, ok := signal.Body[1].(map[string]dbus.Variant)
			if !ok {
				return nil, fmt.Errorf("input/remote-desktop: malformed %s result map: %w", method, ErrRemoteDesktopUnavailable)
			}
			return results, nil
		}
	}
}

func portalRequestPath(uniqueName, token string) dbus.ObjectPath {
	sender := strings.ReplaceAll(strings.TrimPrefix(uniqueName, ":"), ".", "_")
	return dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
}

func portalUniqueName(names []string) (string, error) {
	for _, name := range names {
		if strings.HasPrefix(name, ":") {
			return name, nil
		}
	}
	return "", errors.New("session bus did not assign a unique name")
}

func (p *portalSession) subscribeClosed(ctx context.Context) error {
	p.signals = make(chan *dbus.Signal, 8)
	p.conn.Signal(p.signals)
	p.match = []dbus.MatchOption{
		dbus.WithMatchSender(remoteDesktopBus),
		dbus.WithMatchInterface(portalSessionInterface),
		dbus.WithMatchMember("Closed"),
		dbus.WithMatchObjectPath(p.path),
	}
	if err := p.conn.AddMatchSignalContext(ctx, p.match...); err != nil {
		p.conn.RemoveSignal(p.signals)
		p.signals = nil
		return err
	}
	return nil
}

func (p *portalSession) close() error {
	if p == nil || p.conn == nil {
		return nil
	}
	if !p.resources.CompareAndSwap(false, true) {
		<-p.resourcesDone
		return p.resourcesErr
	}
	var closeErr error
	if p.path != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		closeErr = p.conn.Object(remoteDesktopBus, p.path).CallWithContext(ctx, portalSessionInterface+".Close", 0).Err
		cancel()
	}
	if p.signals != nil {
		p.conn.RemoveSignal(p.signals)
	}
	if len(p.match) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		closeErr = errors.Join(closeErr, p.conn.RemoveMatchSignalContext(ctx, p.match...))
		cancel()
	}
	closeErr = errors.Join(closeErr, p.conn.Close())
	p.resourcesErr = closeErr
	close(p.resourcesDone)
	return closeErr
}

type remoteDesktopCommand struct {
	ctx  context.Context //nolint:containedctx // the serialized writer retains caller cancellation until dispatch finishes.
	run  func() error
	done chan error
}

type remoteStateError struct{ err error }

// RemoteDesktopBackend forwards operations only through the EIS connection
// authorized for this session. A single command owner serializes complete
// operation frames without holding locks across socket I/O.
type RemoteDesktopBackend struct {
	sender       *eiSender
	portal       *portalSession
	commands     chan remoteDesktopCommand
	stop         chan struct{}
	revoked      chan struct{}
	writerDone   chan struct{}
	closeDone    chan struct{}
	closeStarted atomic.Bool
	revokedState atomic.Pointer[remoteStateError]
	closeErr     error
	heldKeys     map[uint32]bool
	buttons      map[uint32]bool
	pointerX     int
	pointerY     int
	hasPointer   bool
}

func newRemoteDesktopBackend(sender *eiSender, portal *portalSession) *RemoteDesktopBackend {
	b := &RemoteDesktopBackend{
		sender: sender, portal: portal, commands: make(chan remoteDesktopCommand),
		stop: make(chan struct{}), revoked: make(chan struct{}), writerDone: make(chan struct{}), closeDone: make(chan struct{}),
		heldKeys: make(map[uint32]bool), buttons: make(map[uint32]bool),
	}
	go b.writeLoop()
	go b.watchLifecycle()
	return b
}

// SupportedOperations reports operations executable by the authorized EIS backend.
func (b *RemoteDesktopBackend) SupportedOperations() []string {
	return supportedOperations(false, true, "sync")
}

// Diagnostics reports bounded authorization and pointer-region details.
func (b *RemoteDesktopBackend) Diagnostics() []string {
	if b == nil {
		return []string{"portal EIS input unavailable"}
	}
	if b.sender == nil {
		return []string{"portal EIS input unavailable"}
	}
	return []string{
		"backend: XDG RemoteDesktop portal with EIS sender",
		"authorization: portal granted keyboard and pointer for this session",
		fmt.Sprintf("absolute pointer regions: %d", len(b.sender.regions)),
		"coordinate mapping: EIS logical screen coordinates, validated against advertised device regions",
	}
}

// PointerCoordinateSpace reports the logical coordinate space advertised by EIS.
func (b *RemoteDesktopBackend) PointerCoordinateSpace(context.Context) (CoordinateSpaceInfo, error) {
	if err := b.stateError(); err != nil {
		return CoordinateSpaceInfo{}, err
	}
	return CoordinateSpaceInfo{Kind: CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}, nil
}

func (b *RemoteDesktopBackend) stateError() error {
	if b == nil {
		return ErrRemoteDesktopUnavailable
	}
	if state := b.revokedState.Load(); state != nil {
		return state.err
	}
	select {
	case <-b.stop:
		return ErrRemoteDesktopUnavailable
	default:
		return nil
	}
}

func (b *RemoteDesktopBackend) submit(ctx context.Context, run func() error) error {
	if ctx == nil {
		return errors.New("input/remote-desktop: nil operation context")
	}
	if err := b.stateError(); err != nil {
		return err
	}
	command := remoteDesktopCommand{ctx: ctx, run: run, done: make(chan error, 1)}
	select {
	case b.commands <- command:
	case <-ctx.Done():
		return ctx.Err()
	case <-b.revoked:
		return b.stateError()
	case <-b.stop:
		return ErrRemoteDesktopUnavailable
	}
	select {
	case err := <-command.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-b.revoked:
		return b.stateError()
	case <-b.stop:
		return ErrRemoteDesktopUnavailable
	}
}

func (b *RemoteDesktopBackend) writeLoop() {
	defer close(b.writerDone)
	for {
		select {
		case command := <-b.commands:
			if err := command.ctx.Err(); err != nil {
				command.done <- err
				continue
			}
			if err := b.stateError(); err != nil {
				command.done <- err
				return
			}
			deadline, ok := command.ctx.Deadline()
			if !ok {
				deadline = time.Now().Add(remoteOperationTimeout)
			}
			conn := b.sender.client.conn
			_ = conn.SetWriteDeadline(deadline)
			cancelDone := make(chan struct{})
			stopCancellation := context.AfterFunc(command.ctx, func() {
				_ = conn.SetWriteDeadline(time.Now())
				close(cancelDone)
			})
			err := command.run()
			if !stopCancellation() {
				<-cancelDone
			}
			if err == nil && command.ctx.Err() != nil {
				err = command.ctx.Err()
			}
			_ = conn.SetWriteDeadline(time.Time{})
			command.done <- err
			if errors.Is(err, ErrRemoteDesktopRevoked) {
				return
			}
		case <-b.revoked:
			return
		case <-b.stop:
			return
		}
	}
}

func (b *RemoteDesktopBackend) write(frames ...[]byte) error {
	if err := b.sender.client.writeMessages(frames...); err != nil {
		return b.revoke(fmt.Errorf("EIS transport failed: %w", err))
	}
	return nil
}

func (b *RemoteDesktopBackend) revoke(cause error) error {
	if cause == nil {
		cause = ErrRemoteDesktopRevoked
	}
	state := &remoteStateError{err: fmt.Errorf("%w: %w", ErrRemoteDesktopRevoked, cause)}
	if b.revokedState.CompareAndSwap(nil, state) {
		close(b.revoked)
		_ = b.sender.client.close()
		_ = b.portal.close()
	}
	if current := b.revokedState.Load(); current != nil {
		return current.err
	}
	return ErrRemoteDesktopRevoked
}

func (b *RemoteDesktopBackend) watchLifecycle() {
	for {
		select {
		case signal, ok := <-b.portal.signals:
			if !ok {
				_ = b.revoke(errors.New("portal session signal stream ended"))
				return
			}
			if signal != nil && signal.Name == portalSessionInterface+".Closed" && signal.Path == b.portal.path {
				_ = b.revoke(errors.New("portal session closed"))
				return
			}
		case message := <-b.sender.client.messages:
			if b.isEISLifecycleFailure(message) {
				_ = b.revoke(errors.New("EIS session ended"))
				return
			}
		case <-b.sender.client.done:
			if b.closeStarted.Load() {
				return
			}
			cause := b.sender.client.readError()
			if cause == nil {
				cause = errors.New("EIS stream closed")
			}
			_ = b.revoke(cause)
			return
		case <-b.stop:
			return
		case <-b.revoked:
			return
		}
	}
}

func (b *RemoteDesktopBackend) isSelectedDevicePaused(message eiMessage) bool {
	if message.opcode == 0 || message.opcode == 8 {
		if message.object == b.sender.keyboardDev || message.object == b.sender.buttonDev || message.object == b.sender.scrollDev || message.object == b.sender.textDev {
			return true
		}
		for _, pointer := range b.sender.pointers {
			if message.object == pointer.device {
				return true
			}
		}
	}
	return false
}

func (b *RemoteDesktopBackend) isEISLifecycleFailure(message eiMessage) bool {
	if isEISDisconnected(message, b.sender.connection) || (message.object == b.sender.seat && message.opcode == 0) || b.isSelectedDevicePaused(message) {
		return true
	}
	if message.opcode != 0 {
		return false
	}
	if message.object == b.sender.keyboard || message.object == b.sender.button || message.object == b.sender.scroll || message.object == b.sender.text {
		return true
	}
	for _, pointer := range b.sender.pointers {
		if message.object == pointer.pointer {
			return true
		}
	}
	return false
}

// KeyDown presses and holds a key through the authorized EIS device.
func (b *RemoteDesktopBackend) KeyDown(ctx context.Context, key string) error {
	keycode, err := remoteKeyCode(key)
	if err != nil {
		return err
	}
	return b.submit(ctx, func() error { //nolint:contextcheck // revocation closes the session with independent bounded teardown.
		frame, err := eiKeyFrame(b.sender, keycode, true)
		if err != nil {
			return err
		}
		writeErr := b.write(frame)
		if writeErr == nil {
			b.heldKeys[keycode] = true
		}
		return writeErr
	})
}

// KeyUp releases a key through the authorized EIS device.
func (b *RemoteDesktopBackend) KeyUp(ctx context.Context, key string) error {
	keycode, err := remoteKeyCode(key)
	if err != nil {
		return err
	}
	return b.submit(ctx, func() error { //nolint:contextcheck // revocation closes the session with independent bounded teardown.
		frame, err := eiKeyFrame(b.sender, keycode, false)
		if err != nil {
			return err
		}
		writeErr := b.write(frame)
		if writeErr == nil {
			delete(b.heldKeys, keycode)
		}
		return writeErr
	})
}

func remoteKeyCode(key string) (uint32, error) { //nolint:gocyclo // explicit key-name mappings must preserve Linux input-event codes.
	key = strings.TrimSpace(key)
	if key == "" {
		return 0, errors.New("input: key name must not be empty")
	}
	if parsed, ok := keymap.FromString(key); ok {
		switch {
		case parsed >= keymap.KeyA && parsed <= keymap.KeyZ:
			return uint32(qwertyRuneMap()[rune('a'+parsed-keymap.KeyA)].keycode), nil
		case parsed >= keymap.Key0 && parsed <= keymap.Key9:
			return uint32(qwertyRuneMap()[rune('0'+parsed-keymap.Key0)].keycode), nil
		case parsed >= keymap.KeyF1 && parsed <= keymap.KeyF10:
			return uint32(uinput.KeyF1 + int(parsed-keymap.KeyF1)), nil
		}
		switch parsed {
		case keymap.KeySpace:
			return uinput.KeySpace, nil
		case keymap.KeyEnter:
			return uinput.KeyEnter, nil
		case keymap.KeyTab:
			return uinput.KeyTab, nil
		case keymap.KeyBackspace:
			return uinput.KeyBackspace, nil
		case keymap.KeyEscape:
			return uinput.KeyEsc, nil
		case keymap.KeyCtrl:
			return uinput.KeyLeftctrl, nil
		case keymap.KeyAlt:
			return uinput.KeyLeftalt, nil
		case keymap.KeyShift:
			return uinput.KeyLeftshift, nil
		case keymap.KeySuper:
			return uinput.KeyLeftmeta, nil
		case keymap.KeyUp:
			return uinput.KeyUp, nil
		case keymap.KeyDown:
			return uinput.KeyDown, nil
		case keymap.KeyLeft:
			return uinput.KeyLeft, nil
		case keymap.KeyRight:
			return uinput.KeyRight, nil
		case keymap.KeyHome:
			return uinput.KeyHome, nil
		case keymap.KeyEnd:
			return uinput.KeyEnd, nil
		case keymap.KeyPageUp:
			return uinput.KeyPageup, nil
		case keymap.KeyPageDown:
			return uinput.KeyPagedown, nil
		case keymap.KeyInsert:
			return uinput.KeyInsert, nil
		case keymap.KeyDelete:
			return uinput.KeyDelete, nil
		case keymap.KeyF11:
			return uinput.KeyF11, nil
		case keymap.KeyF12:
			return uinput.KeyF12, nil
		}
	}
	if utf8.RuneCountInString(key) == 1 {
		runes := qwertyRuneMap()
		r, _ := utf8.DecodeRuneInString(key)
		if mapping, ok := runes[r]; ok {
			return uint32(mapping.keycode), nil
		}
		if mapping, ok := runes[[]rune(strings.ToLower(key))[0]]; ok {
			return uint32(mapping.keycode), nil
		}
	}
	return 0, fmt.Errorf("input: unknown key %q", key)
}

// Type sends key syntax and literal text through the authorized EIS devices.
func (b *RemoteDesktopBackend) Type(ctx context.Context, value string) error {
	actions, err := parseKeySend(value)
	if err != nil {
		return err
	}
	keycodes := make([]uint32, len(actions))
	for i, action := range actions {
		if action.text != "" {
			if !utf8.ValidString(action.text) {
				return errors.New("input: text must be valid UTF-8")
			}
			if b.sender.text == 0 {
				return unsupportedError("remote-desktop", "text input")
			}
			continue
		}
		keycodes[i], err = remoteKeyCode(action.key)
		if err != nil {
			return err
		}
		for _, modifier := range modifierNames(action.modifiers) {
			if _, err := remoteKeyCode(modifier); err != nil {
				return err
			}
		}
	}
	return b.submit(ctx, func() error { //nolint:contextcheck // command cancellation interrupts EIS writes; revocation owns independent teardown.
		for i, action := range actions {
			if err := ctx.Err(); err != nil {
				return err
			}
			if action.text != "" {
				frames, err := eiTextFrames(b.sender, action.text)
				if err != nil {
					return err
				}
				if writeErr := b.write(frames...); writeErr != nil { //nolint:contextcheck // a failed send revokes the portal session outside the operation context.
					return writeErr
				}
				continue
			}
			if actionErr := b.typeAction(action, keycodes[i]); actionErr != nil { //nolint:contextcheck // a failed send revokes the portal session outside the operation context.
				return actionErr
			}
		}
		return nil
	})
}

func modifierNames(mod modifiers) []string {
	var names []string
	if mod.shift {
		names = append(names, "shift")
	}
	if mod.ctrl {
		names = append(names, "ctrl")
	}
	if mod.alt {
		names = append(names, "alt")
	}
	if mod.super {
		names = append(names, "super")
	}
	return names
}

func (b *RemoteDesktopBackend) typeAction(action keySend, keycode uint32) error { //nolint:gocyclo // modifier and key state transitions are emitted and tracked as ordered frames.
	var temporary []uint32
	for _, name := range modifierNames(action.modifiers) {
		modifier, err := remoteKeyCode(name)
		if err != nil {
			return err
		}
		if b.heldKeys[modifier] {
			continue
		}
		temporary = append(temporary, modifier)
	}
	var modifierDown, modifierUp [][]byte
	for _, modifier := range temporary {
		frame, err := eiKeyFrame(b.sender, modifier, true)
		if err != nil {
			return err
		}
		modifierDown = append(modifierDown, frame)
	}
	for index := len(temporary) - 1; index >= 0; index-- {
		frame, err := eiKeyFrame(b.sender, temporary[index], false)
		if err != nil {
			return err
		}
		modifierUp = append(modifierUp, frame)
	}
	var actionFrames [][]byte
	switch {
	case action.down:
		frame, err := eiKeyFrame(b.sender, keycode, true)
		if err != nil {
			return err
		}
		actionFrames = append(actionFrames, frame)
	case action.up:
		frame, err := eiKeyFrame(b.sender, keycode, false)
		if err != nil {
			return err
		}
		actionFrames = append(actionFrames, frame)
	default:
		down, err := eiKeyFrame(b.sender, keycode, true)
		if err != nil {
			return err
		}
		up, err := eiKeyFrame(b.sender, keycode, false)
		if err != nil {
			return err
		}
		actionFrames = append(actionFrames, down, up)
	}
	frames := make([][]byte, 0, len(modifierDown)+len(actionFrames)+len(modifierUp))
	frames = append(frames, modifierDown...)
	frames = append(frames, actionFrames...)
	frames = append(frames, modifierUp...)
	for i, frame := range frames {
		if err := b.write(frame); err != nil {
			return err
		}
		if i < len(modifierDown) {
			b.heldKeys[temporary[i]] = true
			continue
		}
		actionIndex := i - len(modifierDown)
		if actionIndex < len(actionFrames) {
			switch {
			case action.down && actionIndex == 0:
				b.heldKeys[keycode] = true
			case action.up && actionIndex == 0:
				delete(b.heldKeys, keycode)
			case !action.down && !action.up && actionIndex == 0:
				b.heldKeys[keycode] = true
			case !action.down && !action.up && actionIndex == 1:
				delete(b.heldKeys, keycode)
			}
			continue
		}
		modifierIndex := i - len(modifierDown) - len(actionFrames)
		delete(b.heldKeys, temporary[len(temporary)-1-modifierIndex])
	}
	return nil
}

// TypeLiteral sends unparsed UTF-8 text through the authorized EIS text interface.
func (b *RemoteDesktopBackend) TypeLiteral(ctx context.Context, value string) error {
	frames, err := eiTextFrames(b.sender, value)
	if err != nil {
		return err
	}
	return b.submit(ctx, func() error { //nolint:contextcheck // command cancellation interrupts EIS writes; revocation owns independent teardown.
		if err := ctx.Err(); err != nil {
			return err
		}
		return b.write(frames...) //nolint:contextcheck // a failed send revokes the portal session outside the operation context.
	})
}

// MouseMove places the pointer inside one advertised EIS region.
func (b *RemoteDesktopBackend) MouseMove(ctx context.Context, x, y int) error {
	if !b.sender.validatePoint(x, y) {
		return fmt.Errorf("input/remote-desktop: (%d,%d) is outside the authorized EIS regions", x, y)
	}
	return b.submit(ctx, func() error { //nolint:contextcheck // command cancellation interrupts EIS writes; revocation owns independent teardown.
		frame, err := eiPointerFrame(b.sender, x, y)
		if err != nil {
			return err
		}
		writeErr := b.write(frame) //nolint:contextcheck // a failed send revokes the portal session outside the operation context.
		if writeErr == nil {
			b.pointerX, b.pointerY, b.hasPointer = x, y, true
		}
		return writeErr
	})
}

func remoteButtonCode(button int) (uint32, error) {
	if err := validateMouseButton("remote-desktop", button); err != nil {
		return 0, err
	}
	switch button {
	case 1:
		return 0x110, nil
	case 2:
		return 0x112, nil
	default:
		return 0x111, nil
	}
}

// MouseClick moves to the target point and sends one button press and release.
func (b *RemoteDesktopBackend) MouseClick(ctx context.Context, x, y, button int) error {
	buttonCode, err := remoteButtonCode(button)
	if err != nil {
		return err
	}
	if !b.sender.validatePoint(x, y) {
		return fmt.Errorf("input/remote-desktop: (%d,%d) is outside the authorized EIS regions", x, y)
	}
	return b.submit(ctx, func() error { //nolint:contextcheck // click cancellation or EIS failure retires the session with independent teardown.
		move, err := eiPointerFrame(b.sender, x, y)
		if err != nil {
			return err
		}
		if writeErr := b.write(move); writeErr != nil { //nolint:contextcheck // a failed send revokes the portal session outside the operation context.
			return writeErr
		}
		b.pointerX, b.pointerY, b.hasPointer = x, y, true
		down, err := eiButtonFrame(b.sender, buttonCode, true)
		if err != nil {
			return err
		}
		if writeErr := b.write(down); writeErr != nil { //nolint:contextcheck // a failed send revokes the portal session outside the operation context.
			return writeErr
		}
		b.buttons[buttonCode] = true
		if sleepErr := sleepContext(ctx, mouseClickHoldDuration); sleepErr != nil { //nolint:contextcheck // cancellation closes the portal session to clear the held button.
			return b.revoke(fmt.Errorf("mouse click canceled while button is held: %w", sleepErr)) //nolint:contextcheck // teardown uses an independent bounded context.
		}
		up, err := eiButtonFrame(b.sender, buttonCode, false)
		if err != nil {
			return err
		}
		writeErr := b.write(up) //nolint:contextcheck // a failed send revokes the portal session outside the operation context.
		if writeErr == nil {
			delete(b.buttons, buttonCode)
		}
		return writeErr
	})
}

// MouseDown presses and holds a supported pointer button.
func (b *RemoteDesktopBackend) MouseDown(ctx context.Context, button int) error {
	buttonCode, err := remoteButtonCode(button)
	if err != nil {
		return err
	}
	return b.submit(ctx, func() error { //nolint:contextcheck // command cancellation interrupts EIS writes; revocation owns independent teardown.
		frame, err := eiButtonFrame(b.sender, buttonCode, true)
		if err != nil {
			return err
		}
		writeErr := b.write(frame) //nolint:contextcheck // a failed send revokes the portal session outside the operation context.
		if writeErr == nil {
			b.buttons[buttonCode] = true
		}
		return writeErr
	})
}

// MouseUp releases a supported pointer button.
func (b *RemoteDesktopBackend) MouseUp(ctx context.Context, button int) error {
	buttonCode, err := remoteButtonCode(button)
	if err != nil {
		return err
	}
	return b.submit(ctx, func() error { //nolint:contextcheck // command cancellation interrupts EIS writes; revocation owns independent teardown.
		frame, err := eiButtonFrame(b.sender, buttonCode, false)
		if err != nil {
			return err
		}
		writeErr := b.write(frame) //nolint:contextcheck // a failed send revokes the portal session outside the operation context.
		if writeErr == nil {
			delete(b.buttons, buttonCode)
		}
		return writeErr
	})
}

// ScrollUp scrolls vertically toward the top of the content.
func (b *RemoteDesktopBackend) ScrollUp(ctx context.Context, clicks int) error {
	return b.scroll(ctx, 0, -clicks, clicks)
}

// ScrollDown scrolls vertically toward the bottom of the content.
func (b *RemoteDesktopBackend) ScrollDown(ctx context.Context, clicks int) error {
	return b.scroll(ctx, 0, clicks, clicks)
}

// ScrollLeft scrolls horizontally toward the left.
func (b *RemoteDesktopBackend) ScrollLeft(ctx context.Context, clicks int) error {
	return b.scroll(ctx, -clicks, 0, clicks)
}

// ScrollRight scrolls horizontally toward the right.
func (b *RemoteDesktopBackend) ScrollRight(ctx context.Context, clicks int) error {
	return b.scroll(ctx, clicks, 0, clicks)
}

func (b *RemoteDesktopBackend) scroll(ctx context.Context, x, y, clicks int) error {
	if err := validateScrollClicks(clicks); err != nil {
		return err
	}
	if clicks == 0 {
		return nil
	}
	if b.sender.scroll == 0 {
		return unsupportedError("remote-desktop", "scroll")
	}
	if x > math.MaxInt32/120 || x < math.MinInt32/120 || y > math.MaxInt32/120 || y < math.MinInt32/120 {
		return fmt.Errorf("input: scroll distance overflows the EIS protocol")
	}
	return b.submit(ctx, func() error { //nolint:contextcheck // command cancellation interrupts EIS writes; revocation owns independent teardown.
		frame, err := eiScrollFrame(b.sender, int32(x*120), int32(y*120))
		if err != nil {
			return err
		}
		return b.write(frame) //nolint:contextcheck // a failed send revokes the portal session outside the operation context.
	})
}

// PointerLocation is unsupported because EIS does not expose the current pointer.
func (*RemoteDesktopBackend) PointerLocation(context.Context) (int, int, error) {
	return 0, 0, unsupportedError("remote-desktop", "pointer location")
}

// Sync is unsupported because EIS does not expose an input completion barrier.
func (*RemoteDesktopBackend) Sync(context.Context) error {
	return unsupportedError("remote-desktop", "sync")
}

// Close releases held input, stops EIS devices, and closes the portal session.
func (b *RemoteDesktopBackend) Close() error {
	if b == nil {
		return nil
	}
	if !b.closeStarted.CompareAndSwap(false, true) {
		<-b.closeDone
		return b.closeErr
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	if err := b.submit(cleanupCtx, b.releaseHeldInput); err != nil && !errors.Is(err, ErrRemoteDesktopUnavailable) && !errors.Is(err, ErrRemoteDesktopRevoked) {
		b.closeErr = errors.Join(b.closeErr, err)
	}
	cancel()
	close(b.stop)
	_ = b.sender.client.close()
	<-b.writerDone
	b.closeErr = errors.Join(b.closeErr, b.portal.close())
	close(b.closeDone)
	return b.closeErr
}

func (b *RemoteDesktopBackend) releaseHeldInput() error {
	var frames [][]byte
	for key := range b.heldKeys {
		frame, err := eiKeyFrame(b.sender, key, false)
		if err != nil {
			return err
		}
		frames = append(frames, frame)
	}
	for button := range b.buttons {
		frame, err := eiButtonFrame(b.sender, button, false)
		if err != nil {
			return err
		}
		frames = append(frames, frame)
	}
	if err := b.write(frames...); err != nil {
		return err
	}
	devices := []uint64{b.sender.keyboardDev, b.sender.buttonDev, b.sender.scrollDev, b.sender.textDev}
	for _, pointer := range b.sender.pointers {
		devices = append(devices, pointer.device)
	}
	seen := make(map[uint64]struct{}, len(devices))
	for _, id := range devices {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if err := b.sender.client.writeMessages(eiRequest(id, 2, eiU32(b.sender.client.serial.Load()))); err != nil {
			return b.revoke(fmt.Errorf("stop EIS device: %w", err))
		}
	}
	clear(b.heldKeys)
	clear(b.buttons)
	return nil
}

var _ Inputter = (*RemoteDesktopBackend)(nil)
var _ PointerCoordinateSpaceReporter = (*RemoteDesktopBackend)(nil)
