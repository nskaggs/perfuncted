//go:build linux

package clipboard

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"github.com/nskaggs/perfuncted/internal/testutil"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
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

type clipboardPrivateCoreWithoutClipboard struct{ clipboardPrivateCore }

func (clipboardPrivateCoreWithoutClipboard) GetCapabilities() ([]string, *dbus.Error) {
	return nil, nil
}

type clipboardPrivateCoreOutdated struct{ clipboardPrivateCore }

func (clipboardPrivateCoreOutdated) GetExtensionVersion() (string, *dbus.Error) {
	return "0", nil
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
	address := "unix:path=" + testutil.ShortSocketPath(t, "bus")
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

	// The public runtime selector must choose this authorized GNOME bridge
	// over subprocess clipboard tools, using only our synthetic private bus.
	gnomeRuntime := env.FromEnviron([]string{
		"DBUS_SESSION_BUS_ADDRESS=" + address,
		"WAYLAND_DISPLAY=nonexistent-private-test-socket",
		"XDG_RUNTIME_DIR=" + testutil.ShortSocketDir(t),
		"XDG_CURRENT_DESKTOP=GNOME",
	})
	selected, err := OpenRuntimeContext(ctx, gnomeRuntime)
	if err != nil {
		t.Fatalf("GNOME runtime selection did not use the private bridge: %v", err)
	}
	defer selected.Close()
	if _, ok := selected.(*GnomeNativeClipboard); !ok {
		t.Fatalf("GNOME runtime selected %T instead of the native bridge", selected)
	}
	const selection = "private runtime-selected clipboard π"
	if err := selected.Set(ctx, selection); err != nil {
		t.Fatalf("runtime-selected clipboard set: %v", err)
	}
	if got, err := selected.Get(ctx); err != nil || got != selection {
		t.Fatalf("runtime-selected clipboard round-trip %q: %v", got, err)
	}
}

func TestClipboardFallsBackWhenGNOMEBridgeLacksClipboardCapability(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon unavailable")
	}
	dir := testutil.ShortSocketDir(t)
	address := "unix:path=" + filepath.Join(dir, "bus")
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
	if err := server.Export(clipboardPrivateCoreWithoutClipboard{}, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.CoreInterface); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "xclip"), []byte("#!/bin/sh\nexit 77\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	runtime := env.FromEnviron([]string{
		"DBUS_SESSION_BUS_ADDRESS=" + address,
		"WAYLAND_DISPLAY=missing-private-wayland-socket",
		"XDG_RUNTIME_DIR=" + testutil.ShortSocketDir(t),
		"XDG_CURRENT_DESKTOP=GNOME",
		"DISPLAY=:private-test",
	})
	selected, err := OpenRuntimeContext(ctx, runtime)
	if err != nil {
		t.Fatalf("fallback after missing GNOME clipboard capability: %v", err)
	}
	defer selected.Close()
	command, ok := selected.(*extCmdClipboard)
	if !ok || len(command.getCmd) == 0 || command.getCmd[0] != "xclip" {
		t.Fatalf("missing GNOME capability selected %T, want xclip fallback", selected)
	}
}

func TestClipboardRequiresGNOMESessionRestartAfterBridgeUpgrade(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon unavailable")
	}
	dir := testutil.ShortSocketDir(t)
	address := "unix:path=" + filepath.Join(dir, "bus")
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
	if err := server.Export(clipboardPrivateCoreOutdated{}, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.CoreInterface); err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	gsettings := "#!/bin/sh\nif [ \"$1\" = get ]; then\n  case \"$3\" in\n    allow-extension-installation) echo true ;;\n    disable-user-extensions|disabled-extensions|enabled-extensions) echo '[]' ;;\n    *) exit 1 ;;\n  esac\nfi\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "gsettings"), []byte(gsettings), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	runtime := env.FromEnviron([]string{
		"DBUS_SESSION_BUS_ADDRESS=" + address,
		"WAYLAND_DISPLAY=missing-private-wayland-socket",
		"XDG_RUNTIME_DIR=" + testutil.ShortSocketDir(t),
		"XDG_CURRENT_DESKTOP=GNOME",
		"XDG_DATA_HOME=" + t.TempDir(),
		"DISPLAY=:private-test",
		"PATH=" + bin,
	})
	selected, err := OpenRuntimeContext(ctx, runtime)
	if selected != nil || !errors.Is(err, gnomebridge.ErrSessionRestartRequired) {
		if selected != nil {
			_ = selected.Close()
		}
		t.Fatalf("outdated GNOME bridge returned backend=%T err=%v, want session restart", selected, err)
	}
}

func TestGnomeNativeUninitializedBoundary(t *testing.T) {
	for _, cb := range []*GnomeNativeClipboard{nil, {}} {
		if text, err := cb.Get(context.Background()); err == nil || text != "" || !strings.Contains(err.Error(), "not initialised") {
			t.Fatalf("Get = %q, %v", text, err)
		}
		if err := cb.Set(context.Background(), "private clipboard text"); err == nil || !strings.Contains(err.Error(), "not initialised") {
			t.Fatalf("Set error = %v", err)
		}
		if err := cb.Close(); err != nil {
			t.Fatalf("Close = %v", err)
		}
	}
}

func TestClipboardCapabilitiesAreIsolated(t *testing.T) {
	native := (*GnomeNativeClipboard)(nil)
	cmd := (*extCmdClipboard)(nil)
	a, b := native.SupportedOperations(), cmd.SupportedOperations()
	if len(a) == 0 || !reflect.DeepEqual(a, b) {
		t.Fatalf("inconsistent operations: %v %v", a, b)
	}
	a[0] = "injected"
	if reflect.DeepEqual(a, native.SupportedOperations()) {
		t.Fatal("caller mutated global supported operations")
	}
}

func TestGnomeNativeCanceledConstructor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cb, err := NewGnomeNativeClipboardForRuntimeContext(ctx, env.FromEnviron(nil))
	if cb != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("constructor = %v, %v", cb, err)
	}
}
