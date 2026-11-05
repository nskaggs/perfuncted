//go:build linux

package window

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

type privateGNOMECore struct{}

func (privateGNOMECore) GetProtocolVersion() (uint32, *dbus.Error) {
	return gnomebridge.ProtocolVersion, nil
}
func (privateGNOMECore) GetExtensionVersion() (string, *dbus.Error) { return "1-test", nil }
func (privateGNOMECore) GetShellVersion() (string, *dbus.Error)     { return "51-test", nil }
func (privateGNOMECore) GetCapabilities() ([]string, *dbus.Error) {
	return []string{gnomebridge.CapabilityWindows}, nil
}

type privateGNOMEWindows struct{}

func (privateGNOMEWindows) ListWindows() ([]gnomebridge.WindowInfo, *dbus.Error) {
	return []gnomebridge.WindowInfo{{ID: "17", Title: "Private Editor", AppID: "org.test.Editor", Width: 600, Height: 400}}, nil
}
func (privateGNOMEWindows) GetActiveWindow() (gnomebridge.WindowInfo, *dbus.Error) {
	return gnomebridge.WindowInfo{ID: "17", Title: "Private Editor", Active: true}, nil
}
func (privateGNOMEWindows) GetWindow(id string) (gnomebridge.WindowInfo, *dbus.Error) {
	if id != "17" {
		return gnomebridge.WindowInfo{}, dbus.NewError("io.github.nskaggs.perfuncted.Gnome1.Error.NotFound", []any{"missing window"})
	}
	return gnomebridge.WindowInfo{ID: id, Title: "Private Editor"}, nil
}

func TestGnomeNativeManagerPrivateBusListingAndSubscription(t *testing.T) { //nolint:gocyclo // One isolated D-Bus lifecycle verifies setup, reads, events and shutdown.
	_, address, _ := startSyntheticGNOMEEvalBus(t)
	conn, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if reply, nameErr := conn.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue); nameErr != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim private bridge name: %v %v", reply, nameErr)
	}
	path := dbus.ObjectPath(gnomebridge.ObjectPath)
	if exportErr := conn.Export(privateGNOMECore{}, path, gnomebridge.CoreInterface); exportErr != nil {
		t.Fatal(exportErr)
	}
	if exportErr := conn.Export(privateGNOMEWindows{}, path, gnomebridge.WindowsInterface); exportErr != nil {
		t.Fatal(exportErr)
	}
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + address})
	startup, stop := context.WithTimeout(context.Background(), 2*time.Second)
	manager, err := NewGnomeNativeManagerForRuntimeContext(startup, rt)
	stop() // Startup context must not determine subscription lifetime.
	if err != nil {
		t.Fatalf("GNOME manager denied valid bridge: %v", err)
	}
	defer manager.Close()
	list, err := manager.List(context.Background())
	if err != nil || len(list) != 1 || list[0].ID != 17 || list[0].Title != "Private Editor" {
		t.Fatalf("list %+v %v", list, err)
	}
	count := 0
	for win, err := range manager.IterateWindows(context.Background()) {
		if err != nil || win.ID != 17 {
			t.Fatalf("iterator %+v %v", win, err)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("iterator count %d", count)
	}
	title, err := manager.ActiveTitle(context.Background())
	if err != nil || title != "Private Editor" {
		t.Fatalf("active title %q %v", title, err)
	}
	got, err := manager.InfoByID(context.Background(), "17")
	if err != nil || got.ID != 17 {
		t.Fatalf("window ID %+v %v", got, err)
	}
	if _, err := manager.InfoByID(context.Background(), "absent"); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("missing window: %v", err)
	}
	if err := conn.Emit(path, gnomebridge.WindowsInterface+".WindowRemoved", "17"); err != nil {
		t.Fatal(err)
	}
	select {
	case event, ok := <-manager.WindowEvents():
		if !ok || event.Kind != WindowRemovedEvent || event.ID != "17" {
			t.Fatalf("native event after canceled setup %+v available=%t", event, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("native event stream stopped with setup context")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := <-manager.WindowEvents(); ok {
		t.Fatal("event stream still open after close")
	}
	if windows, err := manager.List(context.Background()); err == nil || len(windows) != 0 || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed listing %+v %v", windows, err)
	}
}
