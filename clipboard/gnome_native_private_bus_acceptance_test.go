//go:build linux

package clipboard

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

type clipboardPrivateCore struct{}

func (clipboardPrivateCore) GetProtocolVersion() (uint32, *dbus.Error) {
	return gnomebridge.ProtocolVersion, nil
}
func (clipboardPrivateCore) GetExtensionVersion() (string, *dbus.Error) { return "1", nil }
func (clipboardPrivateCore) GetShellVersion() (string, *dbus.Error)     { return "51", nil }
func (clipboardPrivateCore) GetCapabilities() ([]string, *dbus.Error) {
	return []string{gnomebridge.CapabilityClipboard}, nil
}

type clipboardPrivateProvider struct {
	mu   sync.Mutex
	text string
}

func (p *clipboardPrivateProvider) GetText() (string, *dbus.Error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.text, nil
}
func (p *clipboardPrivateProvider) SetText(text string) *dbus.Error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.text = text
	return nil
}

func TestGnomeNativeClipboardRealPrivateBusNegotiatesAndRoundTripsUTF8(t *testing.T) { //nolint:gocyclo // Isolated bus lifecycle and clipboard text contract share the same fixture.
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon unavailable")
	}
	address := "unix:path=" + filepath.Join(t.TempDir(), "bus")
	daemon := exec.Command("dbus-daemon", "--session", "--address="+address, "--nofork", "--nopidfile")
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var server *dbus.Conn
	var err error
	for ctx.Err() == nil {
		server, err = dbusutil.ConnectContext(ctx, address)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("private bus startup: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	reply, nameErr := server.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue)
	if nameErr != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("private service name: %v %v", reply, nameErr)
	}
	path := dbus.ObjectPath(gnomebridge.ObjectPath)
	if exportErr := server.Export(clipboardPrivateCore{}, path, gnomebridge.CoreInterface); exportErr != nil {
		t.Fatal(exportErr)
	}
	provider := &clipboardPrivateProvider{text: "initial"}
	if exportErr := server.Export(provider, path, gnomebridge.ClipboardInterface); exportErr != nil {
		t.Fatal(exportErr)
	}
	runtime := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + address})
	native, err := NewGnomeNativeClipboardForRuntimeContext(ctx, runtime)
	if err != nil {
		t.Fatalf("private clipboard setup: %v", err)
	}
	defer native.Close()
	got, err := native.Get(ctx)
	if err != nil || got != "initial" {
		t.Fatalf("initial clipboard text %q err %v", got, err)
	}
	if setErr := native.Set(ctx, "invalid\x00clipboard"); setErr == nil || !strings.Contains(setErr.Error(), "null char") {
		t.Fatalf("D-Bus accepted an invalid embedded NUL: %v", setErr)
	}
	provider.mu.Lock()
	unchanged := provider.text
	provider.mu.Unlock()
	if unchanged != "initial" {
		t.Fatalf("rejected text changed clipboard: %q", unchanged)
	}
	value := "hello π 🌍\nsecond line"
	if setErr := native.Set(ctx, value); setErr != nil {
		t.Fatalf("set unicode/multiline text: %v", setErr)
	}
	got, err = native.Get(ctx)
	if err != nil || got != value {
		t.Fatalf("round-trip text %q err %v", got, err)
	}
	provider.mu.Lock()
	stored := provider.text
	provider.mu.Unlock()
	if stored != value {
		t.Fatalf("GNOME provider received altered text: %q", stored)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if got, err := native.Get(canceled); !errors.Is(err, context.Canceled) || got != "" {
		t.Fatalf("canceled get disclosed text %q err %v", got, err)
	}
	if err := native.Set(canceled, "must-not-apply"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled set accepted: %v", err)
	}
	if err := native.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Get(ctx); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed clipboard accepted read: %v", err)
	}
}
