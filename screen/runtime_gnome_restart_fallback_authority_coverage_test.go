package screen

import (
	"bufio"
	"bytes"
	"context"
	"errors"
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

type outdatedGnomeScreenBridge struct{}

func (outdatedGnomeScreenBridge) GetProtocolVersion() (uint32, *dbus.Error) {
	return gnomebridge.ProtocolVersion, nil
}

func (outdatedGnomeScreenBridge) GetExtensionVersion() (string, *dbus.Error) {
	return "0", nil
}

func (outdatedGnomeScreenBridge) GetShellVersion() (string, *dbus.Error) {
	return "51.0", nil
}

func (outdatedGnomeScreenBridge) GetCapabilities() ([]string, *dbus.Error) {
	return []string{gnomebridge.CapabilityScreen}, nil
}

func startPrivateOutdatedGnomeBridge(t *testing.T) string {
	t.Helper()
	dbusDaemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skipf("private D-Bus server unavailable: %v", err)
	}
	cmd := exec.Command(dbusDaemon, "--session", "--nofork", "--nopidfile", "--print-address=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("open private bus address pipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start private D-Bus server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	addressLine, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read private D-Bus address: %v (%s)", err, strings.TrimSpace(stderr.String()))
	}
	address := strings.TrimSpace(addressLine)
	service, err := dbusutil.SessionBusAddressContext(context.Background(), address)
	if err != nil {
		t.Fatalf("connect private D-Bus service: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim GNOME bridge name: reply=%v err=%v", reply, err)
	}
	if err := service.Export(outdatedGnomeScreenBridge{}, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.CoreInterface); err != nil {
		t.Fatalf("export outdated GNOME bridge methods: %v", err)
	}
	return address
}

func TestGNOMEScreenRuntimeReturnsRestartRequiredInsteadOfFallingBack(t *testing.T) {
	address := startPrivateOutdatedGnomeBridge(t)
	bin := t.TempDir()
	oldPath := os.Getenv("PATH")
	path := bin + string(os.PathListSeparator) + oldPath
	t.Setenv("PATH", path)
	settingsLog := filepath.Join(t.TempDir(), "gsettings.log")
	settings := "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$*\" >> \"$PF_TEST_GSETTINGS_LOG\"\n" +
		"case \"$*\" in\n" +
		"  'get org.gnome.shell allow-extension-installation') printf 'true\\n' ;;\n" +
		"  'get org.gnome.shell disable-user-extensions') printf 'false\\n' ;;\n" +
		"  'get org.gnome.shell disabled-extensions') printf '[]\\n' ;;\n" +
		"  'get org.gnome.shell enabled-extensions') printf '[]\\n' ;;\n" +
		"  'set org.gnome.shell enabled-extensions '*) exit 0 ;;\n" +
		"  *) exit 91 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "gsettings"), []byte(settings), 0o700); err != nil {
		t.Fatalf("write isolated gsettings helper: %v", err)
	}
	t.Setenv("PF_TEST_GSETTINGS_LOG", settingsLog)

	dataHome := t.TempDir()
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-not-present",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=" + address,
		"XDG_DATA_HOME=" + dataHome,
		"PATH=" + path,
		"PF_TEST_GSETTINGS_LOG=" + settingsLog,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	backend, err := OpenRuntimeContext(ctx, runtime)
	if backend != nil {
		_ = backend.Close()
	}
	log, logErr := os.ReadFile(settingsLog)
	if backend != nil || !errors.Is(err, gnomebridge.ErrSessionRestartRequired) {
		t.Fatalf("GNOME restart requirement was replaced by fallback: backend=%T err=%v gsettings=%q logErr=%v", backend, err, log, logErr)
	}
	if !strings.Contains(err.Error(), "log out and back in once") {
		t.Fatalf("restart error omitted its actionable instruction: %v", err)
	}
	if logErr != nil {
		t.Fatalf("read isolated settings calls: %v", logErr)
	}
	wantLog := "get org.gnome.shell allow-extension-installation\n" +
		"get org.gnome.shell disable-user-extensions\n" +
		"get org.gnome.shell disabled-extensions\n" +
		"get org.gnome.shell enabled-extensions\n" +
		"set org.gnome.shell enabled-extensions ['perfuncted@nskaggs.github.io']\n"
	if string(log) != wantLog {
		t.Fatalf("extension setup used unexpected settings operations: %q", log)
	}
	extensionPath := filepath.Join(dataHome, "gnome-shell", "extensions", "perfuncted@nskaggs.github.io")
	if info, err := os.Stat(extensionPath); err != nil || !info.IsDir() {
		t.Fatalf("private test extension was not installed before restart was requested: stat=%v err=%v", info, err)
	}
}
