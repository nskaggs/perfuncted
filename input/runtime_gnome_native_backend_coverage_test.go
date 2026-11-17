//go:build linux

package input

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

type runtimeGnomeInputBridge struct {
	mu     sync.Mutex
	events []string
}

func (*runtimeGnomeInputBridge) GetProtocolVersion() (uint32, *dbus.Error) {
	return gnomebridge.ProtocolVersion, nil
}

func (*runtimeGnomeInputBridge) GetExtensionVersion() (string, *dbus.Error) {
	return gnomebridge.ExtensionVersion, nil
}

func (*runtimeGnomeInputBridge) GetShellVersion() (string, *dbus.Error) {
	return "51.0", nil
}

func (*runtimeGnomeInputBridge) GetCapabilities() ([]string, *dbus.Error) {
	return []string{gnomebridge.CapabilityInput}, nil
}

func (b *runtimeGnomeInputBridge) Key(keyval uint32, pressed bool) *dbus.Error {
	b.mu.Lock()
	b.events = append(b.events, fmt.Sprintf("key:%x:%t", keyval, pressed))
	b.mu.Unlock()
	return nil
}

func (b *runtimeGnomeInputBridge) Text(text string) *dbus.Error {
	b.mu.Lock()
	b.events = append(b.events, "text:"+text)
	b.mu.Unlock()
	return nil
}

func TestInputRuntimeSelectsCurrentGnomeNativeBackend(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	service := &runtimeGnomeInputBridge{}
	reply, err := portal.conn.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim GNOME bridge name: reply=%v err=%v", reply, err)
	}
	if err := portal.conn.Export(service, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.CoreInterface); err != nil {
		t.Fatalf("export GNOME input bridge core: %v", err)
	}
	if err := portal.conn.Export(service, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.InputInterface); err != nil {
		t.Fatalf("export GNOME input bridge operations: %v", err)
	}
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-missing",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
	})

	backend, err := OpenRuntimeContext(context.Background(), runtime, 1920, 1080)
	if err != nil {
		t.Fatalf("open GNOME input runtime with current bridge: %v", err)
	}
	native, ok := backend.(*GnomeNativeBackend)
	if !ok {
		_ = backend.Close()
		t.Fatalf("GNOME runtime selected %T, want native input bridge", backend)
	}
	ctx := context.Background()
	if err := native.KeyDown(ctx, "ctrl"); err != nil {
		t.Fatalf("send modifier press through selected bridge: %v", err)
	}
	if err := native.TypeLiteral(ctx, "hello"); err != nil {
		t.Fatalf("send text through selected bridge: %v", err)
	}
	if err := native.KeyUp(ctx, "ctrl"); err != nil {
		t.Fatalf("send modifier release through selected bridge: %v", err)
	}
	service.mu.Lock()
	events := slices.Clone(service.events)
	service.mu.Unlock()
	if want := []string{"key:ffe3:true", "text:hello", "key:ffe3:false"}; !slices.Equal(events, want) {
		t.Fatalf("GNOME bridge events %v, want %v", events, want)
	}
	if err := native.Close(); err != nil {
		t.Fatalf("close GNOME native input backend: %v", err)
	}

	var available, selected bool
	for _, result := range ProbeRuntime(runtime) {
		if result.Name == "gnome-native" {
			available, selected = result.Available, result.Selected
		}
	}
	if !available || !selected {
		t.Fatalf("GNOME capability probe available=%t selected=%t", available, selected)
	}
}
