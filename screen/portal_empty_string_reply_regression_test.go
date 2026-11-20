package screen

import (
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type emptyStringScreenshotPortal struct {
	conn *dbus.Conn
	code uint32
	uri  string
}

func (p *emptyStringScreenshotPortal) Screenshot(sender dbus.Sender, _ string, opts map[string]dbus.Variant) (string, *dbus.Error) {
	token, ok := opts["handle_token"].Value().(string)
	if !ok {
		return "", dbus.MakeFailedError(errors.New("missing screenshot handle token"))
	}
	path := portalRequestPath(string(sender), token)
	results := map[string]dbus.Variant{"uri": dbus.MakeVariant(p.uri)}
	if err := p.conn.Emit(path, portalReqIf+".Response", p.code, results); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return "", nil
}

func TestScreenshotPortalEmptyStringReplyUsesPredictedPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		code uint32
	}{
		{name: "authorized", code: 0},
		{name: "denied", code: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, portal := newSyntheticScreenshotPortal(t)
			path := filepath.Join(t.TempDir(), "capture.png")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			fixture := &emptyStringScreenshotPortal{conn: portal.conn, code: tc.code, uri: "file://" + path}
			if err := portal.conn.Export(fixture, portalPath, portalSsIf); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			got, err := backend.Grab(ctx, image.Rectangle{})
			if tc.code == 0 {
				if err != nil || got == nil || got.Bounds() != image.Rect(0, 0, 3, 2) {
					t.Fatalf("authorized screenshot = (%v, %v)", got, err)
				}
			} else if err == nil || got != nil || !strings.Contains(err.Error(), "denied") {
				t.Fatalf("denied screenshot = (%v, %v)", got, err)
			}
			if len(backend.captureGate) != 0 {
				t.Fatal("screenshot request retained its capture gate")
			}
		})
	}
}
