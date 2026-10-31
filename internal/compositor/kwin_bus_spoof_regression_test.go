//go:build linux

package compositor

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
)

func TestKWinDetectionRequiresExactBusServiceName(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("synthetic dbus-daemon unavailable")
	}
	address := "unix:path=" + filepath.Join(t.TempDir(), "bus")
	proc := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+address)
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var owner *dbus.Conn
	for ctx.Err() == nil {
		var err error
		owner, err = dbusutil.SessionBusAddressContext(ctx, address)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if owner == nil {
		t.Fatal("private D-Bus session not ready")
	}
	defer owner.Close()
	reply, err := owner.RequestName("org.kde.KWinSpoof", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register impostor service: %v %v", reply, err)
	}
	if kwinOnBus(address) {
		t.Fatal("KWin detection accepted unrelated bus name with KWin prefix")
	}
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=perfuncted-missing-socket", "DBUS_SESSION_BUS_ADDRESS=" + address})
	if got := DetectRuntime(rt); got != Unknown {
		t.Fatalf("impostor KWin service selected %v", got)
	}
	reply, err = owner.RequestName("org.kde.KWin", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register genuine KWin service: %v %v", reply, err)
	}
	if !kwinOnBus(address) {
		t.Fatal("exact KWin service not detected")
	}
	if got := DetectRuntime(rt); got != KDE {
		t.Fatalf("genuine KWin service selected %v", got)
	}
}
