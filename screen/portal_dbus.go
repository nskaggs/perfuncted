//go:build linux
// +build linux

package screen

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

var _ Screenshotter = (*PortalDBusBackend)(nil)

// GrabFullHash returns a fast pixel hash of the entire screen.
func (b *PortalDBusBackend) GrabFullHash(ctx context.Context) (uint32, error) {
	img, err := b.Grab(ctx, image.Rectangle{})
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

// GrabRegionHash returns a fast pixel hash of rect.
func (b *PortalDBusBackend) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	img, err := b.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

// PortalDBusBackend captures the screen via the xdg-desktop-portal Screenshot
// interface (org.freedesktop.portal.Screenshot). Each Grab call takes a full
// workspace screenshot and returns the cropped region. No PipeWire required.
//
// The compositor may show a one-time consent dialog on first use; once granted
// the permission is remembered. Sandboxed (Flatpak) environments may require
// additional portal permissions.
type PortalDBusBackend struct {
	conn *dbus.Conn

	captureOnce sync.Once
	captureGate chan struct{}
	healthMu    sync.RWMutex
	lastFailure string
	timeoutMu   sync.RWMutex
	policyLimit time.Duration
}

const (
	portalDest  = "org.freedesktop.portal.Desktop"
	portalPath  = "/org/freedesktop/portal/desktop"
	portalSsIf  = "org.freedesktop.portal.Screenshot"
	portalReqIf = "org.freedesktop.portal.Request"
	// portalProtocolTimeout bounds a response request because the portal
	// protocol provides no caller-configurable consent deadline.
	portalProtocolTimeout = 30 * time.Second
)

// portalCaptureTimeout returns the smaller of the caller policy and the
// portal protocol ceiling. A portal screenshot always captures and decodes a
// full workspace image, so it is deliberately unsuitable for tight polling.
func portalCaptureTimeout(policyLimit time.Duration) time.Duration {
	if policyLimit <= 0 || policyLimit > portalProtocolTimeout {
		return portalProtocolTimeout
	}
	return policyLimit
}

func (b *PortalDBusBackend) initCaptureGate() {
	b.captureOnce.Do(func() {
		b.captureGate = make(chan struct{}, 1)
		b.policyLimit = portalProtocolTimeout
	})
}

func (b *PortalDBusBackend) captureTimeoutPolicy() (policy, effective time.Duration) {
	b.initCaptureGate()
	b.timeoutMu.RLock()
	policy = b.policyLimit
	b.timeoutMu.RUnlock()
	return policy, portalCaptureTimeout(policy)
}

// SetCaptureTimeout sets the session policy limit for portal requests. The
// protocol ceiling remains authoritative even when the policy is larger.
func (b *PortalDBusBackend) SetCaptureTimeout(policyLimit time.Duration) {
	if b == nil {
		return
	}
	b.initCaptureGate()
	b.timeoutMu.Lock()
	b.policyLimit = policyLimit
	b.timeoutMu.Unlock()
}

// Diagnostics reports the bounded cost, serialization, timeout, and latest
// failure state of this portal backend.
func (b *PortalDBusBackend) Diagnostics() []string {
	if b == nil {
		return []string{"backend unavailable"}
	}
	policy, timeout := b.captureTimeoutPolicy()
	b.healthMu.RLock()
	lastFailure := b.lastFailure
	b.healthMu.RUnlock()
	diagnostics := []string{
		"capture cost: every request captures and decodes a full-screen PNG",
		"capture concurrency: requests are serialized per backend",
		"high-frequency waits: unsupported because each read is a full-screen capture",
		fmt.Sprintf("capture timeout: policy %s, protocol ceiling %s, effective %s", policy, portalProtocolTimeout, timeout),
	}
	if lastFailure != "" {
		diagnostics = append(diagnostics, "last failure: "+lastFailure)
	} else {
		diagnostics = append(diagnostics, "connection health: no recorded failures")
	}
	return diagnostics
}

func (b *PortalDBusBackend) recordFailure(err error) {
	if b == nil || err == nil {
		return
	}
	b.healthMu.Lock()
	b.lastFailure = err.Error()
	b.healthMu.Unlock()
}

func fileURIPath(fileURI string) (string, error) {
	const prefix = "file://"
	if !strings.HasPrefix(fileURI, prefix) {
		return "", fmt.Errorf("unsupported URI scheme")
	}
	path := strings.TrimPrefix(fileURI, prefix)
	switch {
	case strings.HasPrefix(path, "/"):
	case strings.HasPrefix(path, "localhost/"):
		path = "/" + strings.TrimPrefix(path, "localhost/")
	default:
		return "", fmt.Errorf("unsupported file URI host")
	}

	var b strings.Builder
	b.Grow(len(path))
	for i := 0; i < len(path); i++ {
		if path[i] != '%' {
			b.WriteByte(path[i])
			continue
		}
		if i+2 >= len(path) {
			return "", fmt.Errorf("truncated escape")
		}
		v, err := strconv.ParseUint(path[i+1:i+3], 16, 8)
		if err != nil {
			return "", fmt.Errorf("invalid escape %q", path[i:i+3])
		}
		b.WriteByte(byte(v))
		i += 2
	}
	return b.String(), nil
}

func portalUniqueName(names []string) (string, error) {
	for _, name := range names {
		if strings.HasPrefix(name, ":") {
			return name, nil
		}
	}
	return "", fmt.Errorf("no unique bus name available")
}

func portalRequestPath(uniqueName, token string) dbus.ObjectPath {
	sender := strings.ReplaceAll(strings.TrimPrefix(uniqueName, ":"), ".", "_")
	return dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
}

func portalSignalMatches(sig *dbus.Signal, paths ...dbus.ObjectPath) bool {
	if sig == nil {
		return false
	}
	for _, path := range paths {
		if path != "" && sig.Path == path {
			return true
		}
	}
	return false
}

// NewPortalDBusBackendForBus verifies that the xdg-desktop-portal Screenshot
// interface is reachable on the session bus at addr.
func NewPortalDBusBackendForBus(addr string) (*PortalDBusBackend, error) {
	return NewPortalDBusBackendForBusContext(context.Background(), addr)
}

// NewPortalDBusBackendForBusContext verifies portal availability while
// honoring ctx during session-bus setup and service discovery.
func NewPortalDBusBackendForBusContext(ctx context.Context, addr string) (*PortalDBusBackend, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if addr == "" {
		return nil, fmt.Errorf("screen/portal: D-Bus session unset")
	}
	conn, err := dbusutil.SessionBusAddressContext(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("screen/portal: D-Bus session: %w", err)
	}
	hasService, err := dbusutil.HasServiceContext(ctx, conn, portalDest)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("screen/portal: inspect session bus: %w", err), conn.Close())
	}
	if !hasService {
		return nil, errors.Join(
			fmt.Errorf("screen/portal: %s not on session bus", portalDest),
			conn.Close(),
		)
	}
	backend := &PortalDBusBackend{conn: conn}
	backend.initCaptureGate()
	return backend, nil
}

