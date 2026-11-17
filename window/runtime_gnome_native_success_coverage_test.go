//go:build linux

package window

import (
	"context"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

type privateGNOMEMultipleWindows struct{}

func (privateGNOMEMultipleWindows) ListWindows() ([]gnomebridge.WindowInfo, *dbus.Error) {
	return []gnomebridge.WindowInfo{
		{ID: "17", Title: "Private Editor", AppID: "org.test.Editor", Width: 600, Height: 400},
		{ID: "18", Title: "Private Terminal", AppID: "org.test.Terminal", Width: 800, Height: 600},
	}, nil
}

func (privateGNOMEMultipleWindows) GetActiveWindow() (gnomebridge.WindowInfo, *dbus.Error) {
	return gnomebridge.WindowInfo{ID: "17", Title: "Private Editor", Active: true}, nil
}

func (privateGNOMEMultipleWindows) GetWindow(id string) (gnomebridge.WindowInfo, *dbus.Error) {
	return gnomebridge.WindowInfo{ID: id, Title: "Private Editor"}, nil
}

func TestOpenRuntimeSelectsGNOMENativeManagerAndIteratorHonorsStop(t *testing.T) {
	_, address, _ := startSyntheticGNOMEEvalBus(t)
	conn, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if reply, nameErr := conn.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue); nameErr != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim private bridge name: %v %v", nameErr, reply)
	}
	path := dbus.ObjectPath(gnomebridge.ObjectPath)
	if err := conn.Export(privateGNOMECore{}, path, gnomebridge.CoreInterface); err != nil {
		t.Fatal(err)
	}
	if err := conn.Export(privateGNOMEMultipleWindows{}, path, gnomebridge.WindowsInterface); err != nil {
		t.Fatal(err)
	}

	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/nonexistent/pf-window-runtime.sock",
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=" + address,
	})
	manager, err := OpenRuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatalf("open GNOME runtime through its private native bridge: %v", err)
	}
	defer manager.Close()
	if _, ok := manager.(*GnomeNativeManager); !ok {
		t.Fatalf("GNOME runtime selected %T, want native manager", manager)
	}

	visited := 0
	for info, err := range manager.IterateWindows(context.Background()) {
		if err != nil {
			t.Fatalf("iterate native GNOME windows: %v", err)
		}
		if info.ID != 17 || info.Title != "Private Editor" {
			t.Fatalf("first native GNOME window = %+v", info)
		}
		visited++
		break
	}
	if visited != 1 {
		t.Fatalf("iterator yielded %d windows after caller stopped, want 1", visited)
	}
}
