//go:build linux

package gnomebridge

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

func startPrivateBridgeBus(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bus")
	address := "unix:path=" + path
	var stderr bytes.Buffer
	cmd := exec.Command("dbus-daemon", "--session", "--address="+address, "--nofork", "--nopidfile")
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Skipf("private dbus daemon unavailable: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.After(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return address
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-deadline:
			t.Fatalf("private D-Bus startup: %s", stderr.String())
		}
	}
}

type syntheticGNOMECore struct {
	protocol uint32
	caps     []string
}

func (s *syntheticGNOMECore) GetProtocolVersion() (uint32, *dbus.Error) { return s.protocol, nil }
func (*syntheticGNOMECore) GetExtensionVersion() (string, *dbus.Error)  { return "1.42-synthetic", nil }
func (*syntheticGNOMECore) GetShellVersion() (string, *dbus.Error)      { return "51-synthetic", nil }
func (s *syntheticGNOMECore) GetCapabilities() ([]string, *dbus.Error) {
	return append([]string(nil), s.caps...), nil
}
func (*syntheticGNOMECore) Ping() *dbus.Error { return nil }

type syntheticGNOMEWindows struct{}

func (*syntheticGNOMEWindows) ListWindows() ([]WindowInfo, *dbus.Error) {
	return []WindowInfo{{ID: "7", Title: "Synthetic Editor", AppID: "org.example.Editor", PID: 117, Width: 800, Height: 600}}, nil
}
func (*syntheticGNOMEWindows) GetWindow(id string) (WindowInfo, *dbus.Error) {
	if id != "7" {
		return WindowInfo{}, dbus.NewError("io.github.nskaggs.perfuncted.Gnome1.Error.NotFound", []any{"synthetic missing window"})
	}
	return WindowInfo{ID: id, Title: "Synthetic Editor", Width: 800, Height: 600}, nil
}
func (*syntheticGNOMEWindows) GetActiveWindow() (WindowInfo, *dbus.Error) {
	return WindowInfo{ID: "7", Active: true}, nil
}
func (*syntheticGNOMEWindows) Move(id string, x, y int32) *dbus.Error {
	if id != "7" {
		return dbus.NewError("io.github.nskaggs.perfuncted.Gnome1.Error.NotFound", []any{"synthetic missing window"})
	}
	return nil
}

type syntheticGNOMEClipboard struct {
	mu   sync.Mutex
	text string
}

func (s *syntheticGNOMEClipboard) GetText() (string, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.text, nil
}
func (s *syntheticGNOMEClipboard) SetText(value string) *dbus.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.text = value
	return nil
}
func publishSyntheticGNOME(t *testing.T, address string, protocol uint32) *dbus.Conn {
	t.Helper()
	conn, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	reply, err := conn.RequestName(BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim private GNOME service: %v %v", reply, err)
	}
	path := dbus.ObjectPath(ObjectPath)
	for _, tc := range []struct {
		iface string
		impl  any
	}{
		{CoreInterface, &syntheticGNOMECore{protocol: protocol, caps: []string{CapabilityWindows, CapabilityClipboard}}},
		{WindowsInterface, &syntheticGNOMEWindows{}},
		{ClipboardInterface, &syntheticGNOMEClipboard{text: "initial-private-clipboard"}},
	} {
		if err := conn.Export(tc.impl, path, tc.iface); err != nil {
			t.Fatalf("export GNOME %s: %v", tc.iface, err)
		}
	}
	return conn
}
func TestGNOMEBridgePrivateDBusNegotiationWindowsClipboardAndTypedErrors(t *testing.T) {
	address := startPrivateBridgeBus(t)
	publishSyntheticGNOME(t, address, ProtocolVersion)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := NewClientForBus(ctx, address)
	if err != nil {
		t.Fatalf("private GNOME service refused: %v", err)
	}
	if client.ProtocolVersion() != ProtocolVersion || client.ExtensionVersion() != "1.42-synthetic" || client.ShellVersion() != "51-synthetic" {
		t.Fatalf("protocol negotiation lost versions: %v %q %q", client.ProtocolVersion(), client.ExtensionVersion(), client.ShellVersion())
	}
	if !client.HasCapability(CapabilityClipboard) || client.HasCapability(CapabilityScreen) {
		t.Fatalf("incorrect private capabilities %v", client.Capabilities())
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	windows, err := client.ListWindows(ctx)
	if err != nil || len(windows) != 1 || windows[0].ID != "7" || windows[0].PID != 117 || windows[0].Title != "Synthetic Editor" {
		t.Fatalf("typed GNOME window list %+v %v", windows, err)
	}
	win, err := client.GetWindow(ctx, "7")
	if err != nil || win.ID != "7" || win.Width != 800 {
		t.Fatalf("window lookup %+v %v", win, err)
	}
	active, err := client.GetActiveWindow(ctx)
	if err != nil || active.ID != "7" || !active.Active {
		t.Fatalf("active window %+v %v", active, err)
	}
	if err := client.Move(ctx, "7", 12, 34); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetWindow(ctx, "absent"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("GNOME D-Bus NotFound was not mapped: %v", err)
	}
	if err := client.Move(ctx, "absent", 12, 34); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("GNOME D-Bus mutator NotFound was not mapped: %v", err)
	}
	if content, err := client.GetText(ctx); err != nil || content != "initial-private-clipboard" {
		t.Fatalf("private clipboard read %q %v", content, err)
	}
	if err := client.SetText(ctx, "non-public synthetic string"); err != nil {
		t.Fatal(err)
	}
	if content, err := client.GetText(ctx); err != nil || content != "non-public synthetic string" {
		t.Fatalf("clipboard write/read %q %v", content, err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("closed bridge still connected: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("idempotent close failed: %v", err)
	}
}
func TestGNOMEBridgePrivateBusRejectsAbsentServiceAndProtocolMismatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		serve   bool
		version uint32
	}{
		{"missing service", false, 0}, {"incompatible extension", true, ProtocolVersion + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address := startPrivateBridgeBus(t)
			if tc.serve {
				publishSyntheticGNOME(t, address, tc.version)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			client, err := NewClientForBus(ctx, address)
			if client != nil || err == nil {
				t.Fatalf("invalid GNOME service accepted: %v %v", client, err)
			}
			if !tc.serve && !errors.Is(err, ErrUnavailable) {
				t.Fatalf("missing service: %v", err)
			}
			if tc.serve {
				var mismatch *ProtocolError
				if !errors.As(err, &mismatch) {
					t.Fatalf("protocol mismatch not preserved: %v", err)
				}
			}
		})
	}
}
func TestGNOMEBridgePrivateBusMissingMethodFailsWithoutDefaultingValues(t *testing.T) {
	address := startPrivateBridgeBus(t)
	publishSyntheticGNOME(t, address, ProtocolVersion)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := NewClientForBus(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, _, err := client.PointerLocation(ctx); !errors.Is(err, ErrUnavailable) {
		var dbusError *dbus.Error
		errors.As(err, &dbusError)
		if dbusError != nil {
			t.Logf("missing-interface error name=%q body=%v", dbusError.Name, dbusError.Body)
		}
		t.Fatalf("missing GNOME input interface fabricated pointer: %T %v", err, err)
	}
	if _, err := client.CaptureFull(ctx, 4); err == nil || (!errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrUnixFDUnsupported)) {
		t.Fatalf("missing GNOME screen interface returned success: %v", err)
	}
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("missing optional interface corrupted core: %v", err)
	}
}
