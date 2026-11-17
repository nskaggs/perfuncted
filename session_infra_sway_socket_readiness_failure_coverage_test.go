package perfuncted

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedSwayRejectsStartupWhenEitherRuntimeSocketIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name        string
		readySocket string
		wantError   string
	}{
		{name: "Wayland", readySocket: "ipc", wantError: "wayland socket"},
		{name: "Sway IPC", readySocket: "wayland", wantError: "sway IPC socket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtimeDir, err := createManagedSessionRuntimeDir()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })

			readyPath := filepath.Join(runtimeDir, "wayland-1")
			if tc.readySocket == "ipc" {
				readyPath = filepath.Join(runtimeDir, fmt.Sprintf("sway-ipc.%d.1.sock", os.Getuid()))
			}
			readySocket, err := net.Listen("unix", readyPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = readySocket.Close() })

			binDir := t.TempDir()
			fakeSway := filepath.Join(binDir, "sway")
			if err := os.WriteFile(fakeSway, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			infra := &sessionInfra{
				xdgDir:    runtimeDir,
				wlDisplay: "wayland-1",
				dbusAddr:  "unix:path=/tmp/pf-readiness-bus",
				logDir:    runtimeDir,
				ctx:       context.Background(),
				timeouts:  TimeoutPolicy{Startup: 20 * time.Millisecond, Poll: time.Millisecond},
			}
			err = infra.launchSway(context.Background(), filepath.Join(runtimeDir, "sway.conf"), sessionModeHeadless)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("startup with missing %s socket returned %v", tc.wantError, err)
			}
			if infra.swayCmd == nil {
				t.Fatal("readiness check failed before the compositor process was started")
			}
			waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := infra.swayCmd.wait(waitCtx); err != nil {
				t.Fatalf("fake compositor did not exit cleanly: %v", err)
			}
		})
	}
}
