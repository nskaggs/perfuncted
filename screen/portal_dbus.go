//go:build linux
// +build linux

package screen

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/ctxutil"
	"github.com/nskaggs/perfuncted/find"
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
}

const (
	portalDest  = "org.freedesktop.portal.Desktop"
	portalPath  = "/org/freedesktop/portal/desktop"
	portalSsIf  = "org.freedesktop.portal.Screenshot"
	portalReqIf = "org.freedesktop.portal.Request"
)

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
	return dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/portal/desktop/request/%s/%s", sender, token))
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

// NewPortalDBusBackend verifies that the xdg-desktop-portal Screenshot
// interface is reachable on the session bus.
func NewPortalDBusBackend() (*PortalDBusBackend, error) {
	return NewPortalDBusBackendForBus("")
}

// NewPortalDBusBackendForBus verifies that the xdg-desktop-portal Screenshot
// interface is reachable on the session bus at addr.
func NewPortalDBusBackendForBus(addr string) (*PortalDBusBackend, error) {
	if addr == "" {
		return nil, fmt.Errorf("screen/portal: D-Bus session unset")
	}
	conn, err := dbusutil.SessionBusAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("screen/portal: D-Bus session: %w", err)
	}
	if !dbusutil.HasService(conn, portalDest) {
		conn.Close()
		return nil, fmt.Errorf("screen/portal: %s not on session bus", portalDest)
	}
	return &PortalDBusBackend{conn: conn}, nil
}

// Grab takes a full workspace screenshot via the portal and returns the
// requested rectangle. The portal may show a consent dialog on first use.
func (b *PortalDBusBackend) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	ctx = ctxutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("screen/portal: grab canceled: %w", err)
	}
	if b == nil || b.conn == nil {
		return nil, fmt.Errorf("screen/portal: backend not initialised")
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
	if err := b.conn.AddMatchSignal(
		dbus.WithMatchSender(portalDest),
		dbus.WithMatchInterface(portalReqIf),
		dbus.WithMatchMember("Response"),
	); err != nil {
		return nil, fmt.Errorf("screen/portal: AddMatch: %w", err)
	}

	obj := b.conn.Object(portalDest, portalPath)
	opts := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token),
		"interactive":  dbus.MakeVariant(false),
	}
	var gotHandle dbus.ObjectPath
	if err := obj.Call(portalSsIf+".Screenshot", 0, "", opts).Store(&gotHandle); err != nil {
		return nil, fmt.Errorf("screen/portal: Screenshot: %w", err)
	}
	if gotHandle == "" {
		gotHandle = expectedHandlePath
	}

	// Wait for the portal response. The compositor may block for user consent.
	// Timeout is 30s to allow time for the user to respond to the consent dialog.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("screen/portal: screenshot canceled: %w", ctx.Err())
		case sig := <-ch:
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
			f, err := os.Open(path)
			if err != nil {
				return nil, fmt.Errorf("screen/portal: open %s: %w", path, err)
			}
			img, err := png.Decode(f)
			f.Close()
			os.Remove(path) //nolint:errcheck
			if err != nil {
				return nil, fmt.Errorf("screen/portal: decode PNG: %w", err)
			}
			if rect.Empty() {
				return img, nil
			}
			return cropImage(img, rect), nil
		case <-timer.C:
			return nil, fmt.Errorf("screen/portal: timed out waiting for screenshot (30s)")
		}
	}
}

func (b *PortalDBusBackend) Close() error {
	if b == nil || b.conn == nil {
		return nil
	}
	return b.conn.Close()
}
