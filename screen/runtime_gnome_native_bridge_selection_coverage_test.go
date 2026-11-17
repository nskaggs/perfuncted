package screen

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

type currentGnomeScreenBridge struct{}

func (currentGnomeScreenBridge) GetProtocolVersion() (uint32, *dbus.Error) {
	return gnomebridge.ProtocolVersion, nil
}

func (currentGnomeScreenBridge) GetExtensionVersion() (string, *dbus.Error) {
	return gnomebridge.ExtensionVersion, nil
}

func (currentGnomeScreenBridge) GetShellVersion() (string, *dbus.Error) {
	return "51.0", nil
}

func (currentGnomeScreenBridge) GetCapabilities() ([]string, *dbus.Error) {
	return []string{gnomebridge.CapabilityScreen}, nil
}

func startCurrentGnomeScreenBridge(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skipf("private D-Bus server unavailable: %v", err)
	}
	dir, err := os.MkdirTemp("", "pfgn-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	address := "unix:path=" + filepath.Join(dir, "bus")
	command := exec.Command("dbus-daemon", "--session", "--nofork", "--nopidfile", "--address="+address, "--print-address=1")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read private D-Bus address: %v", err)
	}
	address = strings.TrimSpace(line)
	service, err := dbusutil.SessionBusAddressContext(context.Background(), address)
	if err != nil {
		t.Fatalf("connect private bridge service: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim GNOME bridge name: reply=%v err=%v", reply, err)
	}
	if err := service.Export(currentGnomeScreenBridge{}, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.CoreInterface); err != nil {
		t.Fatalf("export current GNOME screen bridge: %v", err)
	}
	return address, dir
}

func TestScreenRuntimeSelectsCurrentGnomeNativeBridge(t *testing.T) {
	address, runtimeDir := startCurrentGnomeScreenBridge(t)
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-missing",
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=" + address,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	backend, err := OpenRuntimeContext(ctx, runtime)
	if err != nil {
		t.Fatalf("open GNOME screen runtime with current bridge: %v", err)
	}
	native, ok := backend.(*GnomeNativeScreenBackend)
	if !ok {
		_ = backend.Close()
		t.Fatalf("GNOME runtime selected %T, want native bridge", backend)
	}
	if !native.CanonicalHashing() {
		t.Fatal("GNOME native backend did not advertise canonical hashes")
	}
	if err := native.Close(); err != nil {
		t.Fatalf("close GNOME native backend: %v", err)
	}
}
