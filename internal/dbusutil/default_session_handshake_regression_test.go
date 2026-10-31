//go:build linux

package dbusutil

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultSessionBusConnectionAuthenticatesAndRemainsPrivate(t *testing.T) {
	address := startSyntheticDefaultBus(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var first, second error
	var available bool
	for ctx.Err() == nil {
		conn, err := SessionBusAddressContext(ctx, "")
		if err == nil {
			available, first = HasServiceContext(ctx, conn, "org.freedesktop.DBus")
			second = conn.Close()
			break
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if first != nil || second != nil || !available {
		t.Fatalf("private default bus could not make authenticated call: available=%v, call=%v, close=%v", available, first, second)
	}
	firstConn, err := SessionBusAddressContext(ctx, "")
	if err != nil {
		t.Fatalf("reopening private session connection: %v", err)
	}
	secondConn, err := SessionBusAddressContext(ctx, "")
	if err != nil {
		_ = firstConn.Close()
		t.Fatalf("opening independent session connection: %v", err)
	}
	if firstConn == secondConn {
		t.Fatal("private session connections share a client pointer")
	}
	if closeErr := firstConn.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	available, err = HasServiceContext(ctx, secondConn, "org.freedesktop.DBus")
	if err != nil || !available {
		t.Fatalf("closing independent connection disrupted peer: available=%v err=%v", available, err)
	}
	if closeErr := secondConn.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
}

func startSyntheticDefaultBus(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("synthetic D-Bus daemon unavailable")
	}
	address := "unix:path=" + filepath.Join(t.TempDir(), "session-bus")
	process := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+address)
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	return address
}
