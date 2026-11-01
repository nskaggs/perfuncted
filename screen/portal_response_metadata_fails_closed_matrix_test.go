//go:build linux

package screen

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// This private D-Bus service can send structurally malformed and omitted
// response fields, unlike the normal synthetic service which always adds a
// file URI. The test runs the real portal admission and signal routing path.
type portalResponseMetadataFixture struct {
	conn        *dbus.Conn
	code        any
	results     any
	blankHandle bool
}

func (f *portalResponseMetadataFixture) Screenshot(_ string, opts map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	token, _ := opts["handle_token"].Value().(string)
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/fixture/" + token)
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = f.conn.Emit(path, portalReqIf+".Response", f.code, f.results)
	}()
	if f.blankHandle {
		return "", nil
	}
	return path, nil
}

func TestScreenshotPortalResponseMetadataMatrix(t *testing.T) {
	for _, tc := range []struct {
		name        string
		code        any
		results     func(string) any
		blankHandle bool
		want        string
		successful  bool
	}{
		{"valid portal result", uint32(0), func(path string) any { return map[string]dbus.Variant{"uri": dbus.MakeVariant("file://" + path)} }, false, "", true},
		// A blank object path is invalid on the D-Bus wire and must be refused.
		{"invalid empty request handle", uint32(0), func(path string) any { return map[string]dbus.Variant{"uri": dbus.MakeVariant("file://" + path)} }, true, "invalid message", false},
		{"wrong result type", uint32(0), func(string) any { return "not a variant map" }, false, "no URI", false},
		{"missing URI", uint32(0), func(string) any { return map[string]dbus.Variant{} }, false, "no URI", false},
		{"wrong URI variant type", uint32(0), func(string) any { return map[string]dbus.Variant{"uri": dbus.MakeVariant(true)} }, false, "unsupported URI scheme", false},
		{"not authorized", uint32(2), func(path string) any { return map[string]dbus.Variant{"uri": dbus.MakeVariant("file://" + path)} }, false, "denied", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, base := newSyntheticScreenshotPortal(t)
			path := filepath.Join(t.TempDir(), "portal-result.png")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
				_ = f.Close()
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			fixture := &portalResponseMetadataFixture{conn: base.conn, code: tc.code, results: tc.results(path), blankHandle: tc.blankHandle}
			if err := base.conn.Export(fixture, portalPath, portalSsIf); err != nil {
				t.Fatal(err)
			}
			got, err := backend.Grab(context.Background(), image.Rectangle{})
			if tc.successful {
				if err != nil || got == nil || got.Bounds() != image.Rect(0, 0, 4, 3) {
					t.Fatalf("valid fallback response image=%v err=%v", got, err)
				}
			} else {
				if err == nil || got != nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("malformed response image=%v err=%v want=%q", got, err, tc.want)
				}
			}
			if len(backend.captureGate) != 0 {
				t.Fatal("portal response leaked screenshot gate")
			}
		})
	}
}

func TestScreenshotFileURIDecodingAndPathAuthorityMatrix(t *testing.T) {
	for _, tc := range []struct {
		uri, want string
		ok        bool
	}{
		{"file:///tmp/photo%20name.png", "/tmp/photo name.png", true},
		{"file://localhost/tmp/%C3%A9.png", "/tmp/é.png", true},
		{"file:///tmp/%%", "", false},
		{"file:///tmp/%", "", false},
		{"file:///tmp/%2", "", false},
		{"file:///tmp/%ff", "", false},
		{"file:///tmp/%00file.png", "", false},
		{"file://remotehost/tmp/photo.png", "", false},
		{"https:///tmp/photo.png", "", false},
	} {
		t.Run(tc.uri, func(t *testing.T) {
			got, err := fileURIPath(tc.uri)
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("URI=%q got=%q err=%v", tc.uri, got, err)
				}
			} else if err == nil || got != "" {
				t.Fatalf("invalid URI=%q accepted as %q err=%v", tc.uri, got, err)
			}
		})
	}
}