// Grab takes a full workspace screenshot via the portal and returns the
// requested rectangle. The portal may show a consent dialog on first use.
func (b *PortalDBusBackend) Grab(ctx context.Context, rect image.Rectangle) (img image.Image, err error) { //nolint:gocyclo
	ctx = contextutil.Default(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, fmt.Errorf("screen/portal: grab canceled: %w", ctxErr)
	}
	if b == nil || b.conn == nil {
		return nil, fmt.Errorf("screen/portal: backend not initialised")
	}
	b.initCaptureGate()
	select {
	case b.captureGate <- struct{}{}:
		defer func() { <-b.captureGate }()
	case <-ctx.Done():
		return nil, fmt.Errorf("screen/portal: screenshot canceled: %w", ctx.Err())
	}
	defer func() {
		if err != nil {
			b.recordFailure(err)
		}
	}()

	requestPolicy, requestTimeout := b.captureTimeoutPolicy()
	requestCtx, requestCancel := context.WithTimeout(ctx, requestTimeout)
	defer requestCancel()
	if requestErr := requestCtx.Err(); requestErr != nil {
		return nil, fmt.Errorf("screen/portal: screenshot canceled: %w", requestErr)
	}
	// Build a unique token; the portal embeds it in the request handle path.
	token := fmt.Sprintf("pf%d", time.Now().UnixNano())

	// Listen for all portal screenshot responses before making the request so we
	// do not miss a fast reply if the returned handle differs from the predicted
	// path.
	uniqueName, err := portalUniqueName(b.conn.Names())
	if err != nil {
		return nil, fmt.Errorf("screen/portal: %w", err)
	}
	expectedHandlePath := portalRequestPath(uniqueName, token)

	ch := make(chan *dbus.Signal, 4)
	b.conn.Signal(ch)
	defer b.conn.RemoveSignal(ch)
	matchOptions := []dbus.MatchOption{
		dbus.WithMatchSender(portalDest),
		dbus.WithMatchInterface(portalReqIf),
		dbus.WithMatchMember("Response"),
	}
	if err := b.conn.AddMatchSignalContext(requestCtx, matchOptions...); err != nil {
		return nil, fmt.Errorf("screen/portal: AddMatch: %w", err)
	}
	defer func(cleanupCtx context.Context) {
		_ = b.conn.RemoveMatchSignalContext(cleanupCtx, matchOptions...)
	}(context.WithoutCancel(requestCtx))

	obj := b.conn.Object(portalDest, portalPath)
	opts := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token),
		"interactive":  dbus.MakeVariant(false),
	}
	var gotHandle dbus.ObjectPath
	if err := obj.CallWithContext(requestCtx, portalSsIf+".Screenshot", 0, "", opts).Store(&gotHandle); err != nil {
		return nil, fmt.Errorf("screen/portal: Screenshot: %w", err)
	}
	if gotHandle == "" {
		gotHandle = expectedHandlePath
	}

	// Wait for the portal response. The compositor may block for user consent,
	// but the effective session policy and protocol ceiling bound the wait.
	timer := time.NewTimer(requestTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("screen/portal: screenshot canceled: %w", ctx.Err())
		case <-requestCtx.Done():
			if ctx.Err() != nil {
				return nil, fmt.Errorf("screen/portal: screenshot canceled: %w", ctx.Err())
			}
			return nil, fmt.Errorf("screen/portal: timed out waiting for screenshot (policy/protocol %s/%s, effective %s)", requestPolicy, portalProtocolTimeout, requestTimeout)
		case sig, ok := <-ch:
			if !ok {
				return nil, fmt.Errorf("screen/portal: D-Bus signal channel closed")
			}
			if !portalSignalMatches(sig, gotHandle, expectedHandlePath) || len(sig.Body) < 2 {
				continue
			}
			code, _ := sig.Body[0].(uint32)
			if code != 0 {
				return nil, fmt.Errorf("screen/portal: screenshot denied (code=%d)", code)
			}
			results, _ := sig.Body[1].(map[string]dbus.Variant)
			uriVar, ok := results["uri"]
			if !ok {
				return nil, fmt.Errorf("screen/portal: no URI in response")
			}
			fileURI, _ := uriVar.Value().(string)
			path, err := fileURIPath(fileURI)
			if err != nil {
				return nil, fmt.Errorf("screen/portal: parse URI %q: %w", fileURI, err)
			}
			f, root, err := openScreenshotFile(path)
			if err != nil {
				return nil, fmt.Errorf("screen/portal: open %s: %w", path, err)
			}
			img, err := png.Decode(f)
			closeErr := f.Close()
			rootCloseErr := root.Close()
			if err == nil && closeErr != nil {
				return nil, fmt.Errorf("screen/portal: close %s: %w", path, closeErr)
			}
			if err == nil && rootCloseErr != nil {
				return nil, fmt.Errorf("screen/portal: close root for %s: %w", path, rootCloseErr)
			}
			if err != nil {
				return nil, fmt.Errorf("screen/portal: decode PNG: %w", err)
			}
			if rect.Empty() {
				return img, nil
			}
			return cropImage(img, rect), nil
		case <-timer.C:
			return nil, fmt.Errorf("screen/portal: timed out waiting for screenshot (policy/protocol %s/%s, effective %s)", requestPolicy, portalProtocolTimeout, requestTimeout)
		}
	}
}

// Close releases the portal D-Bus connection.
func (b *PortalDBusBackend) Close() error {
	if b == nil || b.conn == nil {
		return nil
	}
	return b.conn.Close()
}
