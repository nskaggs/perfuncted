package accessibility

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRuntimeRejectsInvalidTargetAccessibilityRoutes(t *testing.T) {
	sessionAddress := startTestDBus(t)
	session, err := dbusutil.SessionBusAddress(sessionAddress)
	if err != nil {
		t.Fatalf("connect private target session bus: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	provider := &testAccessibilityBusProvider{}
	if err := session.Export(provider, busPath, busService); err != nil {
		t.Fatalf("export private accessibility bus provider: %v", err)
	}
	if reply, err := session.RequestName(busService, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim accessibility bus service: reply=%v err=%v", reply, err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		rt   env.Runtime
		want error
	}{
		{name: "nil context", rt: env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress})},
		{name: "canceled context", ctx: canceled, rt: env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress}), want: context.Canceled},
		{name: "missing target session bus", ctx: context.Background(), rt: env.FromEnviron([]string{})},
		{name: "malformed target session bus", ctx: context.Background(), rt: env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=not-a-dbus-address"})},
		{name: "malformed accessibility override", ctx: context.Background(), rt: env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress, "ATSPI_BUS_ADDRESS=not-a-dbus-address"})},
		{name: "empty accessibility address", ctx: context.Background(), rt: env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, err := OpenRuntimeContext(tc.ctx, tc.rt)
			if backend != nil {
				_ = backend.Close()
				t.Fatalf("invalid target route returned backend %T", backend)
			}
			if err == nil || tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("OpenRuntimeContext error = %v, want an error matching %v", err, tc.want)
			}
		})
	}
}

func TestToolkitIdentityCacheFollowsCurrentBackendGeneration(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackend(t, false)
	ctx := context.Background()
	if _, err := backend.toolkitForNode(nil, id); err == nil {
		t.Fatal("nil toolkit lookup context was accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := backend.toolkitForNode(canceled, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled toolkit lookup = %v", err)
	}

	toolkit, err := backend.toolkitForNode(ctx, id)
	if err != nil || toolkit != "GTK" {
		t.Fatalf("toolkit lookup = %q, %v", toolkit, err)
	}
	if toolkit, err := backend.toolkitForNode(ctx, id); err != nil || toolkit != "GTK" {
		t.Fatalf("cached toolkit lookup = %q, %v", toolkit, err)
	}
	backend.mu.RLock()
	_, cached := backend.toolkits[id.BusName]
	backend.mu.RUnlock()
	if !cached {
		t.Fatal("successful toolkit lookup was not cached")
	}

	backend.Invalidate(id)
	if _, err := backend.toolkitForNode(ctx, id); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("stale toolkit handle error = %v", err)
	}
	current := backend.refID(objectRef{BusName: registryName, ObjectPath: dbus.ObjectPath(id.ObjectPath)})
	toolkit, err = backend.toolkitForNode(ctx, current)
	if err != nil || toolkit != "GTK" || current.Generation <= id.Generation {
		t.Fatalf("toolkit after generation change = %q, handle=%+v err=%v", toolkit, current, err)
	}
}
