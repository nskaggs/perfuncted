package perfuncted

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installNestedStartupHelpers(t *testing.T) string {
	t.Helper()

	runtimeRoot := prepareHeadlessHelpers(t, false)
	bin := filepath.Join(runtimeRoot, "bin")
	dbusScript := filepath.Join(runtimeRoot, "synthetic-dbus.py")
	swayScript := filepath.Join(runtimeRoot, "synthetic-sway.py")
	for path, contents := range map[string]string{
		dbusScript: "import os, signal, socket\n" +
			"listener = socket.socket(socket.AF_UNIX)\n" +
			"listener.bind(os.path.join(os.environ['XDG_RUNTIME_DIR'], 'bus'))\n" +
			"listener.listen(1)\n" +
			"signal.pause()\n",
		swayScript: "import os, signal, socket\n" +
			"listeners = []\n" +
			"for name in ('wayland-1', 'sway-ipc.synthetic.sock'):\n" +
			"    listener = socket.socket(socket.AF_UNIX)\n" +
			"    listener.bind(os.path.join(os.environ['XDG_RUNTIME_DIR'], name))\n" +
			"    listener.listen(1)\n" +
			"    listeners.append(listener)\n" +
			"signal.pause()\n",
		filepath.Join(bin, "dbus-daemon"): "#!/bin/sh\nset -eu\nexec python3 \"$PF_SYNTH_DBUS_HELPER\"\n",
		filepath.Join(bin, "sway"): "#!/bin/sh\nset -eu\n" +
			"test \"$WAYLAND_DISPLAY\" = \"$PF_EXPECTED_HOST_WAYLAND\"\n" +
			"test \"$WLR_BACKENDS\" = wayland\n" +
			"test \"$WLR_RENDERER\" = pixman\n" +
			"exec python3 \"$PF_SYNTH_SWAY_HELPER\"\n",
		filepath.Join(bin, "wl-paste"): "#!/bin/sh\nset -eu\nexec python3 -c 'import signal; signal.pause()'\n",
	} {
		mode := os.FileMode(0o600)
		if strings.HasPrefix(contents, "#!/") {
			mode = 0o700
		}
		if err := os.WriteFile(path, []byte(contents), mode); err != nil {
			t.Fatalf("write nested startup helper %q: %v", path, err)
		}
	}
	t.Setenv("PF_SYNTH_DBUS_HELPER", dbusScript)
	t.Setenv("PF_SYNTH_SWAY_HELPER", swayScript)
	return runtimeRoot
}

func TestOpenNestedRuntimeUsesHostWaylandAndOwnsManagedChildren(t *testing.T) {
	root := installNestedStartupHelpers(t)
	hostRuntime := shortPrivateSocketDir(t)
	hostSocket := filepath.Join(hostRuntime, "wayland-host")
	listener, err := net.Listen("unix", hostSocket)
	if err != nil {
		t.Fatalf("listen on synthetic host Wayland socket: %v", err)
	}
	defer listener.Close()
	t.Setenv("XDG_RUNTIME_DIR", hostRuntime)
	t.Setenv("WAYLAND_DISPLAY", filepath.Base(hostSocket))
	t.Setenv("PF_EXPECTED_HOST_WAYLAND", hostSocket)
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := Open(ctx, WithNested(SessionConfig{Timeouts: TimeoutPolicy{Startup: 3 * time.Second, Poll: time.Millisecond}}))
	if err != nil {
		t.Fatalf("Open nested runtime with a live host Wayland socket: %v", err)
	}
	managedRuntime := session.XDG()
	if session.Target().Kind() != TargetNested || filepath.Clean(managedRuntime) == filepath.Clean(hostRuntime) {
		t.Fatalf("nested target routing = kind %q runtime %q, host runtime %q", session.Target().Kind(), managedRuntime, hostRuntime)
	}
	if session.WaylandDisplay() != "wayland-1" || !strings.HasPrefix(session.DBusAddress(), "unix:path="+managedRuntime+string(os.PathSeparator)) {
		t.Fatalf("nested session did not publish its managed desktop endpoints: wayland=%q bus=%q runtime=%q", session.WaylandDisplay(), session.DBusAddress(), managedRuntime)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close nested runtime children: %v", err)
	}
	if _, err := os.Stat(managedRuntime); !os.IsNotExist(err) {
		t.Fatalf("managed nested runtime still exists after Close: %v", err)
	}
	requireNoManagedSessionDirs(t, root)
}

func TestOpenNestedRuntimeWithoutHostWaylandCleansUpStartedBus(t *testing.T) {
	root := installNestedStartupHelpers(t)
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	t.Setenv("PF_EXPECTED_HOST_WAYLAND", "")
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := Open(ctx, WithNested(SessionConfig{Timeouts: TimeoutPolicy{Startup: 3 * time.Second, Poll: time.Millisecond}}))
	if session != nil || err == nil || !strings.Contains(err.Error(), "nested session requires a host Wayland socket") {
		if session != nil {
			_ = session.Close()
		}
		t.Fatalf("nested startup without host Wayland = session %v, error %v", session, err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatalf("inspect synthetic runtime root: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
			t.Fatalf("failed nested startup leaked managed runtime %q", entry.Name())
		}
	}
}
