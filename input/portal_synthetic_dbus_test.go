package input

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

type syntheticPortal struct {
	conn          *dbus.Conn
	mu            sync.Mutex
	code          uint32
	result        map[string]dbus.Variant
	skip          bool
	getVersion    any
	busAddress    string
	deviceMask    uint32
	selectCode    uint32
	startCode     uint32
	persistToken  string
	eisFD         dbus.UnixFD
	selectOptions map[string]dbus.Variant
	called        int
}

func (p *syntheticPortal) CreateSession(options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	token, _ := options["handle_token"].Value().(string)
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/fixture/" + token)
	p.mu.Lock()
	code, result, skip := p.code, p.result, p.skip
	p.called++
	p.mu.Unlock()
	if !skip {
		go func() {
			time.Sleep(10 * time.Millisecond)
			_ = p.conn.Emit(path, portalRequestInterface+".Response", code, result)
		}()
	}
	return path, nil
}

func (p *syntheticPortal) Get(iface, key string) (dbus.Variant, *dbus.Error) {
	if iface != remoteDesktopInterface || key != "version" {
		return dbus.Variant{}, dbus.MakeFailedError(errors.New("unknown property"))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return dbus.MakeVariant(p.getVersion), nil
}

func startSyntheticPortalBus(t *testing.T) (*dbus.Conn, *syntheticPortal) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon unavailable")
	}
	dir, err := os.MkdirTemp("", "pf-portal-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "bus")
	address := "unix:path=" + socket
	proc := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+address)
	if startErr := proc.Start(); startErr != nil {
		t.Fatal(startErr)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
	var service *dbus.Conn
	for i := 0; i < 100; i++ {
		service, err = dbusutil.SessionBusAddressContext(context.Background(), address)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("private dbus connection: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(remoteDesktopBus, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own portal service: %v %v", reply, err)
	}
	mock := &syntheticPortal{conn: service, busAddress: address, getVersion: uint32(2), result: map[string]dbus.Variant{"session_handle": dbus.MakeVariant("/org/freedesktop/portal/desktop/session/fixture")}}
	if exportErr := service.Export(mock, remoteDesktopPath, remoteDesktopInterface); exportErr != nil {
		t.Fatal(exportErr)
	}
	if exportErr := service.Export(mock, remoteDesktopPath, "org.freedesktop.DBus.Properties"); exportErr != nil {
		t.Fatal(exportErr)
	}
	if exportErr := service.Export(&syntheticPortalSession{}, dbus.ObjectPath(syntheticSessionPath), portalSessionInterface); exportErr != nil {
		t.Fatal(exportErr)
	}
	client, err := dbusutil.SessionBusAddressContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, mock
}

func TestSyntheticPortalVersionAndSessionAuthorization(t *testing.T) {
	client, portal := startSyntheticPortalBus(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := requireRemoteDesktopV2(ctx, client); err != nil {
		t.Fatalf("version 2 rejected: %v", err)
	}
	name, err := portalUniqueName(client.Names())
	if err != nil {
		t.Fatal(err)
	}
	path, err := portalCreateSession(ctx, client, name)
	if err != nil {
		t.Fatalf("authorized portal session: %v", err)
	}
	if path != "/org/freedesktop/portal/desktop/session/fixture" {
		t.Fatalf("session path %q", path)
	}
	portal.mu.Lock()
	calls := portal.called
	portal.getVersion = uint32(1)
	portal.mu.Unlock()
	if calls != 1 {
		t.Fatalf("create calls=%d", calls)
	}
	if err := requireRemoteDesktopV2(ctx, client); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Fatalf("outdated version accepted: %v", err)
	}
}

func TestSyntheticPortalRefusesDeniedOrMalformedAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   uint32
		result map[string]dbus.Variant
		want   error
	}{
		{"denied", 1, map[string]dbus.Variant{}, ErrRemoteDesktopDenied},
		{"missing session", 0, map[string]dbus.Variant{}, ErrRemoteDesktopUnavailable},
		{"malformed session", 0, map[string]dbus.Variant{"session_handle": dbus.MakeVariant(uint32(8))}, ErrRemoteDesktopUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, portal := startSyntheticPortalBus(t)
			portal.mu.Lock()
			portal.code = tc.code
			portal.result = tc.result
			portal.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			name, err := portalUniqueName(client.Names())
			if err != nil {
				t.Fatal(err)
			}
			_, err = portalCreateSession(ctx, client, name)
			if !errors.Is(err, tc.want) {
				t.Fatalf("authorization error = %v want %v", err, tc.want)
			}
		})
	}
}

func TestSyntheticPortalCanceledRequestDoesNotHang(t *testing.T) {
	client, portal := startSyntheticPortalBus(t)
	portal.mu.Lock()
	portal.skip = true
	portal.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	name, err := portalUniqueName(client.Names())
	if err != nil {
		t.Fatal(err)
	}
	_, err = portalCreateSession(ctx, client, name)
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(fmt.Sprint(err), "timed out") {
		t.Fatalf("unanswered portal request returned %v, want bounded timeout", err)
	}
}

func TestPortalRequestRejectsInvalidOptionsWithoutDBusRoundTrip(t *testing.T) {
	if _, err := portalRequest(context.Background(), nil, ":1.0", "CreateSession", nil); err == nil {
		t.Fatal("empty options accepted")
	}
	if _, err := portalRequest(context.Background(), nil, ":1.0", "CreateSession", []any{"bad"}); err == nil {
		t.Fatal("invalid options accepted")
	}
	for _, tc := range []struct {
		names []string
		valid bool
	}{
		{[]string{"org.freedesktop.portal.Desktop"}, false},
		{[]string{"org.freedesktop.portal.Desktop", ":1.25"}, true},
	} {
		got, err := portalUniqueName(tc.names)
		if tc.valid && (err != nil || got != ":1.25") {
			t.Fatalf("name %q error %v", got, err)
		}
		if !tc.valid && err == nil {
			t.Fatal("accepted non-unique D-Bus names")
		}
	}
}
