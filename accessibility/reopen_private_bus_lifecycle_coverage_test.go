package accessibility

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
)

func TestATSPIReopenCreatesFreshBackendGenerationOnTargetBuses(t *testing.T) {
	sessionAddress := startTestDBus(t)
	accessibilityAddress := startTestDBus(t)
	sessionConn, err := dbusutil.SessionBusAddress(sessionAddress)
	if err != nil {
		t.Fatalf("connect private session bus: %v", err)
	}
	t.Cleanup(func() { _ = sessionConn.Close() })
	if err := sessionConn.Export(&testAccessibilityBusProvider{address: accessibilityAddress}, busPath, busService); err != nil {
		t.Fatalf("export accessibility bus provider: %v", err)
	}
	if reply, err := sessionConn.RequestName(busService, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim accessibility bus provider: reply=%d err=%v", reply, err)
	}

	runtime := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + sessionAddress})
	opened, err := OpenRuntimeContext(context.Background(), runtime)
	if err != nil {
		t.Fatalf("open initial accessibility backend: %v", err)
	}
	initial, ok := opened.(*dbusBackend)
	if !ok {
		t.Fatalf("initial backend type=%T, want *dbusBackend", opened)
	}
	t.Cleanup(func() { _ = initial.Close() })

	reopenedBackend, err := initial.Reopen(context.Background())
	if err != nil {
		t.Fatalf("reopen accessibility backend: %v", err)
	}
	reopened, ok := reopenedBackend.(*dbusBackend)
	if !ok {
		t.Fatalf("reopened backend type=%T, want *dbusBackend", reopenedBackend)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if reopened == initial || reopened.access == initial.access || reopened.session == initial.session {
		t.Fatal("reopen reused a prior backend transport")
	}
	if got, want := reopened.Generation(), initial.Generation()+1; got != want {
		t.Fatalf("reopened generation=%d, want %d", got, want)
	}
	if err := reopened.connected(); err != nil {
		t.Fatalf("fresh backend is disconnected: %v", err)
	}
	if err := initial.connected(); err != nil {
		t.Fatalf("reopen revoked the prior backend before its owner closed it: %v", err)
	}
}

func TestATSPIReopenConnectionFailureInvalidatesPriorBackend(t *testing.T) {
	missingBus := filepath.Join(t.TempDir(), "missing-bus")
	backend := &dbusBackend{
		runtime:    env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=" + missingBus}),
		generation: 12,
	}
	if _, err := backend.Reopen(context.Background()); err == nil {
		t.Fatal("reopen accepted an unavailable target session bus")
	}
	if !errors.Is(backend.connected(), ErrDisconnected) {
		t.Fatalf("backend after failed transport reopen = %v, want disconnected", backend.connected())
	}
	if got := backend.Generation(); got != 13 {
		t.Fatalf("generation after failed transport reopen=%d, want 13", got)
	}
}
