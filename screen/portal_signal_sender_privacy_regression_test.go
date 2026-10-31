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
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

type portalAttackObservable struct{ requests chan dbus.ObjectPath }

func (p *portalAttackObservable) Screenshot(_ string, opts map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	token, _ := opts["handle_token"].Value().(string)
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/fixture/" + token)
	select {
	case p.requests <- path:
	default:
	}
	return path, nil
}

// godbus delivers the union of match rules to all local signal channels.
// A second subscription must not allow another bus peer or a different
// method member to impersonate the authoritative screenshot grant.
func TestScreenshotPortalRejectsForgedResponseAndUnrelatedSignalMember(t *testing.T) {
	for _, tc := range []struct {
		name     string
		attacker bool
		member   string
		code     uint32
	}{
		{"forged authorization", true, "Response", 0},
		{"forged denial", true, "Response", 1},
		{"unrelated portal member", false, "Unexpected", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, portal := newSyntheticScreenshotPortal(t)
			provider := &portalAttackObservable{requests: make(chan dbus.ObjectPath, 1)}
			if err := portal.conn.Export(provider, portalPath, portalSsIf); err != nil {
				t.Fatal(err)
			}
			attacker, err := dbusutil.ConnectContext(context.Background(), portal.busAddress)
			if err != nil {
				t.Fatal(err)
			}
			defer attacker.Close()
			imgPath := filepath.Join(t.TempDir(), "private-fake-grant.png")
			file, err := os.Create(imgPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
				file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			wide := []dbus.MatchOption{dbus.WithMatchInterface(portalReqIf)}
			if err := b.conn.AddMatchSignalContext(context.Background(), wide...); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = b.conn.RemoveMatchSignalContext(context.Background(), wide...) }()
			ctx, cancel := context.WithTimeout(context.Background(), 160*time.Millisecond)
			defer cancel()
			done := make(chan struct {
				image image.Image
				err   error
			}, 1)
			go func() {
				captured, err := b.Grab(ctx, image.Rectangle{})
				done <- struct {
					image image.Image
					err   error
				}{captured, err}
			}()
			var path dbus.ObjectPath
			select {
			case path = <-provider.requests:
			case <-ctx.Done():
				t.Fatalf("portal screenshot did not reach synthetic service: %v", ctx.Err())
			}
			service := portal.conn
			if tc.attacker {
				service = attacker
			}
			results := map[string]dbus.Variant{"uri": dbus.MakeVariant("file://" + imgPath)}
			if err := service.Emit(path, portalReqIf+"."+tc.member, tc.code, results); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.err == nil || result.image != nil {
					t.Fatalf("unauthorized or unrelated screenshot response returned image: %v %v", result.image, result.err)
				}
				if !errors.Is(result.err, context.DeadlineExceeded) && !strings.Contains(result.err.Error(), "canceled") {
					t.Fatalf("untrusted response was treated as authoritative error: %v", result.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("screenshot request hung instead of honoring canceled authorization")
			}
		})
	}
}
